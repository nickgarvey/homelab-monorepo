# agents

The agents worker's namespace (design: `docs/agent-sessions-design.md`). The
worker runs agent sessions in OpenShell sandboxes; the agents themselves are in
the repo root's `agents/`.

## The worker

`deployment.yaml` runs `agents-worker` (`packages/agents`): the `ChatSession`,
`SessionCoordinator`, `SandboxReaper` and `Wakeup` workflows on task queue
`agents` in Temporal namespace `agents`. Each conversation is a `ChatSession`
with workflow ID `chat/<type>/<conversation>`; a message is its Update
`message`, sent with Update-with-Start, and the Update's result is the reply.
A conversation of a type with wake-ups also has a `Wakeup`,
`wakeup/<type>/<conversation>` ("Wake-ups" in `agents/README.md`). Sessions
keep their records in the `agents` database, and pi's session files go to RGW
bucket `agent-sessions`. The agent types are read from the
`agents-desired-state` ConfigMap, which `nix run .#agents-apply` writes; a
publish reaches the next turn without a restart.

### The reaper

A stopped sandbox keeps its PVC. The `sandbox-reaper` Schedule, which the
worker creates at start (and puts back if it was changed), runs the
`SandboxReaper` workflow hourly, skipping a run while the last is still going.
It deletes, each with its own activity that checks again first:

1. **Expired**: sandboxes stopped longer than their type's
   `session.retention` (7 days unless declared; also for a type no longer
   published), and their PVCs. A row a message has claimed since, or that
   stopped again since, is skipped.
2. **Stuck**: rows left in `deleting` by a crash.
3. **Orphans**: sandboxes labelled `managed-by=agents-worker` whose name no
   live row holds. Names are recorded before a sandbox is created, so a
   create in progress is never taken for one.
4. **Vanished**: stopped rows whose sandbox is gone from the gateway (deleted
   by hand) are marked deleted, so the next message creates a fresh one.

Only stopped rows are expired or marked vanished; a running session's lost
sandbox is replaced by its next turn. The run's result counts each step and
names what it deleted. To run it now, from the break-glass path:

```sh
temporal schedule trigger --schedule-id sandbox-reaper --address 127.0.0.1:7236 --namespace agents
```

The gateway's certificate is issued by the spiffe-ca ClusterIssuer, so the
worker trusts it with the `ca.crt` of its own gateway certificate's Secret,
the same root.

The image is `nix build .#agents-worker-image`; its tag is the store hash of
`packages/agents`, so any change there changes the tag in `deployment.yaml`:

```sh
nix build .#agents-worker-image
skopeo copy docker-archive:result docker://oci.garvey.sh/agents-worker:$(nix eval --raw .#agents-worker-image.imageTag)
```

To send a message by hand, from the Temporal break-glass path ("Ports" in
`manifests/temporal/README.md`):

```sh
temporal workflow execute-update-with-start --address 127.0.0.1:7236 \
  --namespace agents --task-queue agents --type ChatSession \
  --workflow-id chat/pi/<conversation> --id-conflict-policy UseExisting \
  --input '{"Type":"pi","Conversation":"<conversation>"}' \
  --update-name message --update-input '{"Text":"hello"}'
```

## Identity at the OpenShell gateway

The worker logs in to the gateway with `openshell-certificate.yaml`: a 24h
client certificate from the SPIFFE CA with subject
`CN=agents-worker, OU=openshell`, which the gateway reads as user
`agents-worker` with its user role (see "Certificate users" in
`manifests/openshell/README.md`). The `spiffe-agents-worker` policy in
`manifests/cert-manager/spiffe-policies.yaml` allows that subject only to
Certificates in this namespace. The OpenShell Go SDK reads the files once, when
a client is made, so the worker makes a new client when they change. The
certificate has no URI SAN, which the gateway would refuse. The gateway's own
certificate is from the same CA, so the Secret's `ca.crt` verifies it.

The user role reaches only the gateway workspace `agents`, of which the
operator makes it a member; elsewhere it is refused "not a member of
workspace". Profiles, providers and workspaces stay with the operator: profile
import needs the workspace's admin role. Profiles and providers belong to a
workspace, so `agents` needs its own, imported with `--workspace agents`.
Checked 2026-10-11 from a pod holding the certificate: create, exec, stop,
start, exec, delete in `agents`; refused in `default` and refused `profile
import`.

## Temporal, Postgres and RGW

All three know the worker by its ServiceAccount, `agents-worker`:

- **Temporal**: its X.509-SVID
  (`spiffe://home.garvey.sh/ns/agents/sa/agents-worker`) has `write` in the
  Temporal namespace `agents` (`manifests/temporal/authz-configmap.yaml`).
  That covers its activities' own client calls too (Update-with-Start,
  Signal-with-Start and queries), which need at most `write`.
- **Postgres**: the `agents` role and database, logged in with
  `postgres-client-certificate.yaml` (CN `agents`, 24h), the only login
  `manifests/postgres/cluster.yaml` accepts for the role. The
  `spiffe-agents-postgres` policy allows it only here.
- **RGW**: the role `agents-worker` in `modules/services/rgw-roles.nix`, for
  buckets starting with `agent-sessions`, reached with a JWT-SVID through STS
  ("S3 on Ceph RGW" in `manifests/spire/README.md`). Its ARN is
  `arn:aws:iam::RGW77873637642885287:role/nix-managed/agents-worker`.

## The token service

`agent-token` (`packages/agents`, `cmd/agent-token`) hands out credentials to
SPIFFE identities: an OAuth2 token endpoint for the `client_credentials`
grant, with a JWT-SVID as the client assertion. It is what OpenShell provider
profiles with a `token_grant` call ("Provider token grants" in
`manifests/openshell/README.md`), so a sandbox's model key or data token never
lives in the sandbox; any in-cluster workload can use it with its own pod SVID
too. Nothing about where a request comes from counts: pod IPs are reachable
from the LAN, so the JWT-SVID is the only thing it trusts.

```
POST https://agent-token.agents.svc.cluster.local/token
Content-Type: application/x-www-form-urlencoded

grant_type=client_credentials
client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-spiffe   (or ...:jwt-bearer)
client_assertion=<JWT-SVID>
audience=<what the credential is for>
```

The JWT-SVID must be signed by a key in the trust domain's JWT bundle (from
the SPIRE Workload API), be unexpired, have a SPIFFE ID in `home.garvey.sh`,
and carry exactly one audience,
`spiffe://home.garvey.sh/ns/agents/sa/agent-token`, the service's own ID. A
token for several audiences is refused, so no other service that received it
could replay it here. `scope` is refused. A grant is answered
`{"access_token": ..., "token_type": "Bearer", "expires_in": <seconds>}` with
`Cache-Control: no-store`; anything else is an OAuth error,
`{"error": ..., "error_description": ...}`: `invalid_client` (401) for a bad
assertion, `invalid_target` (400) when no grant matches, `invalid_request`,
`unsupported_grant_type` or `invalid_scope` (400) for a malformed request. The
supervisor logs both fields and answers the agent's request with a 502.

**Grants** are in `agent-token-configmap.yaml`, read at start (restart the
Deployment after a change; it refuses to start on a bad one). Each maps a
SPIFFE ID path and a requested `audience` to one credential:

```yaml
grants:
  - name: pi-litellm
    path: /agent/pi/              # ends in "/": every ID below it; otherwise exactly this ID
    audience: litellm
    static:                       # a secret from a file, e.g. a litellm virtual key
      file: /etc/agent-token/credentials/litellm-pi
      expiresIn: 5m               # the expires_in sent, so how long the supervisor caches it
  - name: habit-data
    path: /agent/habit-helper/
    audience: habit-postgrest
    jwt:                          # an HS256 JWT signed with the key in keyFile, e.g. for PostgREST
      keyFile: /etc/agent-token/credentials/postgrest-jwt-key
      ttl: 5m
      claims: {role: habit_agent} # plus sub (the SPIFFE ID), iat and exp, set by the service
```

`expiresIn` and `ttl` default to 5m and may be 1m to 1h. A static secret must
be a token68 string (what the supervisor accepts); a JWT key at least 32
bytes, as for PostgREST's `jwt-secret`. The service refuses to start if two
grants for one audience could match the same ID, if a grant covers
`/agent/untyped/` (sandboxes created without an agent type, whose creator chose
no identity), or if a file is missing or invalid. Credential files are read
again for every grant, so a rotated key goes out as soon as the Secret volume
updates. Both Secrets with credential files are mounted in that one
directory: `agent-token-credentials` (`secret.yaml`), which holds only two
throwaway test values, `test-static-token` and `test-jwt-key`, that no grant
uses, and `agent-token-litellm` (`litellm-keys-secret.yaml`), the litellm
virtual keys.

The committed grants hand out litellm keys, for audience `litellm`:

| Grant | SPIFFE IDs | Key (alias in litellm) |
|---|---|---|
| `pi-litellm` | `/agent/pi/` | `litellm-pi` (`pi`: `qwen3.8-flash-next`) |
| `pi-wakeup-litellm` | `/agent/pi-wakeup/` | `litellm-pi` |
| `habit-helper-litellm` | `/agent/habit-helper/` | `litellm-habit-helper` (`habit-helper`: `habit-helper`) |

litellm decides what each key may call ("Keys" in
`manifests/litellm/README.md`). They go out with `expiresIn: 1h`, which only
sets how long a supervisor caches one: it keeps a granted token for
`expires_in` less 30 seconds (at most an hour) and asks again after that, or
for the profile's `cache_ttl` instead if it sets one (`0s`: every request).

**HTTPS.** The certificate (`agent-token-certificate.yaml`) is from the
spiffe-ca ClusterIssuer, for the Service's names only; the
`spiffe-agent-token` policy in `manifests/cert-manager/spiffe-policies.yaml`
allows exactly those, and only here. Our supervisor image trusts the spiffe-ca
root (`packages/openshell`), which is how a profile's `https://` token endpoint
verifies. It is a cert-manager certificate rather than the pod's X.509-SVID
because the supervisor's TLS library (rustls-webpki) refuses certificates
with a URI SAN under the root's name constraint ("Certificate users" in
`manifests/openshell/README.md`). The service reloads the files when
cert-manager renews them.

**Network.** No CiliumNetworkPolicy of its own: the Service is ClusterIP, and
`manifests/cilium/pod-ingress-default-deny.yaml` already keeps the LAN out of
the pod while letting the cluster in. A narrower allow-list (only sandbox
supervisors) could not be expressed without taking the pod out of that policy,
since allow rules add up, and would shut out the in-cluster workloads the
service is also for.

A profile using it:

```yaml
credentials:
  - name: api_key
    auth_style: bearer
    header_name: Authorization
    token_grant:
      grant_type: client_credentials
      token_endpoint: https://agent-token.agents.svc.cluster.local/token
      jwt_svid_audience: spiffe://home.garvey.sh/ns/agents/sa/agent-token
      client_assertion_type: urn:ietf:params:oauth:client-assertion-type:jwt-spiffe
      audience: litellm
```

The image is `nix build .#agent-token-image`, tagged like the worker's (the
store hash of `packages/agents`):

```sh
nix build .#agent-token-image
skopeo copy docker-archive:result docker://oci.garvey.sh/agent-token:$(nix eval --raw .#agent-token-image.imageTag)
```

## The Matrix bridge

`agents-matrix` (`matrix-*.yaml`; what it does is in `agents/README.md`,
"Matrix") runs the agents-worker image with its own ServiceAccount,
`agents-matrix`:

- **Temporal**: its X.509-SVID
  (`spiffe://home.garvey.sh/ns/agents/sa/agents-matrix`, kept fresh by
  spiffe-helper) has `write` in the namespace `agents`, for Update-with-Start
  on the `ChatSession`s, cancelling `Wakeup`s, and polling the
  `agents-matrix` task queue, where it posts wake-up replies.
- **Synapse**: the appservice registration (`matrix-secret.yaml`) holds the
  `as_token` the bridge acts as the agents' users with, and the `hs_token`
  Synapse sends with each transaction to
  `http://agents-matrix.agents.svc.cluster.local:8009` (`matrix-service.yaml`).
  That hop is plaintext inside the cluster; the bridge refuses a transaction
  without the `hs_token`. Synapse has the same registration as
  `appservice-agents.yaml` in `manifests/matrix/secret.yaml`.

Its state is in Matrix and Temporal only, so it can be restarted at any time;
a turn running at the time still finishes, but its reply is not posted. A
wake-up reply waits in Temporal for the bridge, up to an hour.

To change the registration (a new agent type's user, new tokens), change both
copies the same way, decrypting only into a pipe:

```sh
sops -d manifests/agents/matrix-secret.yaml \
  | yq '.spec.secretTemplates[0].stringData["registration.yaml"]'   # check the current namespace
# Edit with `sops manifests/agents/matrix-secret.yaml`, then copy it into Synapse's:
REG=$(sops -d manifests/agents/matrix-secret.yaml | yq '.spec.secretTemplates[0].stringData["registration.yaml"]')
sops -d manifests/matrix/secret.yaml \
  | REG="$REG" yq '.spec.secretTemplates[0].stringData["appservice-agents.yaml"] = strenv(REG)' \
  | sops -e --filename-override manifests/matrix/secret.yaml --input-type yaml --output-type yaml /dev/stdin \
  > manifests/matrix/secret.yaml.new && mv manifests/matrix/secret.yaml.new manifests/matrix/secret.yaml
unset REG
kubectl apply -f manifests/agents/matrix-secret.yaml -f manifests/matrix/secret.yaml
kubectl -n matrix rollout restart deploy/synapse
kubectl -n agents rollout restart deploy/agents-matrix
```

(`sops --set` on a key of `manifests/matrix/secret.yaml` mangles the comment
above the list item, so the pipe it is.)

## Apply (from `k8s/`)

`manifests/cert-manager/spiffe-policies.yaml` first, and the gateway with
`mtls_auth` (`manifests/openshell/README.md`).

```sh
kubectl apply -f manifests/agents/namespace.yaml
kubectl apply -f manifests/agents/service-account.yaml
kubectl apply -f manifests/agents/openshell-certificate.yaml
kubectl -n agents wait --for=condition=Ready certificate/agents-worker-openshell
openshell workspace create --name agents
openshell workspace member add --workspace agents --subject agents-worker --role user
```

The platform, with `manifests/cert-manager/spiffe-policies.yaml` applied:

```sh
kubectl apply -f manifests/agents/postgres-client-certificate.yaml
kubectl apply -f manifests/postgres/cluster.yaml -f manifests/postgres/databases.yaml
kubectl -n postgres exec postgres-1 -c postgres -- psql -U postgres -c \
  'REVOKE CONNECT ON DATABASE agents FROM PUBLIC; GRANT CONNECT ON DATABASE agents TO agents;'
kubectl apply -f manifests/temporal/authz-configmap.yaml
kubectl -n temporal rollout restart deploy/temporal
kubectl -n temporal port-forward deploy/temporal 7236:7236 &
temporal operator namespace create --namespace agents --retention 72h --address 127.0.0.1:7236
```

The worker, after `nix run .#agents-apply` has written its ConfigMap and the
image is pushed:

```sh
kubectl apply -f manifests/agents/spiffe-helper-configmap.yaml
kubectl apply -f manifests/agents/deployment.yaml
```

The token service, with `manifests/cert-manager/spiffe-policies.yaml` applied
and its image pushed. Sandboxes reach it only once their supervisor runs our
image with the spiffe-ca root ("Upgrading" in `manifests/openshell/README.md`
for pushing and pinning it):

```sh
kubectl apply -f manifests/agents/secret.yaml -f manifests/agents/litellm-keys-secret.yaml
kubectl -n agents get secret agent-token-credentials agent-token-litellm   # wait for both
kubectl apply -f manifests/agents/agent-token-service-account.yaml
kubectl apply -f manifests/agents/agent-token-certificate.yaml
kubectl -n agents wait --for=condition=Ready certificate/agent-token-tls
kubectl apply -f manifests/agents/agent-token-configmap.yaml
kubectl apply -f manifests/agents/agent-token-deployment.yaml
kubectl apply -f manifests/agents/agent-token-service.yaml
kubectl -n agents rollout status deploy/agent-token
```

The RGW role takes effect when joor is deployed. `postgres-1` is whichever
instance is primary (`kubectl -n postgres get cluster postgres`).

The Matrix bridge, with Synapse and MAS running (`manifests/matrix`) and the
agents-worker image that has `agents-matrix` pushed:

```sh
kubectl apply -f manifests/temporal/authz-configmap.yaml
kubectl -n temporal rollout restart deploy/temporal
kubectl apply -f manifests/matrix/secret.yaml -f manifests/matrix/configmap.yaml
kubectl -n matrix rollout restart deploy/synapse
kubectl apply -f manifests/agents/matrix-secret.yaml
kubectl -n agents get secret agents-matrix        # wait for it
kubectl apply -f manifests/agents/matrix-service-account.yaml \
  -f manifests/agents/matrix-spiffe-helper-configmap.yaml \
  -f manifests/agents/matrix-service.yaml -f manifests/agents/matrix-deployment.yaml
kubectl -n agents rollout status deploy/agents-matrix
```
