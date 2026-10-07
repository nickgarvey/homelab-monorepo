# Homelab Monorepo Conventions

One repo for the whole homelab. Each top-level directory was imported from its
own repo with full history; tags `imported/<dir>-<sha>` mark the last commit
of each old repo.

| Directory | What it holds | Its own conventions |
|---|---|---|
| `nix-configs/` | NixOS hosts, shared modules, the `deploy` tool. Flake lives here; run `nix run .#deploy` from this dir. | `nix-configs/AGENTS.md` |
| `k8s-gitops/` | Kubernetes manifests for the k3s cluster, applied by hand (no in-cluster GitOps controller). | `k8s-gitops/AGENTS.md` |
| `homelab-nixpkgs/` | Homelab packages and container images (lan-table, thinkrail, …). `nix-configs` consumes some as `path:../homelab-nixpkgs/<pkg>` inputs. | — |
| `homelab-esp32-devices/` | ESP32 device firmware (ESP-IDF) and its dev shell. | — |

Each directory keeps its own flake; there is no root flake. Cross-references
between directories ("k8s-gitops manifests/zot/secret.yaml", "nix-configs
modules/networking/dns.nix") are paths relative to the repo root.

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
