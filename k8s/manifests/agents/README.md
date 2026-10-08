# agents

The agents worker's namespace (design: `docs/agent-sessions-design.md`). The
worker runs agent sessions in OpenShell sandboxes; the agents themselves are in
the repo root's `agents/`.

## The worker

`deployment.yaml` runs `agents-worker` (`packages/agents`): the `ChatSession`,
`SessionCoordinator` and `SandboxReaper` workflows on task queue `agents` in
Temporal namespace `agents`. Each conversation is a `ChatSession` with
workflow ID `chat/<type>/<conversation>`; a message is its Update `message`,
sent with Update-with-Start, and the Update's result is the reply. Sessions
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
- **Postgres**: the `agents` role and database, logged in with
  `postgres-client-certificate.yaml` (CN `agents`, 24h), the only login
  `manifests/postgres/cluster.yaml` accepts for the role. The
  `spiffe-agents-postgres` policy allows it only here.
- **RGW**: the role `agents-worker` in `modules/services/rgw-roles.nix`, for
  buckets starting with `agent-sessions`, reached with a JWT-SVID through STS
  ("S3 on Ceph RGW" in `manifests/spire/README.md`). Its ARN is
  `arn:aws:iam::RGW77873637642885287:role/nix-managed/agents-worker`.

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

The RGW role takes effect when joor is deployed. `postgres-1` is whichever
instance is primary (`kubectl -n postgres get cluster postgres`).
