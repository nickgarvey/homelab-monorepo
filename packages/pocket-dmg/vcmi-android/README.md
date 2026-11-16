# VCMI for the Pocket DMG

A VCMI Android build with `shrink-to-fit.patch`: a text label that is too wide
(single line) or too tall (multi-line) for its box is drawn in a smaller font,
down to 70% of the configured size, instead of having its end cut off. Text
boxes that scroll keep the configured size. Original bitmap fonts can't be
resized, so they behave as before.

This matters at Font Scaling 130% on the DMG (1240x1080, 155% interface
scale), where creature ability descriptions, Town Hall names and scenario
names otherwise get cut off. `../vcmi-mod/short-labels` shortens the worst
names so they keep the full 130% size.

The app id is `is.xyz.vcmi.dmg` ("VCMI DMG"), so it installs beside the
official VCMI. It is signed with VCMI's public CI key for daily builds
(`CI/android/vcmi-travis.jks`).

## Patch header

- Upstream: https://github.com/vcmi/vcmi
- Upstreamable: yes, as an opt-in feature; not proposed yet
- How: `CLabel::trimText` and `CMultiLineLabel::splitText` lower a per-label
  font scale; `RenderHandler::loadFont(font, scalePercent)` caches TrueType
  fonts at that size
- Written: 2026-11-15, VCMI 1.7.5
- Last checked: 2026-11-15, VCMI 1.7.5
- Remove when: VCMI shrinks text to fit on its own

## Build

```sh
git clone --depth 1 --branch 1.7.5 --recurse-submodules --shallow-submodules \
  https://github.com/vcmi/vcmi.git vcmi
git -C vcmi apply "$PWD/shrink-to-fit.patch"
docker build -t vcmi-android-build .
mkdir -p work
docker run --rm --user "$(id -u):$(id -g)" \
  -v "$PWD/vcmi:/src" -v "$PWD/work:/work" -v "$PWD/build.sh:/build.sh:ro" \
  vcmi-android-build bash /build.sh
```

The first run downloads VCMI's prebuilt Conan dependencies and the NDK
(a few GB in `work/`) and compiles for about 15 minutes on talos. The APK is
`vcmi/out/build/android-conan-ninja-release/android-build/vcmi-app/build/outputs/apk/release/vcmi-release.apk`;
install it with `nix run .#pocket-dmg -- install <apk>`.

Game data lives under `/sdcard/Android/data/is.xyz.vcmi.dmg/files/vcmi-data/`
(`Data`, `Maps`, `Mp3`, `Mods`, `config`, `Saves`).

## Controls

Play in the controller's mouse mode (toggle: L1+R1 held) with the patched
controller firmware from `../controller-fw`: the touchpad is the mouse
(click = left click) and every other control stays a gamepad input.
`input.enableTouch` is `false` in `config/settings.json`, so the game ignores
the touchscreen.

The device's `vcmi-data/config/keyBindingsConfig.json` differs from VCMI's
defaults in these keys (`joystickAxes` swaps the sticks so the stick scrolls
the map; the buttons fill the keyboard-only gaps, zoom and quick save):

```sh
jq '.joystickAxes = {"mouseCursorX":"rightx","mouseCursorY":"righty","mouseSwipeX":"leftx","mouseSwipeY":"lefty"}
  | .joystickButtons.adventureVisitObject = [] | .joystickButtons.adventureQuickSave = "lefttrigger"
  | .joystickButtons.adventureViewScenario = [] | .joystickButtons.adventureThievesGuild = []
  | .joystickButtons.adventureZoomOut = "dpleft" | .joystickButtons.adventureZoomIn = "dpright"'
```

| Control | Adventure map | Battle |
|---|---|---|
| Touchpad | Pointer; click = left click | same |
| Stick | Scroll the map | — |
| A / B | OK; open the selected hero / Cancel | OK / Cancel |
| X | Left click at the pointer | same |
| Y (hold) | Info popup (right click) | same |
| L1 / R1 | Next hero / next town | Defend / wait |
| L2 | Quick save | Creature ability; tactics: next unit |
| R2 | Spellbook | Spellbook |
| D-pad | Up: world view, Down: kingdom overview, Left/Right: zoom out/in; moves in lists | Up/Down: combat log |
| L3 | Surface / underground | Hero stats |
| SELECT / START | End turn / options | Auto-combat / options |

Tested on the device: touchpad, X, Y, A, B, L1/R1 and the D-pad's original
shortcuts. The rest come from the bindings file.

The thieves' guild (formerly D-pad Right) is in any owned town's tavern; open
the town from the town list without travelling. Scenario info is in the
options menu. Holding L1+R1 to toggle mouse mode also sends next hero and
next town, which is harmless.

The VCMI launcher (the screen with Play) is Qt 5.15, which ignores gamepad
buttons. Use mouse mode and click Play with the touchpad.
