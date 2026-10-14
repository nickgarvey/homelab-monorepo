# habit-helper

Habit Helper: an agent for tracking habits and supporting the user through
them (epic nix-psvkl). This directory holds what defines it:

| File | What it is |
|---|---|
| `image.nix` | The sandbox image: pi, the `habit` CLI, a shell, and pi's config (`models.json`, `settings.json`) and instructions in `/sandbox/.pi/agent`. `nix build .#habit-helper-image`. |
| `instructions.md` | The agent's instructions (its `AGENTS.md`): onboarding, the end-of-day check-in, coaching and recovery, going quiet, and scheduling its wake-ups. |
| `policy.yaml` | Sandbox policy: like pi's; `/sandbox`, which holds the wake-up outbox, is writable. |
| `habit-data-profile.yaml` | The `habit-data` provider profile: PostgREST, reachable only by the `habit` CLI ("habit-data" in `agents/README.md`). |
| `schema/` | The database schema, applied by PostgREST's init container (`k8s/manifests/habit-helper/README.md`). |
| `postgrest-image.nix` | PostgREST's image. |

## The agent type

`habit-helper` in `agents/declaration.nix`: pi on litellm's `habit-helper`
model (DeepSeek V4 Flash for now, `k8s/manifests/litellm/configmap.yaml`;
nix-psvkl.11 moves it to Claude), with the `litellm` and `habit-data`
providers. Its supervisor gets litellm's `habit-helper` key and the PostgREST
token from agent-token; pi's node may reach only litellm, and the `habit` CLI
only PostgREST. Wake-ups are on (`session.defaultWakeup` 24h, times in
`America/Los_Angeles`): the agent asks for its check-ins with `habit
wake-at`, and while the user is silent the default backs off to weekly
("Wake-ups" in `agents/README.md`). Talk to it on Matrix, as
`@habit-helper:matrix.garvey.sh` ("Matrix" in `agents/README.md`); its
wake-up replies are posted there.

## The `habit` CLI

The agent's only way to its data. Source `packages/agents/cmd/habit` (Go, in
the agents module), built static by `packages/agents/habit-cli.nix` as the
flake output `habit-cli` (`nix build .#habit-cli`), installed in the
agent's image. Its build runs the tests, including an integration test that
runs every command against PostgREST serving `schema/` on a throwaway
Postgres, behind a stand-in for the sandbox proxy.

`habit --help` lists the commands and `habit <command> --help` explains one.
Output is short plain text, or JSON with `--json`. A mistake on the command
line exits 2 with what is wrong and the usage; a refused or failed request
exits 1 with a message saying what to do. Nothing deletes: habits are retired,
and a day is corrected by logging it again. Archiving and deleting for good
are the admin chat's (`@habit-admin`, "Matrix" in `agents/README.md`); the
CLI never sees an archived habit.

| Command | What it does |
|---|---|
| `add <name> --cue "if <event>, then <behavior>" --motivation M --prep P` | Adds an active habit, due for a check-in today. The cue must read as an if/then plan. |
| `edit <habit> [--name] [--cue] [--motivation] [--prep]` | Changes it; a rename carries over to every table. |
| `pause`, `resume`, `retire <habit>` | Status. Paused and retired habits leave `pending`, `summary` and `cadence`. |
| `list [--all]`, `show <habit> [--days N]` | Habits; one habit in full, with its notes. |
| `log <habit> [<date>] done\|missed ["<why>"]` | Records a day (default today); logging it again replaces it. Then applies the tapering rule. |
| `pending [<date>]` | Active habits with no entry for the day. |
| `history <habit> [--days N]` | Day by day, with reasons. |
| `summary [<habit>]` | Streak, and done/missed/not-logged over 7 and 30 days. |
| `note <habit> motivation\|cue\|prep "<text>"` | Keeps what the user said, over time. `add` and `edit` also keep each new value as a note. |
| `cadence [<habit>]` | Next check-ins, the earliest due, and a suggested wake-up. |
| `cadence <habit> --every N --reason "..." [--next <date>]`, `cadence <habit> --auto` | Override the rule, or hand the habit back to it. |
| `wake-at <when> "<note>"` | Writes `/sandbox/outbox/wakeup.json` for the worker. |

Habits are named as they were added; case does not matter. Dates are the
user's local dates (`today`, `yesterday`, a weekday meaning the latest one,
or `YYYY-MM-DD`) in `America/Los_Angeles`, or `--tz` / `$HABIT_TZ`. A day
after today cannot be logged.

Environment: `HABIT_API_URL` (default
`http://postgrest.habit-helper.svc.cluster.local:3000`), `HABIT_TZ`,
`HABIT_OUTBOX` (default `/sandbox/outbox/wakeup.json`).

### Reaching PostgREST

The sandbox's network goes through the OpenShell supervisor, but not as an
HTTP proxy: the supervisor captures the sandbox's DNS and TCP egress
transparently, and removes `HTTP_PROXY` and the like from sandbox processes
(OpenShell `crates/openshell-sandbox/src/process.rs`, `strip_proxy_env`). So
the CLI dials PostgREST's URL directly, with Go's proxy lookup turned off so
a stray proxy variable cannot divert it. The supervisor allows the
connection only from the binary the profile names, the CLI's store path, and
only for `GET`, `POST` and `PATCH`. It puts the PostgREST token in the
`Authorization` header, replacing any the request carries
(`l7/token_grant_injection.rs`, `inject_header`), so the CLI sends none.

A 401 means no token reached PostgREST, and a 502 that the supervisor could
not get one: the sandbox lacks the `habit-data` provider or the
`habit-helper` type. The CLI says so.

### Tapering: the cadence rule

Each habit has a check-in interval in `cadence`. `habit log` sets it by
replaying all of the habit's entries in day order, so logging a day again or
filling in an old one gives the same result as logging in order would have.
Nothing in the database applies the rule (the schema only adds a row, due
today, when a habit is added).

- Intervals: 1, 2, 4, 7, 14, 30, 60 days. A habit starts at 1.
- **done**: the run of done days grows by one. When it reaches the current
  interval (at least 3), the interval moves up a step and the run starts
  again.
- **missed**: the first miss in a row moves the interval down a step (a single
  slip is fine); a second in a row resets it to 1 day. Either way the run
  starts again.
- Days with no entry are skipped: they neither break nor extend a run.
- Next check = the latest logged day + the interval, never before tomorrow.

Done every day, a habit goes 1 → 2 after 3 days, → 4 after 3 more, → 7 after
4, → 14 after 7, → 30 after 14, → 60 after 30. `streak` and `misses` in the
row are the done and missed entries in a row up to the latest logged day.

An override (`cadence <habit> --every N --reason "..."`, or `--next <date>`
with a reason) pins the interval: later logs still move the next check and the
streak, but not the interval, until `cadence <habit> --auto`. `reason` is set
exactly while an override holds.

`habit cadence` names the earliest due check and suggests a wake-up for it:
that day at the check-in time (`--at`, default 20:00), or the next check-in
time at least 10 minutes away if that has passed. `habit wake-at due "<note>"`
requests it; the agent may pick another time.

### Wake-ups

`habit wake-at <when> "<note>"` writes `{"at": "<RFC 3339>", "note": "..."}`
to `/sandbox/outbox/wakeup.json`, atomically, replacing an earlier request of
the same turn. It refuses what the worker would refuse (10 minutes to 60 days
ahead, note at most 500 characters; a test checks it against the worker's
parser in `packages/agents/internal/session/wakeup.go`), and an empty note.
`<when>` is `due`, `+90m` / `+3h` / `+2d` / `+1d6h`, `HH:MM` (the next time
the clock shows it), `tomorrow` or `YYYY-MM-DD` (at the check-in time),
`"YYYY-MM-DD HH:MM"` in the user's zone, or RFC 3339.

### Trying it in a sandbox

`habit` runs in any image (it is static). To try it without the agent's
image, upload it into a throwaway sandbox that has a copy of the profile whose
`binaries` entry is the uploaded path, and label the supervisor pod with the
type, so agent-token grants it a token (as in "Running pi by hand" in
`agents/README.md`). From the repo root, in `nix develop`:

```sh
B=$(nix build --no-link --print-out-paths .#habit-cli)/bin/habit
P=$(nix build --no-link --print-out-paths .#habit-data-profile)
sed -e 's|^id: habit-data$|id: habit-data-test|' -e 's|/nix/store/[^ ]*/bin/habit|/sandbox/habit|' $P > /tmp/habit-data-test.yaml
openshell profile import --workspace default -f /tmp/habit-data-test.yaml
openshell provider create --workspace default --name habit-data-test --type habit-data-test --runtime-credentials
openshell sandbox create --workspace default --name habit-cli-test --from curlimages/curl:8.11.1 \
  --provider habit-data-test --detach --no-auto-providers -- sleep 3600
openshell sandbox upload --workspace default habit-cli-test $B /sandbox      # lands as /sandbox/habit
ID=$(kubectl -n openshell get pod default--habit-cli-test -o jsonpath='{.metadata.annotations.openshell\.ai/sandbox-id}')
kubectl -n openshell label pod os-supervisor-$ID agents.garvey.sh/type=habit-helper
H() { openshell sandbox exec --workspace default -n habit-cli-test --no-tty -- /sandbox/habit "$@"; }
H list

# Afterwards: the test's rows (nothing in the CLI deletes), then the sandbox.
kubectl -n postgres exec postgres-1 -c postgres -- psql -U postgres -d habit_helper -c \
  "delete from habit.entries where habit like 'cli-test%'; delete from habit.notes where habit like 'cli-test%';
   delete from habit.cadence where habit like 'cli-test%'; delete from habit.habits where name like 'cli-test%';"
openshell sandbox delete --workspace default habit-cli-test
openshell provider delete --workspace default habit-data-test
openshell profile delete --workspace default habit-data-test
```
