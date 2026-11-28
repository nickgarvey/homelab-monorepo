# flake-bot

Keeps `dev` current with `nix flake update` and publishes each `dev` commit's host
systems for hosts to switch to without building (ait nix-b77Zn). It runs on wabbajack as
`flake-bot.service` (modules/services/flake-bot.nix), a Temporal worker on task queue
`flake-bot` in namespace `flake-bot`, reached through
`frontend.temporal.k8s.home.garvey.sh:7233` (k8s/manifests/temporal/README.md, "Workers").

## Workflows

| Workflow | Schedule | What it does |
|---|---|---|
| `UpdateRun` | `flake-bot-update`, daily 10:00 UTC | Rebuilds `flake-update` as `origin/dev` + one fresh `nix flake update` commit + the patches `flake-update` carried, builds every target, pushes what built to celler, force-pushes `flake-update` (leased) and, if every target built, fast-forwards `dev` to it |
| `DevBuild` | `flake-bot-dev`, every 15 minutes | Builds `origin/dev`'s head if `manifest.json` does not cover it yet, pushes what built to celler and merges it into `manifest.json` |

Targets are every `nixosConfiguration` (its toplevel) and every `checks.x86_64-linux`
entry. Each is evaluated on its own; all that evaluate are built in one
`nix build --keep-going`. Both workflows fail, with the failed targets in the error,
when any target fails; `DevBuild` still publishes the hosts that built.

The worker runs one activity at a time, so builds never compete and the two jobs'
git steps never share the clone. Its state is in `/var/lib/flake-bot`: `repo` (the
clone), `work/update` and `work/dev` (a worktree each), `logs` (one build log per
commit).

## Carried patches

A commit on `flake-update` that is not on `dev`, other than the bot's own lock update
(trailer `Flake-Bot: lock-update`), is a patch. Each run cherry-picks the patches onto
the new lock update with `--empty=drop`, so one `dev` already has drops out. A patch
that no longer applies stops the run before any push, since force-pushing would drop it
from `flake-update`; the run fails with type `PatchConflict`.

Landing a fix by hand: commit it on top of `origin/flake-update` and push that branch;
the next run carries it. Or put it on `dev`; the next run drops the patch as empty.

## Manifest

`s3://flake-bot/manifest.json` on RGW (`https://rgw.home.garvey.sh/flake-bot/manifest.json`,
readable by anyone; the worker sets that bucket policy):

```json
{
  "commit": "<newest dev commit built>",
  "subject": "...",
  "built_at": "...",
  "hosts": {
    "dovahkiin": { "path": "/nix/store/...-nixos-system-dovahkiin-...", "commit": "...", "subject": "...", "built_at": "..." }
  }
}
```

A host that failed to build keeps its previous entry.

## Identity

- Temporal: its X.509-SVID `spiffe://home.garvey.sh/host/wabbajack/flake-bot` from the
  host's SPIRE agent (selector `systemd:id:flake-bot.service`).
- RGW: the same ID's JWT-SVID, for STS role `flake-bot` (modules/services/rgw-roles.nix).
- GitHub: App `garvey-homelab-bot` (ID 5962895, installation 186728033), one-hour
  installation tokens from its key in `secrets/flake-bot.yaml`, passed to git as an HTTP
  header through git's config environment. Rulesets keep it off `main` and stop it
  force-pushing or deleting `dev`.
- celler: the push token `wabbajack` in `secrets/celler-push.yaml`.

## Running a workflow now

```sh
temporal schedule trigger --schedule-id flake-bot-update --namespace flake-bot ...
```

with the connection flags of an identity that may write to the namespace, or from the
Temporal UI.
