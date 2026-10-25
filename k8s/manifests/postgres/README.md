# postgres

The shared PostgreSQL for the cluster's services, run by the CloudNativePG
operator (`vendor/cloudnative-pg`). One `Cluster` named `postgres`, a single
instance on `ceph-rbd`: Ceph replicates the volume, and there is no streaming
replica, so no failover can promote a copy that is behind.
Clients connect to `postgres-rw.postgres.svc:5432` with their own role and
password. There is no LoadBalancer: nothing outside the cluster needs it, and
every LoadBalancer Service is published in public DNS.

Do not put bootstrap-critical services on it: they issue the certificates and secrets this cluster needs to come up.

## Apply order (from `k8s/`)

1. Operator, then the backup plugin (needs cert-manager):
   ```sh
   kubectl apply --server-side -f vendor/cloudnative-pg/cloudnative-pg.yaml
   kubectl -n cnpg-system rollout status deploy/cnpg-cloudnative-pg
   kubectl apply --server-side -f vendor/plugin-barman-cloud/manifest.yaml
   kubectl -n cnpg-system rollout status deploy/barman-cloud
   ```
2. `kubectl apply -f manifests/postgres/namespace.yaml`
3. `kubectl apply -f manifests/postgres/secret.yaml -f manifests/postgres/litellm-secret.yaml`,
   then wait for the sops-secrets-operator to create `postgres-temporal`,
   `postgres-backups-s3` and `postgres-litellm` (`kubectl -n postgres get secret`).
4. `kubectl apply -f manifests/postgres/objectstore.yaml -f manifests/postgres/cluster.yaml`
   and wait: `kubectl -n postgres get cluster postgres` shows 2/2 ready.
5. `kubectl apply -f manifests/postgres/databases.yaml -f manifests/postgres/scheduled-backup.yaml -f manifests/postgres/pod-monitor.yaml`
   (`immediate: true` takes the first backup straight away).

## Adding an app

1. Add a `kubernetes.io/basic-auth` secret template to `secret.yaml`
   (`sops secret.yaml`), with `username` and `password`, or put it in a
   SopsSecret of its own, as `litellm-secret.yaml` is (with a rule for its path
   in the repo root's `.sops.yaml`).
2. Add a role under `managed.roles` in `cluster.yaml`, pointing at that secret.
3. Add a `Database` to `databases.yaml`, owned by the role.
4. Restrict it to its databases. Add two lines to `postgresql.pg_hba` in
   `cluster.yaml`, `host <dbs> <role> all scram-sha-256` then `host all <role> all reject`.
   A role that logs in with a certificate instead uses
   `hostssl <dbs> <role> all cert clientname=DN map=spiffe` for the first line and also
   adds `spiffe "CN=<role>" <role>` to `postgresql.pg_ident` (see "Client certificates").
   Postgres grants CONNECT on every database to everyone and the operator does not
   manage grants, so close that once per database (peer login as the superuser):
   ```sh
   kubectl -n postgres exec postgres-1 -c postgres -- psql -U postgres -c \
     'REVOKE CONNECT ON DATABASE <db> FROM PUBLIC; GRANT CONNECT ON DATABASE <db> TO <role>;'
   ```
   Do not revoke on the `postgres` database: the metrics exporter connects to it.
5. The app connects to `postgres-rw.postgres.svc` with the same credentials,
   kept in its own namespace's SopsSecret.

## Backups

WAL is archived continuously and a base backup runs daily at 03:00 UTC, both to
the Garage bucket `postgres-backups` (key `postgres-backups-key`), kept 14 days.
The RBD volumes are also in the daily `ceph-backup` snapshots, but those are
only crash-consistent; use these for a restore.

Take one by hand, and see them:
```sh
kubectl -n postgres create -f - <<'YAML'
apiVersion: postgresql.cnpg.io/v1
kind: Backup
metadata:
  generateName: manual-
spec:
  method: plugin
  cluster: {name: postgres}
  pluginConfiguration: {name: barman-cloud.cloudnative-pg.io}
YAML
kubectl -n postgres get objectstore postgres-backups -o jsonpath='{.status.serverRecoveryWindow}'
```
`kubectl -n postgres get backup` lists no `Backup` objects; the recovery window
(oldest restorable point, last base backup) is in the ObjectStore status above.

### Restore

A restore builds a new `Cluster` from the bucket; it never touches the
running one. This procedure was tested on 2026-10-25 into a scratch namespace.
That restore took about four minutes and matched the live row counts in every
database.

1. To restore up to now, first close the current WAL segment so it gets
   archived, because recovery replays only archived WAL:
   ```sh
   kubectl -n postgres exec postgres-1 -c postgres -- psql -U postgres -c 'select pg_switch_wal()'
   ```
2. Create the target namespace and copy in the S3 credentials (the
   sops-secrets-operator only writes them into `postgres`):
   ```sh
   kubectl create namespace postgres-restore-test
   kubectl -n postgres get secret postgres-backups-s3 -o json \
     | jq '{apiVersion,kind,type,data,metadata:{name:.metadata.name,namespace:"postgres-restore-test"}}' \
     | kubectl apply -f -
   ```
3. Apply an ObjectStore with the same spec as `objectstore.yaml` but in the
   new namespace and without `retentionPolicy`, plus this Cluster:
   ```yaml
   apiVersion: postgresql.cnpg.io/v1
   kind: Cluster
   metadata:
     name: restored
     namespace: postgres-restore-test
   spec:
     instances: 1
     imageName: ghcr.io/cloudnative-pg/postgresql:17.11-standard-trixie
     storage: {storageClass: ceph-rbd, size: 20Gi}
     bootstrap:
       recovery:
         source: origin
         # For a point-in-time restore instead of the latest:
         # recoveryTarget: {targetTime: "2026-10-25 18:00:00+00"}
     externalClusters:
       - name: origin
         plugin:
           name: barman-cloud.cloudnative-pg.io
           parameters:
             barmanObjectName: postgres-backups
             serverName: postgres
   ```
   There is no `spec.plugins`, so nothing in the restored cluster archives to
   `postgres-backups`. A cluster that replaces the live one needs the
   `plugins` block back with its own `serverName`, because two clusters must
   not archive under the same name. The restored cluster also lacks the
   certificates, roles and `pg_hba` from `cluster.yaml`: the roles themselves
   come back with the data, but the rest must be carried over.
4. Wait for `kubectl -n postgres-restore-test get cluster restored` to show a
   healthy state. Partway through, the event "Primary instance is being
   restarted without a switchover" is expected: the pod first starts with the
   barman sidecar used for the recovery, then the operator rolls it once to
   remove that sidecar.
5. Check it. Compare `count(*)` of each table between the two clusters, and
   check the recovery end point:
   `cat $PGDATA/pg_wal/*.history` in the new pod shows the LSN where recovery
   stopped. UNLOGGED tables come back empty: that is Postgres behaviour, not
   data loss. Currently these are `mas.queue_leader`,
   `synapse.worker_read_write_locks` and
   `synapse.worker_read_write_locks_mode`, all of them locks or leases.
6. A test restore ends with `kubectl delete namespace postgres-restore-test`,
   which deletes its PVC along with it.

## Access control

Roles log in with a client certificate ("Client certificates" below), except
`litellm`, which uses a scram-sha-256 password over TLS (`cluster.yaml`
says why). The operator's default pg_hba rejects everything else. No
NetworkPolicy is applied yet.

## Client certificates (cluster CA)

The cluster validates client certificates against the trust domain's CA
(`certificates.clientCASecret`, `client-ca.yaml`), and its `streaming_replica`
certificate is issued from that CA too (`replication-certificate.yaml`). A role can log in
with a certificate whose subject is exactly `CN=<role>` and whose URI SAN is its SPIFFE ID,
once `cluster.yaml` has a `hostssl <dbs> <role> all cert clientname=DN map=spiffe` line in
`postgresql.pg_hba` (the operator places user rules before its password catch-all) and a
`spiffe "CN=<role>" <role>` line in `postgresql.pg_ident`. Postgres matches the whole
subject DN, not the URI, until `clientname=URI` ships in a release. The DN, not just the
CN: the CA signs for the whole trust domain, and a certificate with the role's CN but
any other subject must not log in. Issuing the certificate is gated by
`manifests/cert-manager/cluster-policies.yaml`, whose policies allow only the CN.

The server certificate (`server-certificate.yaml`) is issued by the same CA, so a client
verifies the server with the CA it already has and nothing needs copying out of this
namespace when a certificate rotates. The `temporal` role already logs in this way
(`pg_hba` in `cluster.yaml`); `manifests/temporal/README.md` shows the client side.
