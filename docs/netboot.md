# Netbooting the live environment

Any machine on the LAN can PXE-boot the same rescue/install system the
`live-iso` ISO carries, with no USB stick. `lydia` serves it.

## Booting a machine

1. In the machine's firmware, enable network/PXE boot and put it first in the
   boot order (on the Beelink/Topton N150 boxes this is *Advanced → Network
   Stack → IPv4 PXE Support*).
2. Boot. The machine takes a normal DHCP lease from the router, then picks up
   the boot answer from lydia, chainloads iPXE, and pulls the kernel and initrd
   over HTTP.
3. It lands at a root shell — passwordless on the console, and reachable over
   SSH with the usual key. `disko`, `parted`, `btrfs-progs`, `cryptsetup` and
   `smartmontools` are on PATH.

Watch it from lydia:

```sh
journalctl -u pixiecore -f
curl -s localhost:8880/_/booting     # 200 once the server is up
```

## How it is wired

| Piece | Where |
|---|---|
| Live system, medium-independent | `hosts/live-iso/common.nix` |
| ISO flavour | `hosts/live-iso/configuration.nix` → flake output `live-iso` |
| Netboot flavour | `hosts/live-iso/netboot.nix` → flake output `live-netboot` |
| Boot server | `modules/services/netboot-server.nix`, imported by `hosts/lydia` |

The server is [pixiecore](https://github.com/danderson/netboot) in `boot` mode:
it is handed the `live-netboot` kernel, initrd and kernel command line by store
path and serves TFTP (iPXE binaries) plus HTTP (kernel/initrd) itself. Ports
67/4011 udp, 69 udp and 8880 tcp are opened on lydia via `openFirewall`.

### Why pixiecore on lydia and not `BootFilename=` on the router

The router's DHCP server is systemd-networkd (`modules/router/dhcp.nix`), which
has a single `BootFilename=` and no way to select a boot file per client
architecture — BIOS and UEFI clients need different iPXE binaries. pixiecore
answers as **proxyDHCP**: it supplies boot information only and never hands out
addresses, so it coexists with networkd on a different host entirely, and the
static-lease table that also feeds DNS stays untouched. lydia is always on and
sits on the same flat `10.28.0.0/16` broadcast domain, which is all proxyDHCP
needs.

## Gotcha: the live system is in lydia's closure

`mode = "boot"` references the live kernel, initrd and toplevel by store path,
so they are part of lydia's system closure. **A change under `hosts/live-iso/`
is not served until lydia is rebuilt and redeployed:**

```sh
nix develop -c deploy --hosts lydia
```

Confirm the new paths took effect with `systemctl cat pixiecore | grep ExecStart`.

## Installing a host: keys and trust

The netboot image is served to anyone on the LAN who asks, so it must never
contain a host private key, an age key, or any other secret.

Nothing secret is pushed during an install either. A host's sops identity is
derived from the SSH host key it generates on its own first boot:

1. Install with `nixos-anywhere --flake .#<host>`, without `--extra-files`.
2. After the first boot, derive the host's age recipient from its key:
   ```sh
   ssh <host> cat /etc/ssh/ssh_host_ed25519_key.pub | nix run nixpkgs#ssh-to-age
   ```
3. Add it to `.sops.yaml` as `&<host>`, add it to the creation rules for the
   secrets the host needs, and re-encrypt those files:
   ```sh
   nix run nixpkgs#sops -- updatekeys secrets/<file>.yaml
   ```
4. Deploy the host. sops-nix reads the SSH host key by default
   (`sops.age.sshKeyPaths`), so no `sops.age.keyFile` is set.

lydia, dragonsreach, wabbajack and the workstations predate this and use a
hand-placed `/root/.config/sops/age/keys.txt` (`sops.age.keyFile`). When one of
them is reinstalled, move it to the flow above rather than pushing `keys.txt`.

### Accepted risk: a rogue boot server

PXE is unauthenticated. Another machine on `10.28.0.0/16` can answer the
client's DHCP broadcast with its own proxyDHCP offer, and the kernel and initrd
travel over plain TFTP/HTTP. Whoever wins that race boots their own image on the
target, and that image writes the disk, so it can plant a host key or SSH key
it knows. The steps above would then derive a sops recipient from a key the
attacker holds.

This is accepted: installs are rare, and the exposure lasts only while a machine
is netbooting. For a host where it matters, install from the `live-iso` USB
image instead, which takes the network out of the boot path.
