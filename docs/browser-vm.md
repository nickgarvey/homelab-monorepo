# Browser VM

`browser` is an Incus VM on lydia with an XFCE desktop and a Chromium that
Claude drives. Two MCP servers run inside it:

- **browser-playwright**: Playwright MCP attached to the session's Chromium
  over DevTools (127.0.0.1:9222 in the VM), for web pages.
- **browser-computer**: `packages/computer-use-mcp`, xdotool and scrot on the
  X display, with tools named after Anthropic's computer-use toolset
  (`screenshot`, `zoom`, `left_click`, `type`, `key`, `scroll`, ...). For
  anything outside the page: native dialogs, other apps, the desktop.

Both are what the root `.mcp.json` runs, through `incus exec` with a client
certificate that Incus restricts to the `browser` project.

## Pieces

| What | Where |
|---|---|
| VM config | `hosts/vms/browser/` (`desktop.nix`, `mcp.nix`) |
| Project, profile, certificate, creation | `homelab.incus.vms.browser` in `hosts/lydia/configuration.nix`, module `modules/virtualisation/incus-vms.nix` |
| Seed image it is created from | `hosts/vms/seed.nix` |
| Address | `modules/networking/lan-hosts.nix` (static lease by MAC, `browser.home.garvey.sh`) |
| Deploys | the deploy tool, after lydia (`Parent: "lydia"` in `packages/deploy/hosts.go`) |

The desktop runs as the user `claude` (auto-login, no SSH keys, no sudo). Its
Chromium profile lives in `/home/claude/.local/share/chromium-claude` and
persists across reboots and deploys.

## First bring-up

1. Deploy the router (the DHCP lease and DNS names come from `lan-hosts.nix`),
   then lydia. lydia's `incus-vms` unit trusts the client certificate,
   imports the seed image into the `browser` project and creates and starts
   the VM. `systemctl status incus-vms` on lydia shows what it did.
2. Pin the VM's SSH host key, read through Incus rather than trusted from the
   network (the deploy tool runs SSH in batch mode):

   ```bash
   key=$(INCUS_CONF=~/.config/incus-browser incus exec browser -- \
     /run/current-system/sw/bin/cat /etc/ssh/ssh_host_ed25519_key.pub | cut -d' ' -f1-2)
   echo "browser,browser.home.garvey.sh,10.28.12.111,2001:470:482f::15 $key" >> ~/.ssh/known_hosts
   ```

   The seed has no static IPv6 yet, so SSH to `browser` tries the AAAA
   record, times out, and falls back to IPv4 (a few seconds).
3. Deploy `browser` with the deploy tool. The seed has only SSH and DHCP; this
   installs the desktop and the MCP servers.
4. Restart the VM once (`incus restart --project browser browser`): the seed
   booted to `multi-user.target`, and a switch does not move a running system
   to the graphical target, so the desktop starts only on the next boot.
5. On the workstation, set up the client directory (below) and start Claude
   Code in the repo; approve the two project MCP servers.

`incus exec` does not put `/run/current-system/sw/bin` on PATH, which is why
`.mcp.json` names the servers by full path.

Removing the VM from lydia's config does not delete it. Delete it by hand:
`incus delete --project browser browser`.

## Client certificate

The key is only on the workstation; the repo holds the public certificate
(`hosts/vms/browser/incus-client.crt`). To set up a workstation:

```bash
mkdir -p ~/.config/incus-browser
INCUS_CONF=~/.config/incus-browser incus remote generate-certificate
cat > ~/.config/incus-browser/config.yml <<'YAML'
default-remote: lydia
remotes:
  lydia:
    auth_type: tls
    project: browser
    protocol: incus
    public: false
    addr: https://incus.home.garvey.sh:8443
aliases: {}
YAML
```

A new key means a new certificate: replace `incus-client.crt` with
`~/.config/incus-browser/client.crt` and deploy lydia. The unit adds
certificates and never removes them, so drop the old one by hand
(`incus config trust list`, `incus config trust remove <fingerprint>`).

Check: `INCUS_CONF=~/.config/incus-browser incus list` shows only `browser`.

## Watching and taking over

`incus console --project browser browser --type=vga` opens the desktop over
SPICE (or the console in the Incus web UI, project `browser`). Log in to
sites or solve a CAPTCHA there; the profile keeps the session for Claude.

### From a phone

The Incus web UI at `https://incus.home.garvey.sh:8443` has the same console
(Instances → `browser` → Console), and logs in with a client certificate. The
phone's is `hosts/vms/browser/incus-phone.crt`, restricted to the `browser`
project like the MCP one. Its key and a `.p12` bundle for the phone are on
talos in `~/.config/incus-browser/` (`phone.key`, `phone.p12`,
`phone.p12.pass`); the `.p12` uses SHA1/3DES so that iOS and Android both
import it.

- iOS: open the `.p12` (AirDrop or Files), install it under Settings →
  General → VPN & Device Management, then open the UI in Safari and pick the
  certificate when asked.
- Android: Settings → Security → Encryption & credentials → Install a
  certificate → VPN & app user certificate, then open the UI in Chrome and
  pick it.

The UI serves a Let's Encrypt certificate for `incus.home.garvey.sh`, so the
browser needs no exception. Away from home it should be reachable over the tailnet
on lydia's IPv6 address, through the homelab /48 route dragonsreach
advertises (not yet tried).

## Snapshots

The whole VM, profile included:

```bash
incus snapshot create --project browser browser <name>
incus snapshot restore --project browser browser <name>
```
