# habit-admin

Habit Admin (nix-LAjpH.6): an agent you ask, in plain words, to look at or
change Habit Helper's habits and wake-ups, without going through the coach.
Talk to it on Matrix as `@habit-admin:matrix.garvey.sh` ("Matrix" in
`agents/README.md`).

| File | What it is |
|---|---|
| `image.nix` | The sandbox image: pi, the `habit-admin` CLI, a shell, and pi's config (`models.json`, `settings.json`) and instructions in `/sandbox/.pi/agent`. `nix build .#habit-admin-image`. |
| `instructions.md` | The agent's instructions (its `AGENTS.md`): do what is asked, no coaching, and a permanent delete only on the user's explicit yes. |
| `policy.yaml` | Sandbox policy, like pi's. |
| `admin-api-profile.yaml` | The `habit-admin-api` provider profile: the Matrix bridge's admin API, reachable only by the `habit-admin` CLI. |

## The agent type

`habit-admin` in `agents/declaration.nix`: pi on litellm's `habit-admin`
model (DeepSeek V4 Flash, `k8s/manifests/litellm/configmap.yaml`), with the
`litellm` and `habit-admin-api` providers and no wake-ups. Its supervisor
gets its own litellm key and admin API tokens from agent-token.

## The `habit-admin` CLI

Source `packages/agents/cmd/habit-admin`, built static by
`packages/agents/habit-admin-cli.nix` (`nix build .#habit-admin-cli`).
`habit-admin <command> [args]` joins its arguments with spaces, POSTs them to
the bridge's admin API (`HABIT_ADMIN_API_URL`, default
`http://agents-matrix.agents.svc.cluster.local:8010`) and prints the reply.
The commands are those of `packages/agents/internal/habitadmin`;
`habit-admin help` lists them.

The bridge runs each command for its one allowed user: Habit Helper's data
through PostgREST as role `habit_admin` (which sees archived habits and may
delete), and Habit Helper's wake-up in its direct chat with that user,
through Temporal. A permanent delete waits up to 5 minutes for a `yes`
command, and any other command cancels it; that the yes is the user's rests
on the agent's instructions.
