# Eye tracker monitor mount

**Status: designed 2026-11-26, not yet printed.**

Replaces the desk stand (`3dprints/eye-tracker-stand/`) for the eye tracker (ait epic
nix-c9wua). One piece: an upside-down U taped to the flat underside of the monitor (ASUS
XG27UQDMS) either side of the ROG logo tab, with a bar under the tab carrying:
- two OV9281 camera boards, 200 mm apart, tilted up 18° and turned in 9.5° (eyes at ~60 cm);
- a half-size Perma-Proto board in the middle, tilted up 18°: four 850 nm IR LEDs, a Pico and
  the transistors that switch them.

`eye-tracker-mount.scad` -> `eye-tracker-mount.3mf`. 242 x 76 x 50 mm as printed.

- Print on its back face (as exported); no supports. `print = false` shows it in use position.
- Attach: 3M VHB 5952 on both 30 x 25 mm pads. Clean the underside with isopropyl alcohol, press
  30 s, wait a day before loading it.
- Tab clearance from a ruler photo (tab ~53 mm wide at the top, ~12 mm drop): legs 80 mm apart,
  bar top 20 mm below the underside. Pads assume the underside is flat 25 mm back from the
  front face.
- Cameras: 4x M2*H4 inserts each (3.4 mm holes), M2 x 5-6 screws. Mount each board with its USB
  connector edge at the bottom: the cable slot covers the bottom of the board and runs straight
  back under the bar.
- Perma-Proto: 2x M3 inserts (4.0 mm holes, 5 mm deep; check against the inserts), holes 73.7 mm
  apart on the board's long centre line. 6 mm standoffs clear the solder joints.
- Unverified until printed: tab clearance, pad depth, the M3 insert hole size, aim angles.
