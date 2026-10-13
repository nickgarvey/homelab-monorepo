# litellm

An OpenAI-compatible proxy in front of the models the cluster uses
(`configmap.yaml`):

| Model | Behind it |
|---|---|
| `qwen3.8-flash-next` | llama.cpp on wabbajack, currently off (its memory goes to Nix builds; `hosts/wabbajack/configuration.nix`) |
| `pi` | DeepSeek V4 Flash (`deepseek/deepseek-v4-flash`), with the key in `litellm-deepseek`; the pi agent's default |
| `habit-helper` | DeepSeek V4 Flash (`deepseek/deepseek-v4-flash`), with the key in `litellm-deepseek` |

In-cluster clients use `http://litellm.litellm.svc.cluster.local:4000`. The
LoadBalancer (`service.yaml`, `2001:470:482f:2::4000`, open to all by
`network-policy.yaml`) is for callers outside the cluster. Both are plain
HTTP.

## Keys

Every call needs a key in `Authorization: Bearer`; without one litellm
answers 401. There are three kinds:

- **The master key** (`master-key` in the `litellm` Secret, `secret.yaml`):
  admin, for managing keys. Held by litellm and the user only.
- **Virtual keys for SPIFFE identities**, one per agent type, each limited to
  its models. Sandboxes never see them: agent-token hands them to sandbox
  supervisors that prove their identity with a JWT-SVID, and the supervisor
  injects the key ("The token service" in `manifests/agents/README.md`).

  | Alias | Models | agent-token grants |
  |---|---|---|
  | `pi` | `pi`, `qwen3.8-flash-next` | `/agent/pi/` |
  | `habit-helper` | `habit-helper` | `/agent/habit-helper/` |
  | `habit-admin` | `habit-admin` | `/agent/habit-admin/` |

  The values are in two places that must match: `litellm-virtual-keys` here
  and `agent-token-litellm` in namespace agents
  (`manifests/agents/litellm-keys-secret.yaml`), and for `habit-admin`,
  `habit-admin-key-secret.yaml` here and `manifests/agents/habit-admin-secret.yaml`.
  `register-keys-job.yaml` registers them with litellm, with their alias and
  models, and is safe to re-run.
- **Named static keys** for callers outside the cluster (a workstation, a
  script): one per holder, so each can be revoked alone. None exist yet.

Virtual keys are stored in the `litellm` database on the shared postgres
(`manifests/postgres/`), as hashes. litellm logs in as role `litellm` with a
password (`postgres-password` in the `litellm` Secret, the same as
`postgres-litellm` in `manifests/postgres/litellm-secret.yaml`) over TLS,
checking the server's certificate against the SPIFFE CA (`postgres-ca.yaml`).
Not a client certificate, as other roles use: litellm's Prisma engine reads
a client certificate (PKCS#12 only) once and keeps it, so a short-lived one
would stop working when it expired.

No key has a budget or limits; what a key may call is its whole restriction.

### Minting a named static key

With the master key (from your own cluster access) and the models the holder
needs:

```sh
MASTER=$(kubectl -n litellm get secret litellm -o jsonpath='{.data.master-key}' | base64 -d)
curl -sS http://litellm.litellm.k8s.home.garvey.sh:4000/key/generate \
  -H "Authorization: Bearer $MASTER" -H 'Content-Type: application/json' \
  -d '{"key_alias": "<holder>", "models": ["qwen3.8-flash-next"]}'
```

The answer's `key` is shown only this once; give it to the holder. Revoke it
with `/key/delete`, `{"key_aliases": ["<holder>"]}`. Aliases are unique, and
`pi` and `habit-helper` are taken. `/key/list` shows all keys.

### Rotating a virtual key

Put the new value in both Secrets (`sops secret.yaml`,
`sops ../agents/litellm-keys-secret.yaml`), apply both, delete the old key
(`/key/delete` with `{"key_aliases": ["<alias>"]}`) and re-run the Job, which
then registers the new value under the alias. Supervisors pick up the new key
when their cached one expires (agent-token's `expiresIn`, 1h).

## DeepSeek

`DEEPSEEK_API_KEY` in the `litellm-deepseek` Secret (`deepseek-secret.yaml`)
holds the placeholder `REPLACE_WITH_DEEPSEEK_API_KEY` until the real key is put
in. litellm starts either way (the variable is optional in `deployment.yaml`);
only calls to `habit-helper` fail without it. The key is the one pi uses on the
workstations, `deepseek-api-key` in `secrets/llm-api-keys.yaml`, which the
cluster cannot decrypt, so it is copied across. From the repo root:

```sh
sops set k8s/manifests/litellm/deepseek-secret.yaml \
  '["spec"]["secretTemplates"][0]["stringData"]["DEEPSEEK_API_KEY"]' \
  "$(sops -d --extract '["deepseek-api-key"]' secrets/llm-api-keys.yaml | jq -R .)"
kubectl apply -f k8s/manifests/litellm/deepseek-secret.yaml
kubectl -n litellm rollout restart deploy/litellm
```

The `litellm-anthropic` Secret in `secret.yaml` holds only a placeholder and
nothing reads it.

## Apply (from `k8s/`)

The database first, with `manifests/postgres/litellm-secret.yaml`, the
`litellm` role and pg_hba lines in `cluster.yaml`, and the `litellm`
`Database` in `databases.yaml`, then its grants ("Adding an app" in
`manifests/postgres/README.md`):

```sh
kubectl apply -f manifests/postgres/litellm-secret.yaml
kubectl apply -f manifests/postgres/cluster.yaml -f manifests/postgres/databases.yaml
kubectl -n postgres exec postgres-1 -c postgres -- psql -U postgres -c \
  'REVOKE CONNECT ON DATABASE litellm FROM PUBLIC; GRANT CONNECT ON DATABASE litellm TO litellm;'
```

Then litellm and its keys. The Job waits for litellm to be ready:

```sh
kubectl apply -f manifests/litellm/namespace.yaml
kubectl apply -f manifests/litellm/secret.yaml
kubectl apply -f manifests/litellm/deepseek-secret.yaml
kubectl -n litellm get secret litellm litellm-virtual-keys litellm-deepseek   # wait for all three
kubectl apply -f manifests/litellm/postgres-ca.yaml -f manifests/litellm/configmap.yaml
kubectl apply -f manifests/litellm/deployment.yaml -f manifests/litellm/register-keys-job.yaml
kubectl apply -f manifests/litellm/service.yaml -f manifests/litellm/network-policy.yaml
kubectl -n litellm wait --for=condition=Complete job/litellm-register-keys --timeout=10m
kubectl -n litellm logs job/litellm-register-keys
```

litellm creates its tables at start. The image is the floating
`main-stable` tag, pulled once per node (`IfNotPresent`).
