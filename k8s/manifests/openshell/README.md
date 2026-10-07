# openshell

NVIDIA OpenShell gateway. The chart render and its IPv6 patch are in
`vendor/openshell/`; the Sandbox CRD and controller are `vendor/agent-sandbox/`.
The agents that run on it, and how to start them, are in the repo root's
`agents/`.

The gateway, supervisor and sandbox runtime images are pinned (by digest, in
`vendor/openshell/values.yaml`) to the build of NVIDIA/OpenShell#3702, which
is 0.1.2 plus IPv6 policy DNS egress. 0.1.2 answers every AAAA query in a
sandbox with NODATA, so in this IPv6-only cluster no sandbox can resolve a
name. Drop the pins when a release includes #3702 (nix-lNoiG; the agents/
build fails after 2026-12-01 while they remain). With #3702 the supervisor
logs "Policy DNS IPv6 egress enabled (requested auto, route state
ipv6_only)" at start. Still broken upstream: a policy endpoint given as an
IPv6 literal fails (the supervisor writes the CONNECT host unbracketed), so
use hostnames.

## Apply order (from `k8s/`)

```sh
kubectl apply -f vendor/agent-sandbox/sandbox.yaml
kubectl apply -k vendor/openshell                       # namespace, PKI, gateway
kubectl apply -f manifests/openshell/secret.yaml
kubectl apply -f manifests/openshell/jwt-keys-job.yaml
kubectl apply -f manifests/openshell/service-lb.yaml     # LAN address for the CLI
kubectl -n openshell wait --for=condition=Complete job/openshell-jwt-keys
kubectl -n openshell get certificate                    # all Ready
kubectl -n openshell rollout status deployment/openshell
```

The gateway pod waits (CreateContainerConfigError, ContainerCreating) until
`openshell-kek`, `openshell-jwt-keys` and the cert-manager Secrets exist, then
starts on its own.

These Secrets exist nowhere else, or are costly to lose; nothing should delete
them: `openshell-kek` (in sops here; losing it makes stored provider
credentials unreadable), `openshell-jwt-keys`, `openshell-ca-tls`,
`openshell-server-tls`, `openshell-client-tls`.

## Connecting the CLI

The `openshell` CLI is in the default dev shell (`nix develop`), built at the
gateway's version by the repo root's `packages/openshell/`.

People log in through Authelia (OIDC client `openshell-cli` in
`manifests/authelia/configmap.yaml`). The client certificate only secures the
connection: on Kubernetes it is not a login, and the same certificate is
mounted into every sandbox supervisor. The gateway is on the LAN at
`gateway.openshell.k8s.home.garvey.sh:443` (`service-lb.yaml`); that name is
on its certificate (`certManager.serverDnsNames` in
`vendor/openshell/values.yaml`). The CLI reads `mtls/ca.crt` to trust the
gateway's certificate (issued by the chart's own CA).

```sh
d=~/.config/openshell/gateways/k8s/mtls; mkdir -p "$d"
for k in ca.crt tls.crt tls.key; do
  kubectl -n openshell get secret openshell-client-tls -o "jsonpath={.data.${k//./\\.}}" | base64 -d > "$d/$k"
done
openshell gateway add https://gateway.openshell.k8s.home.garvey.sh --name k8s \
  --oidc-issuer https://auth.home.garvey.sh --oidc-client-id openshell-cli \
  --oidc-audience openshell-cli --oidc-scopes "openid offline_access groups"
```

`gateway add` opens a browser for the Authelia login. Access comes from
Authelia groups: `admins` is the gateway's admin role, `openshell` its user
role (`server.oidc` in `vendor/openshell/values.yaml`). The CLI refreshes the
token itself, and each refresh extends the session by 30 days
(`lifespans.custom.openshell-cli`), so a new login (`openshell gateway login
k8s`) is only needed after a month without use. Running sandboxes never use
this login: their supervisors authenticate with their own ServiceAccount
tokens.

Every gateway RPC except `Health` refuses a call without a valid Authelia token,
including with the client certificate alone or a forged token (checked
2026-10-07, nix-yIyr3). `Health` and `/.well-known/openid-configuration`
answer anyone; they give the version and the public signing keys.

## Upgrading

Bump `VERSION` in `vendor/openshell/chart.env`, re-render, and bump the image in
`jwt-keys-job.yaml` and the CLI version and hashes in the repo root's
`packages/openshell/outputs.nix` to match. Re-check the env patch in
`vendor/openshell/kustomization.yaml` against the new chart's
`templates/gateway-config.yaml` and the gateway's bind options.

## Database

The gateway keeps its state (sandboxes, providers, profiles) in the `openshell` database on
the shared postgres (`manifests/postgres/`), not in the SQLite file the chart defaults to.
The chart's `server.externalDbSecret` (`vendor/openshell/values.yaml`) makes it read
`OPENSHELL_DB_URL` from `db-secret.yaml`. The URL has no password: the gateway logs in as
the `openshell` role with the client certificate in `postgres-client-certificate.yaml`
(CN `openshell`, URI SAN `spiffe://home.garvey.sh/ns/openshell/sa/openshell`, from the
SPIFFE CA, 24h), mounted at `/etc/openshell-pg` by `vendor/openshell/kustomization.yaml`.

Apply `postgres-client-certificate.yaml` and `db-secret.yaml` before
`kubectl apply -k vendor/openshell`, and the role and database (`manifests/postgres/`)
before those. The gateway is a Deployment with no volume of its own.

A fresh database starts empty. Re-create the provider and profile as in `agents/README.md`
("Build and register"); sandboxes that existed before the move are unknown to the gateway
and their pods and volumes have to be deleted by hand.
