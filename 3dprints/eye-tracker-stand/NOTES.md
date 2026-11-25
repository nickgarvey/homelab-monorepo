# Eye tracker camera stand (temporary)

**Status: designed 2026-11-24, not yet printed.**

Desk stand for the 32 x 32 mm OV9281 USB camera board (ait epic nix-c9wua). The board faces up
and toward the front (-X) at 45°, with the centre of its front face 88.9 mm (3.5") above the table.
`camera-stand.scad` -> `camera-stand.3mf`. Outer 70 x 60 x 100.1 mm.

- Print base down; no supports. The sloped face and the standoffs are at 45°.
- Hardware: 4x M2 heat-set inserts (M2*H4, 3.4 mm holes, the size from
  3dprints/laundry-sensor/insert-test) and 4x M2 screws. Screw length: board 1.6 mm + insert 4 mm,
  so M2 x 5 or x 6.
- The standoffs leave a 6 mm gap behind the board for its back-side parts and the cable plug
  (`standoff_h`). Unverified until the board is on it.
