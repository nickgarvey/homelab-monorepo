# Homelab Monorepo Conventions

One repo for the whole homelab. It was assembled from four repos imported with
full history; tags `imported/<dir>-<sha>` mark the last commit of each, under
the directory names of the time (nix-configs, k8s-gitops, homelab-nixpkgs,
homelab-esp32-devices).

| Directory | What it holds | Its own conventions |
|---|---|---|
| `hosts/` | One directory per NixOS host; `hosts/outputs.nix` defines every `nixosConfiguration`. | below |
| `modules/` | Shared NixOS modules, grouped by concern; dotfiles and other inputs sit beside the module that uses them. | below |
| `patches/` | Patches against upstream nixpkgs packages, and `UPSTREAMABLE_FIXES.md` tracking what should go upstream. | — |
| `secrets/` | SOPS secrets for NixOS hosts. (k8s and firmware secrets live in their own directories.) | — |
| `docs/` | Runbooks and notes for hosts and infrastructure. | — |
| `devshells/` | The dev shells (`default`, `makernexus`). | — |
| `packages/` | Software we build: packages, container images, the `deploy` tool. | — |
| `k8s/` | Kubernetes manifests for the k3s cluster, applied by hand (no in-cluster GitOps controller). | `k8s/AGENTS.md` |
| `firmware/` | ESP32 device firmware (ESP-IDF) and its dev shell. | — |
| `agents/` | Agents run in OpenShell sandboxes on k3s: per-agent image, sandbox policy, provider profile and instructions. | `agents/README.md` |
| `pki/` | The private root CA: its certificate, its sops-encrypted key, and the scripts that create it and sign intermediates. | `pki/README.md` |

## The flake

There is one flake, at the repo root, with one `flake.lock`. `hosts/`,
`devshells/`, `packages/`, `firmware/`, `agents/` and `pki/` each define their outputs in
their own `outputs.nix`, and `flake.nix` merges them, failing evaluation if two
define the same output name. Nix finds the root flake from any subdirectory,
so `nix run .#deploy` and `nix build .#<pkg>` work from anywhere in the repo.

- `nix develop` gives the default shell: deploy, openshell, go, python, kubectl, helm,
  skopeo, jq.
  The only other shells are `.#esp32` (ESP-IDF; needed for anything in
  `firmware/`) and `.#makernexus`.
- There is one `nixpkgs`. Two inputs keep their own locked nixpkgs on purpose:
  `nixpkgs-esp-dev` (pins ESP-IDF for the firmware) and `nixos-raspberrypi`
  (skyforge's vendor kernel must match its userspace). Ceph is held to one major
  by an assertion in `modules/services/ceph.nix`.

Paths in comments and docs ("k8s/manifests/zot/secret.yaml",
"modules/networking/dns.nix") are relative to the repo root unless they start
with `./`.

SOPS rules for every directory are in the root `.sops.yaml`.

## Packages

Everything under `packages/` is built from this repo. Two kinds live there:

- Packages with an `outputs.nix` are flake outputs (`nix build .#<name>`);
  hosts use them as `inputs.self.packages.<system>.<name>`.
- Packages with a `default.nix` (helium-browser, agent-issue-tracker, agents, bosl2,
  storj-gateway-st) are built by the module that uses them, with
  `pkgs.callPackage ../../packages/<name> { }`.

`deploy` is neither: it has only Go source, and `packages/outputs.nix` builds
it inline as the `deploy` flake output (also used as the `deploy-tests` check).

## NixOS: module taxonomy

Modules live under `modules/` grouped by concern. Add new modules to the
narrowest matching subdir; create a new one only when you have ≥2 related files.

| Subdir | What belongs here |
|---|---|
| `core/` | Baseline config applied to virtually every host (NixOS defaults, SSH, locale, CA certs). Currently: `nixos-common`, `nspawn-cleanup`, `server-base` (networkd + systemd-boot + sops defaults for LAN servers; k3s-common builds on it). |
| `networking/` | Network configuration and shared host-address data. Currently: `networkd`, `network-manager`, `dns`, `zone` (Knot zone file built from the dns.nix data), `lan-hosts`, `ipv6-accept-ra-routes`, `thunderbolt-mesh` (FRR-routed Thunderbolt mesh between the storage nodes). |
| `desktop/` | Workstation/desktop features (Wayland compositors, audio, packages, udev rules for peripherals). Currently: `common-workstation`, `common-tv`, `niri`, `steam`, `upower-overlay`, `printer`, `orca-slicer`, `orca-skyforge` (OrcaSlicer preset host + API key for skyforge), `openscad` (OpenSCAD + BOSL2, and the Claude Code skill for LLM-written parts), `rtl-sdr`, `zot-push` (containers auth.json for pushing to oci.garvey.sh). |
| `services/` | Host services that don't fit a tighter category. Currently: `smb-automount`, `fancontrol`, `ceph` (mon/mgr/OSD/RGW/MDS on joor/zah/frul, no Rook), `ceph-restore` (restore tools for the Ceph backups the k3s worker makes), `cephfs-mount` (~/local-drive on CephFS, per-host cephx key), `ceph-cluster` (fsid and mon hosts, plain data shared by ceph and cephfs-mount), `ha-cert-probe` (checks the cert Home Assistant serves), `netboot-server` (pixiecore PXE for live-netboot), `reboot-watchdog` (hardware watchdog kept running through reboot, so a hung reboot resets), `shutdown-debug` (temporary: console line per device shut down at reboot, to find the driver that hangs), `repo-backup` (daily archive of ~/homelab-monorepo, ait DB included, to CephFS with 30-day retention), `vector-agent` (host metrics pushed to Prometheus). |
| `k3s/` | Kubernetes cluster node configuration. Currently: `k3s-common`. |
| `router/` | Router subsystem (nftables, DHCP, DNS, IPv6 tunnel, NAT64). Exposes a `default.nix` — import the directory. |
| `containers/` | systemd-nspawn container definitions. Common networking lives in `common.nix`. |
| `microvm/` | MicroVM guest definitions. |
| `virtualisation/` | VM hypervisors. Currently: `incus`. |
| `llms/` | LLM tooling. Currently: `hf-to-garage` (pushes HuggingFace repos to the `llm-models` bucket), `pi` (terminal coding agent). |
| `home/` | home-manager config. Currently: `ngarvey` (imported by `common-workstation`), and the dotfiles it installs in `dotfiles/`. |

## NixOS: imports

Hosts import individual files by path:

```nix
imports = [
  ../../modules/core/nixos-common.nix
  ../../modules/networking/networkd.nix
  ../../modules/desktop/common-workstation.nix
];
```

No `default.nix` aggregators — except `router/`, which intentionally exposes
one. Hosts opt in to each module explicitly.

## NixOS: host `configuration.nix` structure

```nix
imports = [
  ./hardware-configuration.nix          # hardware-config first
  # shared modules, grouped by subdir:
  ../../modules/core/nixos-common.nix
  ../../modules/networking/networkd.nix
  ../../modules/services/fancontrol.nix
  # host-specific opt-ins last
];
# host-specific config below the imports block
```

## NixOS: paths escaping `modules/`

A file used by a single module (a dotfile, a script, a JSON data file) sits
next to that module and is referenced as `./<file>`; home-manager dotfiles are
under `modules/home/dotfiles/`. Root directories (`packages/`, `patches/`,
`secrets/`) are reached by paths relative to the module's own location: from a
subdir such as `modules/desktop/` they are two levels up (`../../`), not one.
Example: `../../patches/my-fix.patch`.

## NixOS: module header comment

The first non-blank line of each module should be a `#` comment describing its
purpose in one sentence, so `ls` + skimming the top of a file is enough to
understand what it does.

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
