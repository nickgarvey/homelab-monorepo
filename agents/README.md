# agents

Coding agents that run in OpenShell sandboxes on the k3s cluster. The gateway
itself, and how to log the CLI in to it, is `k8s/manifests/openshell/`. The
`openshell` CLI is in the default dev shell (`nix develop`).

Each agent has a directory here with everything that defines it:

| File | What it is |
|---|---|
| `image.nix` | The sandbox image. `agents/outputs.nix` exports it as `<agent>-image`. |
| `policy.yaml` | Sandbox policy (filesystem, Landlock), baked into the image at `/etc/openshell/policy.yaml`. |
| `*-profile.yaml` | OpenShell provider profiles: the endpoints the agent may reach, and which binaries may reach them. |
| `instructions.md` | The agent's own instructions, installed as the agent's `AGENTS.md`. Named so tools working on this repo don't read it as repo conventions. |

A sandbox has no network access of its own. In OpenShell 0.1 the policy grants
none, and each provider attached to the sandbox adds the endpoints in its
profile. The sandbox supervisor's proxy makes the upstream connection, and
only for the binaries the profile names. This needs the gateway images from
NVIDIA/OpenShell#3702 (see `k8s/manifests/openshell/README.md`): stock 0.1.2
cannot resolve names in an IPv6-only cluster. Give endpoints as hostnames, not
IPv6 literals.

## pi

The pi coding agent (`pkgs.pi-coding-agent`), on `qwen3.8-flash-next` through
litellm (`k8s/manifests/litellm/`). litellm is the only thing it can reach, and
only pi's `node` can reach it, and only for `GET /v1/models` and
`POST /v1/chat/completions`. It cannot fetch packages, clone repositories or
browse.

- `pi/models.json` and `pi/settings.json` are pi's config, installed in
  `/sandbox/.pi/agent` (`PI_CODING_AGENT_DIR`).
- `pi/litellm-profile.yaml` is a template. Its `binaries` entry has to be the
  real path of pi's node, which is a store path, so
  `nix build .#pi-agent-litellm-profile` fills it in. Update the gateway's copy
  whenever the profile or nixpkgs' node changes (below).

### Build and register

```sh
nix build .#pi-agent-image
skopeo copy docker-archive:result docker://oci.garvey.sh/pi-agent:<tag>

nix build .#pi-agent-litellm-profile -o litellm-profile.yaml
openshell profile lint -f litellm-profile.yaml
openshell profile import -f litellm-profile.yaml    # first time only
openshell provider create --name litellm --type litellm
```

The cluster pulls from `oci.garvey.sh` anonymously. Use a new tag per push: a
node that already has a tag may run its cached copy.

To change the profile once it is imported, update it in place. The gateway
versions profiles, so the file has to carry the current `resource_version`
(`openshell profile export litellm -o yaml | grep resource_version`):

```sh
sed "s/^id: litellm\$/&\\nresource_version: <n>/" litellm-profile.yaml > update.yaml
openshell profile update litellm -f update.yaml
```

Sandboxes created afterwards get the new rules.

### Run

```sh
# Interactive
openshell sandbox create --name pi --from oci.garvey.sh/pi-agent:<tag> \
  --provider litellm -- pi

# With a project. The 0.1.2 CLI rejects --upload together with a command, so
# start the sandbox idle, upload (honouring .gitignore), then run pi in it.
openshell sandbox create --name pi-work --from oci.garvey.sh/pi-agent:<tag> \
  --provider litellm --detach -- sleep infinity
openshell sandbox upload pi-work ./myproject /sandbox    # lands in /sandbox/myproject
openshell sandbox exec -n pi-work --tty --workdir /sandbox/myproject -- pi

openshell sandbox download pi-work /sandbox/myproject ./out    # copy results back
openshell sandbox delete pi-work
```

`openshell logs <name> --tail` shows the sandbox's activity, including the
connections it blocks.

### Getting information into the agent

- **Standing instructions:** `pi/instructions.md`, baked into the image.
  Changing it needs a new image, and a new sandbox, because the sandbox's
  `/sandbox` volume is seeded from the image only when it is created.
- **The task:** the command after `--`. Run `pi` for an interactive session, or
  `pi -p "<task>"` to answer one prompt and exit.
- **Files:** `openshell sandbox upload <name> <path> [dest]` into a running
  sandbox. A directory lands inside `dest`, under its own name. With network
  access limited to litellm, uploading is the only way code gets in.
- **Scripted control:** `openshell sandbox exec` forwards stdin, so
  `openshell sandbox exec -n <name> -- pi --mode rpc` lets a local program drive
  pi through its JSON RPC protocol.
