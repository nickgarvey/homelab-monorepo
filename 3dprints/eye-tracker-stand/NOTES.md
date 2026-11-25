# Eye tracker camera stand (temporary)

**Status: designed 2026-11-24, not yet printed.**

Desk stand for the 32 x 32 mm OV9281 USB camera board (ait epic nix-c9wua). The board faces up
and toward the front (-X) at 45°, with the centre of its front face 88.9 mm (3.5") above the table.
`camera-stand.scad` -> `camera-stand.3mf`. Outer 70 x 60 x 100.1 mm.

- Print base down; no supports. The sloped face and the standoffs are at 45°.
- Hardware: 4x M2 heat-set inserts (M2*H4, 3.4 mm holes, the size from
  3dprints/laundry-sensor/insert-test) and 4x M2 screws. Screw length: board 1.6 mm + insert 4 mm,
  so M2 x 5 or x 6.
- USB cable: the board's cable plugs into a 5-pin 1.25 mm connector on its back, at one edge next
  to the 5V/DM/DP/G/G pads, with the wires leaving straight out the back (inno-maker U20CAM-9281M
  datasheet, section 2.5). A 20 mm slot (`slot_w`) runs the length of the face between the two
  standoff pairs and straight back out of the rear of the tower. Thread the loose end of the cable
  through the slot from the back, plug it into the board, then screw the board down. Mount the board
  with the connector edge at the top or bottom of the slope so the plug lands in the slot.
- The standoffs leave a 6 mm gap behind the board for its back-side parts (`standoff_h`).
  Unverified until the board is on it.
