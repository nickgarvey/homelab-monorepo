# temporal

Temporal workflow engine at `https://temporal.garvey.sh`, backed by the shared
single-replica Postgres. One process runs all five services (frontend, internal
frontend, history, matching, worker) from our own image,
`oci.garvey.sh/temporal-server` (`packages/temporal-server`), with its config in
`config-configmap.yaml`. Two init containers migrate the schema first.

Workers authenticate with a client certificate and get only the roles
`authz-configmap.yaml` grants their identity; people log in to the UI through
Authelia and get roles from their Authelia groups (see "Access").

## Certificate (do this first — it is IP-independent)

`temporal.garvey.sh` is in `certManagerAcme` (`modules/networking/dns.nix`), and its
Cloudflare CNAME `_acme-challenge.temporal.garvey.sh` points at
`temporal.challenge.home.garvey.sh`, where cert-manager writes the DNS-01 TXT
(`../cert-manager/ONBOARDING.md`). `certificate.yaml` uses `letsencrypt-rfc2136-prod`.

## Apply order

```bash
kubectl apply -f manifests/temporal/namespace.yaml
kubectl apply -f manifests/temporal/secret.yaml
kubectl apply -f manifests/temporal/certificate.yaml
kubectl apply -f manifests/temporal/service-account.yaml
kubectl apply -f manifests/temporal/postgres-client-certificate.yaml
kubectl apply -f manifests/temporal/frontend-certificate.yaml
kubectl apply -f manifests/temporal/authz-configmap.yaml
kubectl apply -f manifests/temporal/config-configmap.yaml
kubectl apply -f manifests/temporal/deployment.yaml
kubectl apply -f manifests/temporal/service.yaml
kubectl apply -f manifests/temporal/caddy-configmap.yaml
kubectl apply -f manifests/temporal/ui-oidc-secret.yaml
kubectl apply -f manifests/temporal/ui-service-account.yaml
kubectl apply -f manifests/temporal/ui-spiffe-helper-configmap.yaml
kubectl apply -f manifests/temporal/ui-deployment.yaml
kubectl apply -f manifests/temporal/ui-service.yaml
kubectl apply -f manifests/temporal/service-monitor.yaml
```

The shared postgres (and its role and databases) must exist before the server, and
both certificate Secrets before its pod starts (`cluster-temporal` and
`cluster-temporal-frontend` in `manifests/cert-manager/cluster-policies.yaml` approve them),
and `temporal-tls` must exist before the UI pod starts or Caddy crashloops on
the missing `/certs`.

Split-horizon DNS is `"temporal.garvey.sh" = "2001:470:482f:2::5002";` in
`garveyShOverrides` in `modules/networking/dns.nix`; deploy the router with the
deploy tool after changing it.

## The database

Temporal uses the `temporal` and `temporal_visibility` databases on the shared
postgres, as the `temporal` role. There is no password: the pod presents the client
certificate in `postgres-client-certificate.yaml` (CN `temporal`, URI SAN
`spiffe://home.garvey.sh/ns/temporal/sa/temporal`, from the cluster CA, 24h), and
`manifests/postgres/cluster.yaml` accepts only that for the role. The server certificate
is verified against the same CA. The role's password in `temporal-db` (`secret.yaml`) is
unused.

The init containers run `temporal-sql-tool update-schema` with the schemas embedded in the
image (`--schema-name postgresql/v12/temporal` and `.../visibility`), so the schema always
matches the server version; on a current schema they do nothing. There is no
`create-database` or `setup-schema` step: the operator creates the databases
(`manifests/postgres/databases.yaml`), and the role cannot connect to the `postgres`
database that `CREATE DATABASE` needs. `numHistoryShards: 4` in the config is fixed for the
life of the databases.

The pod must be able to read the key as uid 1000: the Secret volume is mode 0440 with
`fsGroup: 1000`, and lib/pq refuses anything looser.

## Access

The frontend requires a client certificate that chains to the `cluster-ca` root
(`ca.crt` of `temporal-frontend-tls`), and serves `temporal-frontend-tls`, valid for
`temporal-frontend.temporal.svc`. The server (`packages/temporal-server`, started with
`--authz-table`) takes the caller's identity from the certificate's single URI SAN and
looks it up in `authz-configmap.yaml`; an identity that is not listed can do nothing.
Roles are levels per Temporal namespace (`read` < `write` < `admin`), or for every
namespace under `system`. Temporal's `worker` role on its own allows nothing; workers
need `write`.

A SPIRE identity (`spiffe://home.garvey.sh/ns/<namespace>/sa/<service account>`) works as
a client certificate, so adding a worker is:

1. Give it its own ServiceAccount; pods sharing one share the identity.
2. Add the identity to `authz-configmap.yaml` with a role in its Temporal namespace,
   apply it and `kubectl -n temporal rollout restart deploy/temporal` (the table is read
   at start).
3. Have the worker present its X.509-SVID: go-spiffe's `workloadapi.NewX509Source` in Go,
   or a spiffe-helper sidecar writing files (as `ui-deployment.yaml` does), with the CSI
   volume `csi.spiffe.io`. Dial `temporal-frontend.temporal.svc:7233`, verify the server
   against the SVID bundle (the same root), server name `temporal-frontend.temporal.svc`.

The table matches the URI SAN only. Never key it on a CN or DNS name: SPIRE puts an
entry's DNS name in an SVID's CN (`manifests/spire/README.md`).

### People, through the UI

The UI logs users in through Authelia (OIDC client `temporal-ui` in
`manifests/authelia/configmap.yaml`, two-factor) and forwards each request with the
user's access token. The UI itself only checks that a token is present, so the
frontend does the real check: it verifies the token against Authelia's JWKS
(`aud` must be `temporal`) and takes roles from the `groups` claim, using groups named
`temporal-<namespace>-<role>` (`temporal-default-write`) or `temporal-system-<role>`
for every namespace (`temporal-system-admin`). Other groups are ignored. With a token,
only the token counts: the UI's own certificate identity has no table entry, so a
request with a junk, forged or missing token gets nothing, whether it comes through
`temporal.garvey.sh` or straight to the UI pod.

Grant a person access by adding the group to their user in Authelia's
`users_database.yml` (`manifests/authelia/`, sops) and restarting Authelia; they log in
again to get a token with it. Authelia refuses the login of anyone with none of the
groups listed in the client's `temporal` authorization policy
(`manifests/authelia/configmap.yaml`), so a new Temporal namespace adds its
`temporal-<namespace>-<role>` groups there too. Without that refusal the UI would loop:
it sends every 403 from Temporal back to its login.

## Ports

Every Temporal service binds `127.0.0.1`, the frontend included. Temporal binds a
service's ringpop membership port wherever it binds its gRPC port, and membership has no
authentication, so a frontend on the pod IP would also put 6933 on the LAN. The
`frontend-forward` sidecar (socat, from the same image) is what listens on the pod IP's
7233 and forwards to the frontend on loopback; it is plain TCP, so TLS runs end to end
between the client and Temporal. The only other listener on the pod IP is Prometheus
metrics on 9090. The HTTP API is off (`httpPort: 0`). The probes run `temporal-server
healthcheck` in the pod, which asks the internal frontend on loopback: a kubelet gRPC probe
can neither present a client certificate nor reach loopback.

This depends on one replica: with more, the services would have to reach each other
over the network (and then need internode mTLS).

Temporal's own system worker (schedules, batch operations, deletes) talks to the
`internal-frontend` service on `127.0.0.1:7236`, which skips authorization. Break-glass
admin uses the same path, gated by cluster access:

```bash
kubectl -n temporal port-forward deploy/temporal 7236:7236 &
temporal operator namespace list --address 127.0.0.1:7236
```

## Image

`packages/temporal-server` builds both binaries and the image; the tag is the Temporal
version plus the package's store hash, and changes whenever anything in that directory
changes. From the repo root:

```bash
nix build .#temporal-server-image
skopeo copy docker-archive:result docker://oci.garvey.sh/temporal-server:$(nix eval --raw .#temporal-server-image.imageTag)
```

then set the tag in `deployment.yaml` (one `&image` anchor) and apply. Bumping Temporal is
`go.temporal.io/server` in `packages/temporal-server/go.mod`; the init containers then run
the new version's migrations.

## Metrics are unprefixed

The config sets no metrics `prefix`, so the ~489 series land in Prometheus under bare names — `service_requests`,
`errors`, `action`, `ack_level_update` — not `temporal_*`. Filter by
`{namespace="temporal"}` when querying. If the collisions ever become a problem,
add a `metricRelabelings` prefix rule to `service-monitor.yaml`.

## Workers

In-cluster workers are ordinary application Deployments, onboarded as in "Access", each
with its own task queue.

There is no off-cluster path yet. The service CIDR (`fd43::/112`) is not routed
off-cluster and the frontend has no LoadBalancer. An off-cluster worker needs one (a
LoadBalancer to the pod's 7233; the server certificate would then also need that name)
and a client certificate from cert-manager with an approver policy, as postgres's
clients have.
