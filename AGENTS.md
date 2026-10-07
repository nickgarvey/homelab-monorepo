# Homelab Monorepo Conventions

One repo for the whole homelab. Each top-level directory was imported from its
own repo with full history; tags `imported/<dir>-<sha>` mark the last commit
of each old repo.

| Directory | What it holds | Its own conventions |
|---|---|---|
| `nix-configs/` | NixOS hosts, shared modules, the `deploy` tool (`nix run .#deploy`). | `nix-configs/AGENTS.md` |
| `k8s-gitops/` | Kubernetes manifests for the k3s cluster, applied by hand (no in-cluster GitOps controller). | `k8s-gitops/AGENTS.md` |
| `homelab-nixpkgs/` | Homelab packages and container images (lan-table, thinkrail, …). Hosts use them as `inputs.self.packages.<system>.<pkg>`. | — |
| `homelab-esp32-devices/` | ESP32 device firmware (ESP-IDF) and its dev shell. | — |

## The flake

There is one flake, at the repo root, with one `flake.lock`. Each directory
defines its outputs in its own `outputs.nix`, and `flake.nix` merges them,
failing evaluation if two directories define the same output name. Nix finds
the root flake from any subdirectory, so `nix run .#deploy` and
`nix build .#<pkg>` work from anywhere in the repo.

- `nix develop` gives the default shell: deploy, go, python, kubectl, skopeo, jq.
  The only other shells are `.#esp32` (ESP-IDF; needed for anything in
  `homelab-esp32-devices/`) and `.#makernexus`.
- Container images that `homelab-nixpkgs/deploy.sh` pushes live under
  `legacyPackages.x86_64-linux.images`.
- `nixpkgs` is shared by hosts and homelab-nixpkgs. `nixpkgs-stable` (Ceph)
  and `nixpkgs-esp32` (firmware) are deliberate separate pins.

Cross-references between directories ("k8s-gitops manifests/zot/secret.yaml",
"nix-configs modules/networking/dns.nix") are paths relative to the repo root.

SOPS rules for every directory are in the root `.sops.yaml`.

## Deploys and reboots

The deploy tool warns "needs reboot" whenever a host's running kernel differs
from its new generation. Treat that as informational: do not relay it, suggest
a reboot, or list it as a follow-up. The human decides when hosts reboot. Only
raise a reboot when the task itself cannot finish without one (e.g. a kernel
module or kernel parameter the change depends on).

## Commit messages

Use this shape, with the subject on the first line:

```
<subject>

Human Text:
None

LLM Text:
<body: what changed and why>
```

Leave `Human Text:` as `None`; the human fills it in later. Put your
explanation under `LLM Text:` (leave it empty if the subject says it all). Do
not add a `Co-Authored-By:` trailer.
