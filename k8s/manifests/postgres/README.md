# postgres

The shared PostgreSQL for the cluster's services, run by the CloudNativePG
operator (`vendor/cloudnative-pg`). One `Cluster` named `postgres`, two
instances (primary + streaming replica) on `ceph-rbd`, never on the same node.
Clients connect to `postgres-rw.postgres.svc:5432` with their own role and
password. There is no LoadBalancer: nothing outside the cluster needs it, and
every LoadBalancer Service is published in public DNS.

Do not put bootstrap-critical services on it (acme-dns stays on SQLite): they
issue the certificates and secrets this cluster needs to come up.

## Apply order (from `k8s/`)

1. Operator, then the backup plugin (needs cert-manager):
   ```sh
   kubectl apply --server-side -f vendor/cloudnative-pg/cloudnative-pg.yaml
   kubectl -n cnpg-system rollout status deploy/cnpg-cloudnative-pg
   kubectl apply --server-side -f vendor/plugin-barman-cloud/manifest.yaml
   kubectl -n cnpg-system rollout status deploy/barman-cloud
   ```
2. `kubectl apply -f manifests/postgres/namespace.yaml`
3. `kubectl apply -f manifests/postgres/secret.yaml`, then wait for the
   sops-secrets-operator to create `postgres-temporal` and
   `postgres-backups-s3` (`kubectl -n postgres get secret`).
4. `kubectl apply -f manifests/postgres/objectstore.yaml -f manifests/postgres/cluster.yaml`
   and wait: `kubectl -n postgres get cluster postgres` shows 2/2 ready.
5. `kubectl apply -f manifests/postgres/databases.yaml -f manifests/postgres/scheduled-backup.yaml -f manifests/postgres/pod-monitor.yaml`
   (`immediate: true` takes the first backup straight away).

## Adding an app

1. Add a `kubernetes.io/basic-auth` secret template to `secret.yaml`
   (`sops secret.yaml`), with `username` and `password`.
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
kubectl -n postgres get backup
```

To restore, create a new `Cluster` with `bootstrap.recovery` and
`externalClusters` pointing at the `postgres-backups` ObjectStore (see the
CloudNativePG recovery docs); try it in a scratch namespace first.

## Access control

Authentication is per-role scram-sha-256 passwords (the operator's default
pg_hba rejects everything else). No NetworkPolicy is applied yet.

## Client certificates (SPIFFE CA)

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
`manifests/cert-manager/spiffe-policies.yaml`, whose policies allow only the CN.

The server certificate (`server-certificate.yaml`) is issued by the same CA, so a client
verifies the server with the CA it already has and nothing needs copying out of this
namespace when a certificate rotates. The `temporal` role already logs in this way
(`pg_hba` in `cluster.yaml`); `manifests/temporal/README.md` shows the client side.
