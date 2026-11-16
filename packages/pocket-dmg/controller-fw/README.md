# Pocket DMG controller firmware: touchpad mouse, gamepad buttons

The DMG's built-in controls (D-pad, buttons, stick, touchpad) are run by an
STM32 microcontroller with its own firmware (v09). In stock firmware,
keyboard/mouse mode turns the touchpad into a mouse but also turns the D-pad
into arrow keys, A/X/L3/R3 into mouse clicks, B/Y into Esc/Enter, L2/R2 into
Page Up/Down, and the stick into a second pointer.

`build.sh` patches the stock image so that mouse mode only changes the
touchpad:

| Control | Normal mode | Mouse mode (patched) |
|---|---|---|
| Touchpad move | right stick | mouse pointer |
| Touchpad click (R3) | R3 | left click |
| Everything else | gamepad | gamepad (unchanged) |

The mode toggle combo is set in AYANEO Settings → Controller → Keyboard &
Mouse Mode (currently L1+R1; set by serial `55 06 02 05 04`, saved by AYANEO
Settings in `/sdcard/.aya/aya_key_mouse_switch_quick_key.conf`).

## Patch header

- Upstream: AYANEO, closed source; image from
  `https://ayaneo-1305909189.cos.accelerate.myqcloud.com/android/AR02/ControllerFirmwareUpdate/dfu_update_b.txt`
- Upstreamable: no (closed firmware, no public tracker)
- How: binary patch, sources in `*.s`, assembled and spliced by `build.sh`
  (sha256 checks on input and output). Load address 0x0800C000.
  - `enter.s` / `exit.s` (toggle at 0x08015890): on entering mouse mode set
    only key-config entry 13 (R3, +0x8f in the table at 0x20000ea4) to mouse
    left, and re-centre the touch stick accumulator (0x200006a0/a4 =
    0x20000694/98); on leaving, clear the toggle flag and that entry. The
    other table rewrites are skipped.
  - `b1.s` + nop at 0x0801be04: the left stick keeps producing gamepad axes in
    mouse mode instead of mouse motion.
  - `c.s` (0x08016bc8): in mouse mode touch motion no longer accumulates into
    the right-stick values, so the right stick stays centred.
  - `cave.s` + `tramp.s` (0x08016e68 → 0x08015c34): touch mouse reports keep
    the left button held while the touchpad click is held. Stock alternates
    the button between the key handler's report and the touch report, which
    turns one click into a burst of clicks.
- Written: 2026-11-15, controller firmware v09
- Last checked: 2026-11-15, v09
- Remove when: AYANEO ships firmware where mouse mode leaves the buttons alone

AYANEO Settings offers a controller update when the server has a newer
version than the controller reports (`aya_android_handler_firmware_version_b.txt`,
currently `09`). Accepting one replaces this patch.

## Flashing

`dmgflash` (built into `.#pocket-dmg` as `share/pocket-dmg/dmgflash`) speaks
AYANEO's update protocol: serial frame `E7 00..07 07 ED` on `/dev/ttyHS4`
puts the MCU into its bootloader (USB HID 0483:5750), then 1402 packets of
58 bytes (`[sum16][len][2][index16][data]`), then quit (type 3). Root comes
from AYANEO's `/product/bin/xsu`. Keep the screen on while flashing: the
controller powers off when the screen sleeps.

```sh
./build.sh                                   # -> out/fw_v09_dmgsplit.bin
adb push out/fw_v09_dmgsplit.bin "$(nix build --print-out-paths .#pocket-dmg)/share/pocket-dmg/dmgflash" /data/local/tmp/
adb shell svc power stayon usb
adb shell 'xsu "/data/local/tmp/dmgflash /data/local/tmp/fw_v09_dmgsplit.bin"'
adb shell svc power stayon false
```

To go back to stock, flash `out/fw_v09_stock.bin` the same way. Recovery
needs the running firmware to accept the serial bootloader command; the patch
does not touch startup, the serial handler or the bootloader entry.

## Controller facts (fw v09)

- USB: gamepad 4001:0428 (16-byte report: X, Y, Z, RZ, hat, 15 buttons,
  brake, gas); 1C4F:0002 with "AYANEO USB MOUSE", "AYANEO USB KEYBOARD" and
  "AYANEO HID CUSTOM" (64-byte config protocol: type 6 GetKeyCfg, 7 SetKeyCfg
  with no index bounds check, 5 SaveAllCfg to flash 0x08040000).
- Serial `/dev/ttyHS4`, 115200: `E7 <8 bytes> <sum8> ED`. `55 0A NN` sets the
  input mode (1 gamepad, 2 mouse, 3 touch velocity-stick) until the next frame.
- Mode byte 0x2000086e; key-config table 0x20000ea4, 11 bytes per entry,
  byte 0 low nibble = type (0 default, 1 gamepad remap, 2 keyboard, 3 mouse).
- Touchpad: I2C 0x74, register 0x000D; touch-down point is the stick centre.
