# agents

Agents that run in OpenShell sandboxes on the k3s cluster. The gateway itself,
and how to log the CLI in to it, is `k8s/manifests/openshell/`. The `openshell`
CLI is in the default dev shell (`nix develop`).

Three pieces:

- **Agent types**, declared in Nix here: image, sandbox policy, provider
  profiles and providers, and session settings.
- **`agents-apply`**, run by hand, publishes them to the gateway.
- **The worker** (`agents-worker`, `k8s/manifests/agents/`) runs chat sessions
  with them: one sandbox per conversation, driven from Temporal.

## Agent types

`declaration.nix` declares the types (options in `schema.nix`), the gateway
workspace they live in (`agents`) and the profiles and providers they use.
Each type has a directory here with everything that defines it:

| File | What it is |
|---|---|
| `image.nix` | The sandbox image. `outputs.nix` exports it as `<agent>-image`. |
| `policy.yaml` | Sandbox policy (filesystem, Landlock, process), passed at sandbox creation; it is not in the image. The worker cannot pass `network_policies` yet. |
| `*-profile.yaml` | OpenShell provider profiles: the endpoints the agent may reach, and which binaries may reach them. |
| `instructions.md` | The agent's own instructions, installed as the agent's `AGENTS.md`. Named so tools working on this repo don't read it as repo conventions. |

`nix build .#agents-desired-state` builds the declaration into the
desired-state JSON (format in `desired-state.nix`), with each type's
`version`: a hash of everything about the type, session settings included.
`nix flake check` runs `declaration-test.nix`, which checks that bad
declarations fail to evaluate.

A sandbox has no network access of its own. In OpenShell 0.1 the policy grants
none, and each provider attached to the sandbox adds the endpoints in its
profile. The sandbox supervisor's proxy makes the upstream connection, and
only for the binaries the profile names. This needs our OpenShell build with
NVIDIA/OpenShell#3702 (`packages/openshell`): stock OpenShell cannot resolve
names in an IPv6-only cluster. Give endpoints as hostnames, not IPv6 literals.

## Publishing: `agents-apply`

```sh
nix run .#agents-apply -- --dry-run    # print what would change
nix run .#agents-apply
```

It runs the `openshell`, `skopeo` and `kubectl` CLIs with your own gateway
login (the admin role, which the worker doesn't have) and cluster access, in
order:

1. Checks the gateway login is valid (otherwise: `openshell gateway login k8s`).
2. Pushes each type's image to `oci.garvey.sh/<name>:<store hash>` unless the
   registry has it. A changed image always gets a new tag.
3. Creates the workspace and makes `agents-worker` a member.
4. Imports missing profiles and updates changed ones in place.
5. Creates missing providers.
6. Writes the desired state to the `agents-desired-state` ConfigMap in
   namespace `agents`, last, so the worker never sees a type whose image or
   profile isn't on the gateway yet.

It deletes nothing; profiles and providers it doesn't declare are reported. A
second run with nothing changed prints "Nothing to do."

The worker reads the ConfigMap afresh for every turn, so a publish needs no
restart, but the pod's copy lags the write by up to a minute or so. A type
whose `version` changed reaches a conversation at its next message: its
sandbox is deleted and recreated, and pi starts a new session primed from the
chat log. Changing a session setting changes the version too.

## Sessions

Each conversation is a Temporal workflow, `ChatSession`, with ID
`chat/<type>/<conversation>`, in Temporal namespace `agents`. A conversation ID
is letters, digits, `.`, `_` and `-`. A message is the workflow's Update
`message`, sent with Update-with-Start; its result is the reply. How messages
get there (a chat transport, a scheduled job) is the caller's business; by hand
it's the command in `k8s/manifests/agents/README.md`.

- **Per message.** The worker makes sure the conversation's sandbox is running,
  then runs `pi -p --session-id <conversation>` in it and returns the reply.
  Messages to one conversation run one at a time, in order.
- **Chat log.** Every message and reply goes to the `messages` table in the
  `agents` database. After each turn, pi's session file is copied to RGW,
  `agent-sessions/<type>/<conversation>/<sandbox>.jsonl`.
- **Idle.** After `session.idleTimeout` (default 6h) with no message, the
  sandbox is stopped, which keeps its volume, and the workflow completes. The
  next message starts the same sandbox and pi carries on from its own session
  file.
- **Fresh sandboxes.** A conversation gets a new sandbox when it has none, when
  its type's version changed, or when its sandbox was lost or deleted. The
  first turn in it is primed with the last `session.primeMessages` messages
  (default 50) from the chat log.
- **Session cap.** At most 10 sandboxes run at once; at the cap, the session
  idle the longest is stopped early.
- **Cleanup.** The `sandbox-reaper` Schedule runs hourly. It deletes sandboxes
  (and their volumes) stopped longer than `session.retention` (default 7d),
  and sandboxes labelled `managed-by=agents-worker` that no conversation owns.
  The chat log and the RGW copies are kept.

### Wake-ups

A type with `session.defaultWakeup` gets wake-ups: turns the worker starts
itself, so the agent can speak first. Each conversation of such a type has a
`Wakeup` workflow, `wakeup/<type>/<conversation>`, holding its one pending
wake-up. When that time comes, the workflow sends the conversation's
`ChatSession` a wake-up message, which runs as a turn like any other, after
any turn already queued.

- **Asking for one.** During any turn the agent may write
  `/sandbox/outbox/wakeup.json`, `{"at": "<RFC 3339 time>", "note": "<text>"}`.
  After the turn the worker reads and removes it and hands it to the `Wakeup`,
  where it replaces the pending wake-up. `at` must be 10 minutes to 60 days
  ahead, and `note`, for the agent's future self, at most 500 characters. A
  request that breaks these is dropped, and the next prompt says why. A turn
  that writes no file leaves the pending wake-up alone.
- **The default.** The first turn of a conversation starts its `Wakeup` with
  a wake-up `session.defaultWakeup` ahead, unless the turn asked for one.
  After a wake-up that asked for nothing, the next is a default again, which
  backs off while the user is silent: the 1st, 2nd and 3rd wake-ups since the
  user last wrote are followed by one in 1, the 4th in 3 and every later one
  in 7 times `session.defaultWakeup` (so 1d, 1d, 1d, 3d, 7d, 7d, … for
  `"24h"`). Any user message starts the count again. A request from the
  agent always wins over the default.
- **Failures.** A wake-up turn that fails is tried again an hour later, with
  the note `retry: <note>`.
- **The prompt header.** Every prompt of the type starts with the time now in
  `session.timezone` and the pending wake-up (`none yet` before the first
  turn; during a wake-up turn, how long the default after it would be), so a
  fresh sandbox knows the schedule it inherited:

  ```
  [now] 2026-10-11T08:12-07:00 (America/Los_Angeles)
  [next wake-up] 2026-10-11T21:00-07:00. Note: end-of-day check-in
  ```

  The header is not stored in the chat log.
- **Replies.** The wake-up's prompt is stored in `messages` with role
  `wakeup`, and the reply as written. A reply of exactly `<silent>` means the
  agent chose to say nothing, and is posted nowhere. Any other reply of a
  conversation from a Matrix room (ID `matrix-…`) is posted there as the
  agent ("Matrix"); one from any other conversation is only in the chat log. The
  worker's log says which (`Wake-up turn replied; posted to the room`, `…
  no room to post it to`, or `Wake-up turn ended silent`).
- **Ending.** A `Wakeup` ends at its next wake-up once the type is no longer
  published or has no `session.defaultWakeup`.

The pending wake-up, from the break-glass path:

```sh
temporal workflow query --address 127.0.0.1:7236 --namespace agents \
  --workflow-id wakeup/<type>/<conversation> --type pending
```

`habit-helper` is the type with wake-ups (`session.defaultWakeup` 24h); its
instructions say when to ask for one, with `habit wake-at`.

## Matrix

The agents are on Matrix (`matrix.garvey.sh`, `k8s/manifests/matrix`), each
type as its own user: `@habit-helper:matrix.garvey.sh`, `@pi:matrix.garvey.sh`.
The bridge, `agents-matrix` (`packages/agents/internal/matrix`), is a Synapse
application service: Synapse pushes it the events of those users, and it acts
as them.

- **Rooms.** At start, each agent makes sure it has a direct chat with each
  allowed user (`--allow`, now only `@ngarvey:matrix.garvey.sh`), creating one
  and inviting them if not. It also joins any room an allowed user invites it
  to. Invites from anyone else, and their messages, are ignored.
- **One room is one conversation** of each agent in it: the conversation ID is
  `matrix-` and the room ID's opaque part (`Conversation`), so the bridge keeps
  no table.
- **A text message** from an allowed user is the conversation's next
  `message` Update, with Update-with-Start as in "Sessions" and the event ID
  as the Update ID, so a message the bridge reads twice runs one turn. The
  agent marks it read, shows typing until the reply, and posts the reply
  rendered from Markdown; a `<silent>` reply posts nothing. A failed turn
  posts a short notice instead. Anything but text gets a notice; edits are
  ignored.
- **No encryption.** The direct chats the agents create are unencrypted, and
  only the agent may turn encryption on. In an encrypted room (Element X
  encrypts the direct chats it creates) the agent says once that it cannot
  read it.
- **Leaving.** When an allowed user leaves a room, its agents leave too; an
  agent that leaves or is removed cancels its conversation's `Wakeup`.
- **Wake-up replies.** After a wake-up turn the `Wakeup` workflow runs
  `DeliverReply` on the `agents-matrix` task queue, which only the bridge
  serves, so the worker never holds the appservice token. The bridge finds
  the room among the agent's joined rooms and posts the reply; the Matrix
  transaction ID is `wakeup-` and the `Wakeup` run's ID, so a retry posts
  nothing more. If the bridge is down the reply waits up to an hour for it,
  then is only in the chat log, as is one whose agent has left the room.
- **Adding a type** to Matrix: add its user to the registration's users
  namespace, in both copies (`k8s/manifests/agents/README.md`, "The Matrix
  bridge"), restart Synapse and the bridge.

## habit-data

The `habit-data` provider is Habit Helper's data: PostgREST in front of the
`habit_helper` database (`k8s/manifests/habit-helper/`). Its profile,
`habit-helper/habit-data-profile.yaml`, has a `token_grant`: the supervisor
trades the sandbox's JWT-SVID with agent-token for a 5 minute PostgREST JWT
and injects it, so no secret is in the sandbox, and only sandboxes with the
`habit-helper` type get one. Requests may read, insert and update; the
schema is `habit-helper/schema/`, applied by PostgREST's init container. Like
pi's litellm profile it is a template: `binaries` is the store path of the
`habit` CLI (`nix build .#habit-cli`), which `nix build .#habit-data-profile`
fills in, so the CLI is the only program in the sandbox that reaches the
data. The CLI is documented in `habit-helper/README.md`.

## pi

The pi coding agent (`pkgs.pi-coding-agent`), on `qwen3.8-flash-next` through
litellm (`k8s/manifests/litellm/`). litellm is the only thing it can reach, and
only pi's `node` can reach it, and only for `GET /v1/models` and
`POST /v1/chat/completions`. It cannot fetch packages, clone repositories or
browse.

litellm needs a key. The sandbox holds none: the `litellm` profile's
credential is a `token_grant`, so the sandbox's supervisor trades its
JWT-SVID (`spiffe://home.garvey.sh/agent/<type>/<sandbox-id>`) at agent-token
for the key granted to that type and puts it in each request's
`Authorization` header ("The token service" in
`k8s/manifests/agents/README.md`). pi gets litellm's `pi` key, which may
call `qwen3.8-flash-next` only. A sandbox created without an agent
type (`openshell sandbox create` by hand) gets no key, so its model calls fail
with a 502.

- `pi/models.json` and `pi/settings.json` are pi's config, installed in
  `/sandbox/.pi/agent` (`PI_CODING_AGENT_DIR`). The `apiKey` there is a
  placeholder that the supervisor replaces.
- `pi/litellm-profile.yaml` is a template. Its `binaries` entry has to be the
  real path of pi's node, a store path, which `nix build
  .#pi-agent-litellm-profile` fills in; `agents-apply` publishes the built
  one, so a nixpkgs update that changes node is picked up by the next run.
- The image's command is `sleep infinity`: pi runs only when exec'd.

### Running pi by hand

The worker's sandboxes are for sessions; for a one-off, make your own in the
same workspace, which has the published `litellm` provider. The reaper leaves
alone sandboxes without the worker's label.

A sandbox created with the CLI has no agent type, so agent-token refuses its
supervisor a key. Give the supervisor pod the type label by hand before pi's
first model call; the supervisor's SPIFFE ID follows the label.

`<image ref>` is the one `agents-apply` pushed:
`nix build .#agents-desired-state && jq -r .types.pi.image.ref result`.

```sh
# Start it idle and make it a pi sandbox.
openshell sandbox create --workspace agents --name pi-work --from <image ref> \
  --policy agents/pi/policy.yaml --provider litellm --detach
id=$(kubectl -n openshell get pod agents--pi-work -o jsonpath='{.metadata.annotations.openshell\.ai/sandbox-id}')
kubectl -n openshell label pod os-supervisor-$id agents.garvey.sh/type=pi

# Interactive
openshell sandbox exec --workspace agents -n pi-work --tty -- pi

# With a project: upload (honouring .gitignore), run pi in it.
openshell sandbox upload --workspace agents pi-work ./myproject /sandbox    # lands in /sandbox/myproject
openshell sandbox exec --workspace agents -n pi-work --tty --workdir /sandbox/myproject -- pi

openshell sandbox download --workspace agents pi-work /sandbox/myproject ./out    # copy results back
openshell sandbox delete --workspace agents pi-work
```

A stop and start makes a new supervisor pod, without the label: label it
again.

`openshell logs --workspace agents <name> --tail` shows the sandbox's activity,
including the connections it blocks.

### Getting information into the agent

- **Standing instructions:** `pi/instructions.md`, baked into the image.
  Changing it needs a new image, and a new sandbox, because the sandbox's
  `/sandbox` volume is seeded from the image only when it is created. Through
  `agents-apply` that happens by itself: the type's version changes.
- **The task:** a session's messages, or for a sandbox of your own, `pi` for
  an interactive session or `pi -p "<task>"` to answer one prompt and exit.
- **Files:** `openshell sandbox upload <name> <path> [dest]` into a running
  sandbox. A directory lands inside `dest`, under its own name. With network
  access limited to litellm, uploading is the only way code gets in.
