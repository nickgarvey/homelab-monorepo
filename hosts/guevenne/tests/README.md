# TV regression tests

A Go suite covering the `guevenne` TV session. Everything in it exists because
the corresponding behaviour broke at least once.

```
cd hosts/guevenne/tests
go test ./...
```

It checks the real host over ssh. There is no fake mode: the bugs it catches
live in the interaction between Steam, gamescope and the hardware, and no stub
reproduces those.

## What each test covers

| Test | Bug it would have caught |
| --- | --- |
| `GamescopeSessionRunning` | session dying in a loop at boot |
| `TwoXwaylandServers` | one Xwayland — QtWebEngine children die |
| `SteamRunning` | Big Picture gone while gamescope survives |
| `OnlyHDMIOutputEnabled` | the broken internal panel getting the session |
| `AudioRoutesToTV` | wireplumber rule silently rejected, sound lost |

## Not covered

These need a person at the TV. Re-check them after any change to the session.

- Jellyfin video playback. No test plays a file, so decode, HDMI audio during
  playback, and controller input inside the player are all unverified by
  machine.
- The YouTube app rendering its TV interface rather than the desktop site. It
  is a Chromium `--app` window with a TV user-agent; a regression here shows up
  as the desktop site, which the D-pad cannot drive.

For these, `docs/guevenne-recovery.md` has the manual checklist.
