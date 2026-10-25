# Rebuilding guevenne's home state

`nixos-anywhere --flake .#guevenne` gets you a working TV box: the gamescope Steam
session, greetd autologin, the display pinned to HDMI-A-1, audio forced to the TV, both
app launchers, and the patched jellyfin-desktop build. What it does **not** get you is
anything that lives in `~`, because Steam and Jellyfin Media Player both own their config
and rewrite it at runtime.

This is the list of what to redo, in order, and the two things that will waste your
afternoon if you don't know them going in.

Nothing here is secret — guevenne has no sops secrets — so this file is safe to read
end to end before you start.

---

## 0. Before anything: let Steam finish bootstrapping

**On a fresh install the TV will look catastrophically broken.** greetd autologins, the
screen flashes, and you land back at the greeter. It repeats on every boot. Nothing in
the Nix config is wrong.

The cause is that Steam has never run. On first launch it downloads its client
(~74 MB, ending up around 3 GB) and then **re-execs itself**; gamescope sees its primary child
exit, logs `launch: Primary child shut down!`, and tears the session down after about two
seconds. Every boot restarts the download from where it left off and dies again.

Fix it once, over SSH, before touching the TV:

```sh
ssh guevenne
XDG_RUNTIME_DIR=/run/user/1000 timeout 600 steam -tenfoot </dev/null
du -sh ~/.local/share/Steam        # want multiple GB, not ~74M
```

Reboot and the session stays up. This is first-boot-only.

## 1. Sign in to Steam

At the TV, with the controller. `Remember Me` so it survives reboots. Everything after
this depends on the account existing, because Steam's per-account `userdata/` directory
is where the shortcuts live.

## 2. Re-add the two non-Steam shortcuts

Steam → Add a Non-Steam Game, twice:

| Name | Target |
|---|---|
| Jellyfin | `/run/current-system/sw/bin/jellyfin-tv` |
| YouTube | `/run/current-system/sw/bin/youtube-tv` |

Both are wrapper scripts the Nix config provides, so the paths are stable. Enable
`AllowDesktopConfig` / overlay if you want a per-app controller mapping.

These land in `~/.local/share/Steam/userdata/<account>/config/shortcuts.vdf`, a binary
file Steam rewrites on exit — which is why it isn't managed declaratively. Recreating it
by hand is a minute's work; generating it is not worth the fight.

Steam assigns each shortcut a fresh `appid`, so any `steam://rungameid/...` links you
had written down are invalidated. Nothing in the config depends on them.

### Tile artwork

Without artwork both shortcuts render as blank grey rectangles among the game capsules.
Steam reads artwork from `~/.local/share/Steam/userdata/<account>/config/grid/`, keyed by
each shortcut's `appid` — no edit to the binary `shortcuts.vdf` is needed:

| File | Purpose |
|---|---|
| `<appid>p.png` | portrait capsule, 600x900 — the tile you actually see |
| `<appid>.png` | landscape capsule, 920x430 |
| `<appid>_hero.png` | detail-page banner, 1920x620 |
| `<appid>_logo.png` | transparent logo Steam draws over the hero |

The appids change every time the shortcuts are recreated. Read the current ones out of
`shortcuts.vdf` — they are little-endian int32s following each `appid` key — or just look
at a running shortcut's `reaper SteamLaunch AppId=...` in `ps`.

`jellyfin-desktop` ships its own logo (`share/icons/hicolor/scalable/apps/*.svg`); the
YouTube wrapper ships none, so its tile is drawn from primitives. To regenerate, with
`nix shell nixpkgs#imagemagick`:

```sh
GRID=~/.local/share/Steam/userdata/<account>/config/grid; mkdir -p "$GRID"
FONT=$(find /nix/store -name DejaVuSans-Bold.ttf | head -1)
SVG=$(find /nix/store/*jellyfin-desktop*/share/icons -name '*.svg' | head -1)
magick -background none -density 900 "$SVG" -resize 620x620 /tmp/jf_logo.png
magick -size 620x440 xc:none -fill '#FF0000' \
  -draw 'roundrectangle 0,0 619,439 100,100' \
  -fill white -draw 'polygon 245,120 245,320 415,220' /tmp/yt_logo.png
# then per app, with its own appid, logo, gradient and label:
magick -size 600x900 gradient:'#5c2d91'-'#0b1622' \
  \( /tmp/jf_logo.png -resize 340x340 \) -gravity center -geometry +0-60 -composite \
  -gravity south -font "$FONT" -pointsize 54 -fill white -annotate +0+90 'Jellyfin' \
  "$GRID/<appid>p.png"
```

Jellyfin uses `#5c2d91`→`#0b1622`, YouTube `#2a0d0d`→`#0b0b0b`.

**Steam caches artwork at startup**, so new files do not appear until it restarts — and
see the warning below about how to restart it.

## 3. Sign in to Jellyfin and set the TV layout

Launch the Jellyfin tile. Server is `https://jellyfin.garvey.sh` — LAN resolution is
IPv6-only via the split-horizon override in `modules/networking/dns.nix`, so if it can't
reach the server, check the box actually has its static AAAA before blaming Jellyfin.

Then Settings → Display → Layout → **TV**. Without it you get the desktop layout, which
is unreadable at couch distance.

That preference lives in the web client's `localStorage` inside JMP's QtWebEngine
profile, i.e. a LevelDB blob — not a file worth managing. It may also be redundant now:
jellyfin-web asks the host app via `appHost.getDefaultLayout()`, JMP feeds that from its
`webMode` setting, and that hook only started working once the NativeShell injection bug
was fixed (jellyfin-desktop commit `cabddc6`, in nixpkgs' 2.0.0-unstable-2026-08-31).
Worth checking before setting it by hand.

## 4. Recreate the controller input map

This is the one piece that is genuinely hard to reconstruct from memory, so it gets the
most detail.

Without a map, JMP receives the controller but maps nothing useful: Steam Input presents
it as `Microsoft X-Box 360 pad 0`, and JMP's bundled Xbox example does not match, so you
get a flood of raw `KEY_AXIS_2_DOWN` / `KEY_AXIS_5_UP` events from trigger drift and no
navigation at all.

Write this to `~/.local/share/jellyfin-desktop/profiles/<profile-id>/inputmaps/steam-controller.json`
(the profile id is a random UUID — take whatever directory already exists):

```json
{
  "name": "Steam Controller (Steam Input xbox360 emulation)",
  "idmatcher": "Steam Controller.*|Microsoft X-Box.*|XInput.*",
  "mapping": {
    "KEY_BUTTON_0": "enter",
    "KEY_BUTTON_1": { "short": "back", "long": "home" },
    "KEY_BUTTON_6": "back",
    "KEY_BUTTON_7": "home",
    "KEY_HAT_UP": "up",
    "KEY_HAT_DOWN": "down",
    "KEY_HAT_LEFT": "left",
    "KEY_HAT_RIGHT": "right",
    "KEY_AXIS_0_UP": "left",
    "KEY_AXIS_0_DOWN": "right",
    "KEY_AXIS_1_UP": "up",
    "KEY_AXIS_1_DOWN": "down",
    "KEY_AXIS_2_UP": "", "KEY_AXIS_2_DOWN": "",
    "KEY_AXIS_5_UP": "", "KEY_AXIS_5_DOWN": ""
  }
}
```

Three things in there are not guessable, and are why this file is worth documenting
rather than re-deriving:

- **`idmatcher` must match "Steam Controller".** JMP reports that as the source name even
  though SDL calls the device an Xbox 360 pad. The bundled `xbox-controller-windows.json`
  example matches `XInput.*|Microsoft.*joystick driver` and therefore never applies.
- **The d-pad is on the hat, not buttons 11–14.** JMP's own example assumes buttons; this
  pad enumerates as *11 buttons and 6 axes* (JMP logs `JoyStick # 0 is Steam Controller
  with 11 buttons and 6 axes` at startup), so buttons 11–14 do not exist. Buttons 8/9 are
  guide and stick-click, not start/back.
- **Axes 2 and 5 must be bound to `""`.** They are the triggers, they idle noisily, and
  unbound they spam JMP's input queue continuously.

Everything beyond A/B, the d-pad and the left stick (subtitle cycling, seek on the
shoulder buttons) is taste — add from JMP's `inputmaps/examples/` if you want it.

---

## Debugging aids

You cannot see the TV over SSH, and process liveness is not the same as a picture on
screen — assuming otherwise wasted a lot of time the first time round. These give you
eyes and hands:

| Need | Command |
|---|---|
| See the screen | `DISPLAY=:0 gamescopectl screenshot /tmp/tv.png` |
| Send input | `ydotoold` + `YDOTOOL_SOCKET=... ydotool key 28:1 28:0` (uinput; `xdotool` does **not** work, Steam ignores synthetic X events) |
| JMP internals | launch with `--remote-debugging-port=9222`, then drive it over CDP |
| JMP log | `~/.local/share/jellyfin-desktop/profiles/*/logs/jellyfin-desktop.log` |

`Web Client has not connected, handling input in host instead.` in that log means the
NativeShell bridge is down and *all* input is dead, and mpv logs
`vo/libmpv: No render context set`. In jellyfin-desktop 2.0.0 the cause was the bootstrap
script being registered on the view (`web.userScripts`) rather than the profile, so it was
not injected into the remote web client after the cross-origin navigation from the bundled
server picker; upstream fixed it in `cabddc6`. With `--remote-debugging-port`, check the
web client document for `window.jmpInfo`, `window.NativeShell` and `QWebChannel`.

Qt caches compiled QML under `~/.cache/jellyfin-desktop/qmlcache`, keyed on the `qrc:`
path, so a stale unit can keep running an old `webview.qml` across rebuilds. The
`jellyfin-tv` launcher sets `QML_DISABLE_DISK_CACHE=1`; if you launch JMP another way,
compare the QML line numbers in the log (`@ 275`) with the file you think you built
before concluding a fix didn't work.

## Do not use "Switch to Desktop"

Steam's Power menu offers "Switch to Desktop", but greetd has no desktop session on
this host, so choosing it leaves the TV black. Reboot to recover (see "Restarting the
Steam session" below).

## If the hardware changed

`modules/networking/lan-hosts.nix` pins guevenne's identity to the USB ethernet dongle's
MAC (`00:e0:4c:68:19:5d`), not the laptop. A different dongle means editing that entry,
or the box loses its static AAAA and Jellyfin becomes unreachable on the LAN.

## Restarting the Steam session

**`systemctl restart greetd` does not bring Steam back — it drops you at a login
prompt.** greetd runs `initial_session` (the ngarvey autologin that launches
`steam-gamescope`) only on its *first* start after boot. On any later start it runs
`default_session`, which is tuigreet. With no keyboard reachable from the couch that
leaves the TV stuck at a greeter.

To restart the session, reboot:

```sh
ssh guevenne sudo systemctl reboot
```

The session is back about 20 seconds after boot. Confirm with
`pgrep -c gamescope` (expect a non-zero count) and `pgrep -f 'steam.*-tenfoot'`.

Do **not** try to refresh Steam's UI with a CDP `Page.reload` on the Big Picture
target — it knocks Steam into desktop mode and blanks the TV, and recovery is a
reboot anyway.
