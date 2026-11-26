# Eye tracker monitor mount

**Status: designed 2026-11-26, not yet printed.**

Replaces the desk stand (`3dprints/eye-tracker-stand/`) for the eye tracker (ait epic
nix-c9wua). One piece: two clips either side of the ROG logo tab of the monitor (ASUS XG27UQDMS), each a
30 x 30 mm plate taped to the flat back of the 19 mm bottom edge with a block under the edge,
joined by a bar below the tab carrying:
- two OV9281 camera boards, 200 mm apart, tilted up 18° and turned in 9.5° (eyes at ~60 cm);
- a half-size Perma-Proto board in the middle, tilted up 18°: four 850 nm IR LEDs, a Pico and
  the transistors that switch them.

`eye-tracker-mount.scad` -> `eye-tracker-mount.3mf`. 242 x 106 x 50 mm as printed.

- Print on the back plates (as exported); no supports. `print = false` shows it in use position.
- Attach: 3M VHB 5952 (1.1 mm; the plates sit that far off the back) on both plates. The blocks
  under the edge carry the weight; the tape keeps it from tipping. Clean the back with isopropyl
  alcohol, press 30 s, wait a day before loading it.
- Measured: bottom edge 19 mm thick, flat back from 2 mm above the edge. Tab from a ruler photo:
  ~53 mm wide at the top, ~12 mm drop at the front, ~20 mm at the back. Clips 80 mm apart, bar
  top 26 mm below the edge.
- Cameras: 4x M2*H4 inserts each (3.4 mm holes), M2 x 5-6 screws. Mount each board with its USB
  connector edge at the bottom: the cable slot covers the bottom of the board and runs straight
  back, under the bar.
- Perma-Proto: 2x M3 inserts (4.0 mm holes, 5 mm deep; check against the inserts), holes 73.7 mm
  apart on the board's long centre line. 6 mm standoffs clear the solder joints.
- Unverified until printed: tab clearance, pad depth, the M3 insert hole size, aim angles.
