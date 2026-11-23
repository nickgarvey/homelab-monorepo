# litellm

An OpenAI-compatible proxy in front of the models the cluster uses
(`configmap.yaml`):

| Model | Behind it |
|---|---|
| `qwen3.8-flash-next` | llama.cpp on wabbajack, currently off (its memory goes to Nix builds; `hosts/wabbajack/configuration.nix`) |
| `pi` | DeepSeek V4 Flash (`deepseek/deepseek-v4-flash`), with the key in `DEEPSEEK_API_KEY`; the pi agent's default |
| `habit-helper` | GPT-6.1 Sol through the Responses API (`openai/responses/gpt-6.1-sol`), with the key in `OPENAI_API_KEY` |
| `habit-admin` | DeepSeek V4 Flash (`deepseek/deepseek-v4-flash`), with the key in `DEEPSEEK_API_KEY` |

Secrets come from OpenBao, mounted by `secret-provider-class.yaml`
(docs/openbao.md, "Pod secrets"), one file per ServiceAccount:

| File | ServiceAccount | What |
|---|---|---|
| `secrets/openbao/litellm/litellm.yaml` | `litellm` (deployment.yaml) | master key, database URL, provider keys |
| `secrets/openbao/litellm/litellm-register-keys.yaml` | `litellm-register-keys` (register-keys-job.yaml) | master key, the virtual keys it registers |

In-cluster clients use `http://litellm.litellm.svc.cluster.local:4000`. The
LoadBalancer (`service.yaml`, `2001:470:482f:2::4000`, open to all by
`network-policy.yaml`) is for callers outside the cluster. Both are plain
HTTP.

## Keys

Every call needs a key in `Authorization: Bearer`; without one litellm
answers 401. There are three kinds:

- **The master key**: admin, for managing keys. Held by litellm and the user
  only. It is in two places that must match: `LITELLM_MASTER_KEY` in
  `secrets/openbao/litellm/litellm.yaml` (litellm) and `master-key` in
  `secrets/openbao/litellm/litellm-register-keys.yaml` (register-keys-job.yaml).
- **Virtual keys for SPIFFE identities**, one per agent type, each limited to
  its models. Sandboxes never see them: agent-token hands them to sandbox
  supervisors that prove their identity with a JWT-SVID, and the supervisor
  injects the key ("The token service" in `manifests/agents/README.md`).

  | Alias | Models | agent-token grants |
  |---|---|---|
  | `pi` | `pi`, `qwen3.8-flash-next` | `/agent/pi/` |
  | `habit-helper` | `habit-helper` | `/agent/habit-helper/` |
  | `habit-admin` | `habit-admin` | `/agent/habit-admin/` |

  The values are in two places that must match: `virtual-keys` and
  `habit-admin` in `secrets/openbao/litellm/litellm-register-keys.yaml`
  (register-keys-job.yaml), and `litellm` and `litellm-habit-admin` in
  `secrets/openbao/agents/agent-token.yaml` (agent-token).
  `register-keys-job.yaml` registers them with litellm, with their alias and
  models, and is safe to re-run.
- **Named static keys** for callers outside the cluster (a workstation, a
  script): one per holder, so each can be revoked alone. None exist yet.

Virtual keys are stored in the `litellm` database on the shared postgres
(`manifests/postgres/`), as hashes. litellm logs in as role `litellm` with a
password (inside `DATABASE_URL` in `secrets/openbao/litellm/litellm.yaml`, the same as
`postgres-litellm` in `secrets/k8s/postgres/postgres-litellm.yaml`) over TLS,
checking the server's certificate against the cluster CA (`postgres-ca.yaml`).
Not a client certificate, as other roles use: litellm's Prisma engine reads
a client certificate (PKCS#12 only) once and keeps it, so a short-lived one
would stop working when it expired.

No key has a budget or limits; what a key may call is its whole restriction.

### Minting a named static key

With the master key (from the sops file) and the models the holder
needs:

```sh
MASTER=$(sops -d --extract '["secrets"]["litellm"]["LITELLM_MASTER_KEY"]' secrets/openbao/litellm/litellm.yaml)
curl -sS http://litellm.litellm.k8s.home.garvey.sh:4000/key/generate \
  -H "Authorization: Bearer $MASTER" -H 'Content-Type: application/json' \
  -d '{"key_alias": "<holder>", "models": ["qwen3.8-flash-next"]}'
```

The answer's `key` is shown only this once; give it to the holder. Revoke it
with `/key/delete`, `{"key_aliases": ["<holder>"]}`. Aliases are unique, and
`pi` and `habit-helper` are taken. `/key/list` shows all keys.

### Rotating a virtual key

Put the new value in both places (`sops secrets/openbao/litellm/litellm-register-keys.yaml`,
`sops secrets/openbao/agents/agent-token.yaml`), push both
(`bash scripts/push-secrets.sh secrets/openbao/litellm/litellm-register-keys.yaml secrets/openbao/agents/agent-token.yaml`)
and `kubectl -n agents rollout restart deploy/agent-token`, delete the old key
(`/key/delete` with `{"key_aliases": ["<alias>"]}`) and re-run the Job, which
then registers the new value under the alias. Supervisors pick up the new key
when their cached one expires (agent-token's `expiresIn`, 1h).

## DeepSeek

`DEEPSEEK_API_KEY` (`secrets.deepseek` in `secrets/openbao/litellm/litellm.yaml`) is the key
for the `pi` and `habit-admin` models. It is the one pi uses on the
workstations, `deepseek-api-key` in `secrets/llm-api-keys.yaml`, copied
across. From the repo root:

```sh
sops set k8s/secrets/openbao/litellm/litellm.yaml \
  '["secrets"]["deepseek"]["DEEPSEEK_API_KEY"]' \
  "$(sops -d --extract '["deepseek-api-key"]' secrets/llm-api-keys.yaml | jq -R .)"
(cd k8s && bash scripts/push-secrets.sh secrets/openbao/litellm/litellm.yaml)
kubectl -n litellm rollout restart deploy/litellm
```

## OpenAI

`OPENAI_API_KEY` (`secrets.openai` in `secrets/openbao/litellm/litellm.yaml`) is litellm's own
key: an OpenAI service account key, restricted to model capabilities, in a
project of its own. It is not the workstations' `openai-api-key`. To replace
it, write the new key to a file and, from the repo root:

```sh
printf '"%s"' "$(tr -d '\n' < key-file)" | sops set --value-stdin \
  k8s/secrets/openbao/litellm/litellm.yaml \
  '["secrets"]["openai"]["OPENAI_API_KEY"]'
(cd k8s && bash scripts/push-secrets.sh secrets/openbao/litellm/litellm.yaml)
kubectl -n litellm rollout restart deploy/litellm
```

## Apply (from `k8s/`)

The database first, with `secrets/k8s/postgres/postgres-litellm.yaml`, the
`litellm` role and pg_hba lines in `cluster.yaml`, and the `litellm`
`Database` in `databases.yaml`, then its grants ("Adding an app" in
`manifests/postgres/README.md`):

```sh
bash scripts/push-secrets.sh secrets/k8s/postgres/postgres-litellm.yaml
kubectl apply -f manifests/postgres/cluster.yaml -f manifests/postgres/databases.yaml
kubectl -n postgres exec postgres-1 -c postgres -- psql -U postgres -c \
  'REVOKE CONNECT ON DATABASE litellm FROM PUBLIC; GRANT CONNECT ON DATABASE litellm TO litellm;'
```

Then litellm and its keys. litellm's own secrets must be in OpenBao before it
starts, and the Job's before it runs (the pods fail to mount otherwise), and
their roles must exist (`pki/openbao/tofu.sh apply`). The Job waits for
litellm to be ready:

```sh
bash scripts/push-secrets.sh secrets/openbao/litellm/litellm.yaml secrets/openbao/litellm/litellm-register-keys.yaml
kubectl apply -f manifests/litellm/namespace.yaml
kubectl apply -f manifests/litellm/secret-provider-class.yaml
kubectl apply -f manifests/litellm/postgres-ca.yaml -f manifests/litellm/configmap.yaml
kubectl apply -f manifests/litellm/deployment.yaml -f manifests/litellm/register-keys-job.yaml
kubectl apply -f manifests/litellm/service.yaml -f manifests/litellm/network-policy.yaml
kubectl -n litellm wait --for=condition=Complete job/litellm-register-keys --timeout=10m
kubectl -n litellm logs job/litellm-register-keys
```

litellm creates its tables at start. The image is the floating
`main-stable` tag, pulled once per node (`IfNotPresent`).
