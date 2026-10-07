# temporal

Temporal workflow engine at `https://temporal.garvey.sh`, backed by the shared
single-replica Postgres. `temporalio/auto-setup` runs frontend, history,
matching and worker in one process and creates + migrates the schema on start,
so there is no separate schema Job.

**The UI has no authentication.** `temporal.garvey.sh` resolves from the open
internet and the LB address is a globally routable GUA; inbound IPv6 is
default-deny at the router, so it is not reachable from outside today. Revisit
with authelia forward-auth or `TEMPORAL_DISABLE_WRITE_ACTIONS` before opening
anything up.

## Certificate (do this first — it is IP-independent)

Follow `../acme-dns/ONBOARDING.md` by hand for `temporal.garvey.sh`. The manual
runbook is the only path — the `AcmeDnsCert` controller was never deployed and
its manifests were removed (see "Status: not deployed" in that doc).

1. `POST /register` against a port-forwarded `acme-dns-api` with
   `{"allowfrom":["2001:470:482f:100::/56"]}`. Save the response with
   `curl -o` — an orphaned account is unrecoverable.
2. Cloudflare CNAME `_acme-challenge.temporal.garvey.sh` → `<fulldomain>.`,
   DNS-only.
3. Merge the account under key `temporal.garvey.sh` into
   `../acme-dns/secret.yaml`, commit, and apply it.
4. `certificate.yaml` uses `letsencrypt-acmedns-prod`. When onboarding a new
   name, point it at `letsencrypt-acmedns-staging` first; once that reports
   Ready, flip to prod and delete the `temporal-tls` Secret so cert-manager
   re-issues.

## Apply order

```bash
kubectl apply -f manifests/temporal/namespace.yaml
kubectl apply -f manifests/temporal/secret.yaml
kubectl apply -f manifests/temporal/certificate.yaml
kubectl apply -f manifests/temporal/service-account.yaml
kubectl apply -f manifests/temporal/postgres-client-certificate.yaml
kubectl apply -f manifests/temporal/deployment.yaml
kubectl apply -f manifests/temporal/service.yaml
kubectl apply -f manifests/temporal/frontend-lb-service.yaml
kubectl apply -f manifests/temporal/caddy-configmap.yaml
kubectl apply -f manifests/temporal/ui-deployment.yaml
kubectl apply -f manifests/temporal/ui-service.yaml
kubectl apply -f manifests/temporal/service-monitor.yaml
```

The shared postgres (and its role and databases) must exist before the server, and
the client certificate Secret before its pod starts,
and `temporal-tls` must exist before the UI pod starts or Caddy crashloops on
the missing `/certs`.

Split-horizon DNS is `"temporal.garvey.sh" = "2001:470:482f:2::5002";` in
`garveyShOverrides` in `modules/networking/dns.nix`; deploy the router with the
deploy tool after changing it.

## The database

Temporal uses the `temporal` and `temporal_visibility` databases on the shared
postgres, as the `temporal` role. There is no password: the pod presents the client
certificate in `postgres-client-certificate.yaml` (CN `temporal`, URI SAN
`spiffe://home.garvey.sh/ns/temporal/sa/temporal`, from the SPIFFE CA, 24h), and
`manifests/postgres/cluster.yaml` accepts only that for the role. The server certificate
is verified against the same CA. `temporal-db`'s `POSTGRES_PWD` is therefore unused, but
`POSTGRES_USER` is not.

auto-setup reads two sets of TLS variables, `SQL_*` for the server and `POSTGRES_TLS_*` for
its schema tool, and the second inverts host verification; `deployment.yaml` sets both.
`SKIP_DB_CREATE` is set because the operator creates the databases and the role cannot
connect to the `postgres` database that `CREATE DATABASE` needs.

The pod must be able to read the key as uid 1000: the Secret volume is mode 0440 with
`fsGroup: 1000`, and lib/pq refuses anything looser.

## The IPv6 gotcha

Temporal's config template defaults every service's `bindOnIP` to `127.0.0.1`,
so `BIND_ON_IP: "::"` in `deployment.yaml` is load-bearing — without it the pod
is unreachable on this IPv6-only cluster. `TEMPORAL_BROADCAST_ADDRESS` is fed
from `status.podIP`; without it ringpop membership never forms.

`TEMPORAL_AUTH_AUTHORIZER` and `TEMPORAL_AUTH_CLAIM_MAPPER` are deliberately
unset. With both unset the template omits the `publicClient` block entirely —
otherwise it renders `hostPort` by concatenating `BIND_ON_IP + ":" + port`,
giving the unparseable `":::7233"`.

If membership still fails, the fallback is to mount our own
`config_template.yaml` via ConfigMap at
`/etc/temporal/config/config_template.yaml` with bracketed addresses hardcoded.

## Metrics are unprefixed

Temporal's docker config template exposes `timerType` but no metrics `prefix`,
so the ~489 series land in Prometheus under bare names — `service_requests`,
`errors`, `action`, `ack_level_update` — not `temporal_*`. Filter by
`{namespace="temporal"}` when querying. If the collisions ever become a problem,
add a `metricRelabelings` prefix rule to `service-monitor.yaml`.

## Workers

In-cluster workers are ordinary application Deployments in this namespace. Point
them at `temporal-frontend.temporal.svc:7233` and give each its own task queue.
Nothing in these manifests needs to change for those.

Off-cluster workers use `frontend-lb-service.yaml`, a LoadBalancer on
`2001:470:482f:2::7233` that k8s_gateway publishes as
`temporal.temporal.k8s.home.garvey.sh`. A ClusterIP is not an option for them:
the service CIDR is `fd43::/112`, which is not routed off-cluster, and Cilium
does not serve ClusterIP to external clients by default.

That endpoint carries no TLS and no auth, exactly like the in-cluster one --
anything on the LAN that can route to the LB IP can drive Temporal. That is the
same posture as the UI; revisit both together.
