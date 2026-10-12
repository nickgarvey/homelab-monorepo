# openshell

NVIDIA OpenShell gateway. The chart render and its IPv6 patch are in
`vendor/openshell/`; the Sandbox CRD and controller are `vendor/agent-sandbox/`.
The agents that run on it, and how to start them, are in the repo root's
`agents/`.

The gateway, supervisor and sandbox runtime are our own builds of the chart's
version with NVIDIA/OpenShell#3702 (IPv6 policy DNS egress) applied, from the
repo root's `packages/openshell/` and `patches/openshell-ipv6-policy-dns.patch`,
pushed to `oci.garvey.sh/openshell/` and pinned by digest in
`vendor/openshell/values.yaml`. Upstream answers every AAAA query in a sandbox
with NODATA, so in this IPv6-only cluster no sandbox could resolve a name. Go
back to the chart's images when a release includes #3702 (nix-lNoiG; the
agents/ build fails after 2026-12-01 while the patch remains). With #3702 the supervisor
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
starts on its own. The gateway's certificates come from the spiffe-ca
ClusterIssuer, so `manifests/cert-manager/spiffe-policies.yaml` must be applied
first (policies `spiffe-openshell-gateway` and `spiffe-openshell-client`).

These Secrets exist nowhere else, or are costly to lose; nothing should delete
them: `openshell-kek` (in sops here; losing it makes stored provider
credentials unreadable) and `openshell-jwt-keys`.

## Certificates

The gateway's server certificate (`openshell-server-tls`, the in-cluster
Service names and the LAN name) and the chart's client certificate
(`openshell-client-tls`, used by the CLI and the sandbox supervisors) are
issued by the spiffe-ca ClusterIssuer, patched in
`vendor/openshell/kustomization.yaml`. Each Secret's `ca.crt` is the spiffe-ca
root, which the CLI, the supervisors, the gateway's peers and the agents worker
all verify the gateway with, and which the gateway verifies client
certificates with. The root is long-lived and replaced only by hand, so no copy
of it goes stale. The chart also renders its own CA (`openshell-ca`, its
self-signed and CA Issuers); the kustomization deletes it, since nothing uses it.

## Connecting the CLI

The `openshell` CLI is in the default dev shell (`nix develop`), built at the
gateway's version by the repo root's `packages/openshell/`.

People log in through Authelia (OIDC client `openshell-cli` in
`manifests/authelia/configmap.yaml`). The chart's client certificate
(`openshell-client`) only secures the connection: it has no OU, so it carries
no role (see "Certificate users"). Supervisors get only its CA. The gateway is on the LAN at
`gateway.openshell.k8s.home.garvey.sh:443` (`service-lb.yaml`); that name is
on its certificate. The CLI reads `mtls/ca.crt` to trust the gateway's
certificate (the spiffe-ca root, "Certificates").

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

## Certificate users

Programs log in with a client certificate instead (`mtls_auth`, turned on in
`vendor/openshell/kustomization.yaml`). A request without a bearer token
becomes the user named by its certificate's CN, with the certificate's OU
values as its roles. The gateway trusts client certificates under the
spiffe-ca root.

Only approver-policy decides who gets a role: a spiffe-ca certificate carrying
`OU=openshell` or `OU=admins` is a gateway user or admin, so no policy in
`manifests/cert-manager/spiffe-policies.yaml` may allow an OU other than its
own exact identity's. Today one does: `spiffe-agents-worker`
(`CN=agents-worker, OU=openshell`, `manifests/agents/`). The chart's
`openshell-client` certificate has no OU, so it gets no role. A certificate
from the SPIFFE CA must have no URI SAN: the gateway's TLS library
(rustls-webpki) does not process the root's URI name constraint and refuses
the handshake with `NameConstraintViolation`, which also keeps SPIRE's SVIDs
out. A user role reaches only the workspaces the user is a member of, and
profiles and providers belong to a workspace, so a new one has none:

```sh
openshell workspace create --name agents
openshell workspace member add --workspace agents --subject agents-worker --role user
```

Every gateway RPC except `Health` refuses a call without a valid Authelia token,
including with the client certificate alone or a forged token (checked
2026-10-07, nix-yIyr3). `Health` and `/.well-known/openid-configuration`
answer anyone; they give the version and the public signing keys.

## Provider token grants (SPIFFE)

Each sandbox is two pods in namespace `openshell`, both with ServiceAccount
`openshell-sandbox`: the workload pod, where the agent runs, and
`os-supervisor-<sandbox-id>`, whose proxy carries all of the sandbox's
traffic. `provider_spiffe_workload_api_socket_path`
(`vendor/openshell/values.yaml`) mounts the SPIRE Workload API socket into the
supervisor pod only (and into the gateway pod). For a provider whose profile
credential has a `token_grant`, the supervisor fetches a JWT-SVID (audience:
the profile's `jwt_svid_audience`, or else the token endpoint URL), POSTs it as
the `client_assertion` of a `client_credentials` grant to `token_endpoint`,
caches the returned token, and injects it into matching requests. The token
and the socket never reach the workload pod. The token endpoint must be
`https://` (verified against the supervisor image's system CAs) or `http://`
to a `<svc>.<ns>.svc[.cluster.local]` name.

The supervisor's identity is
`spiffe://home.garvey.sh/agent/<type>/<sandbox-id>`, from the
`openshell-sandboxes` ClusterSPIFFEID (`vendor/spire/values.yaml`). `<type>` is
the supervisor pod's `agents.garvey.sh/type` label. The agents worker sets that
label on the sandbox template, but OpenShell 0.1.3 puts template labels on the
workload pod only, so supervisors are `agent/untyped/<sandbox-id>` until it
copies them over.

Apply the SPIRE side first, so no supervisor starts with the socket but
without its identity:

```sh
kubectl apply -f vendor/spire/spire.yaml
kubectl apply -k vendor/openshell
```

The gateway reads the setting at start; a sandbox already running gets the
socket when its supervisor pod is next created (a stop and start).

## Upgrading

Bump `VERSION` in `vendor/openshell/chart.env` and the version and hashes in
the repo root's `packages/openshell/outputs.nix` to match (rebasing
`patches/openshell-ipv6-policy-dns.patch` while it is carried), and re-render.
Then build and push the three images and pin their digests in
`vendor/openshell/values.yaml`, and the gateway's in `jwt-keys-job.yaml`
(from the repo root; `nix flake check` runs the image test):

```sh
for n in gateway supervisor sandbox; do
  skopeo copy docker-archive:$(nix build --no-link --print-out-paths .#openshell-$n-image) \
    docker://oci.garvey.sh/openshell/$n:$(nix eval --raw .#openshell-$n-image.imageTag)
done
```

Re-check the env patches in
`vendor/openshell/kustomization.yaml` against the new chart's
`templates/gateway-config.yaml` and the gateway's bind, client-CA and
`mtls_auth` options.

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

A fresh database starts empty. `nix run .#agents-apply` re-creates the `agents` workspace,
the worker's membership, profiles and providers (`agents/README.md`); sandboxes that existed
before the move are unknown to the gateway and their pods and volumes have to be deleted by
hand.
