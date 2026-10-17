---
name: openscad
description: Design 3D-printable parts in OpenSCAD (with BOSL2), rendering 3MF/STL and PNG previews to check the geometry. Use when asked to model, design, or CAD a part, bracket, enclosure, clip, mount, or anything to 3D print, or to edit a .scad file.
---

# OpenSCAD parts for 3D printing

`openscad` here is a 2026 development snapshot with the Manifold backend. BOSL2 is on
`OPENSCADPATH`, so `include <BOSL2/std.scad>` works anywhere. Renders take seconds and run
headless, PNG previews included.

## Writing the model

- One part per file: `<name>.scad` in the current directory. Units are millimetres.
- Put every dimension the user might change in named top-level variables at the top,
  with a comment for each. Derive the rest from those.
- Start with `include <BOSL2/std.scad>` and use BOSL2 wherever it helps: `cuboid(...,
  rounding=)`, `cyl(..., chamfer=)`, `diff()`/`tag("remove")`, attachments/anchors,
  `threaded_rod`/`screw_hole` from `BOSL2/screws.scad`. Plain OpenSCAD is fine for
  simple parts.
- Set smooth curves once at the top: `$fa = 2; $fs = 0.4;`. Do not set a global `$fn`.
- Sit the part on the bed: its lowest point at z = 0 (in BOSL2, `anchor=BOTTOM`), with
  the largest flat face down.
- Design for FDM unless told otherwise: overhangs at most 45° without support, bridges
  short, walls at least 1.2 mm, about 0.2 mm clearance per side for parts that fit
  together (0.3–0.4 mm for sliding fits), and holes for screws sized to the nominal
  diameter plus 0.2–0.3 mm. Say which way the part is meant to print.
- `echo()` the key derived dimensions (overall size, wall thickness, clearances) so they
  show up in the render output.

## Rendering and checking

Render the printable file and a geometry summary:

```sh
openscad --backend=manifold --summary=all --summary-file=<name>.summary.json -o <name>.3mf <name>.scad
```

Check the output for `WARNING`/`ERROR` lines and `ECHO` values. In the summary JSON,
`geometry.bounding_box` must match the intended size with `min[2]` = 0, and
`geometry.simple` must be `true` (a closed manifold). Export `.stl` as well only if the
user asks for it.

Then render previews and **look at them with Read** before reporting:

```sh
# isometric
openscad --backend=manifold --render --viewall --autocenter --imgsize=1024,768 -o <name>-iso.png <name>.scad
# top, front, right (orthographic): rotation 0,0,0 / 90,0,0 / 90,0,90
openscad --backend=manifold --render --viewall --autocenter --projection=o --camera=0,0,0,90,0,0,0 --imgsize=1024,768 -o <name>-front.png <name>.scad
```

Use the iso view plus whichever orthographic views show the features that matter
(holes, slots, mating faces). For a view from below, use rotation `180,0,0`. If a
preview shows something wrong, such as a missing cut, a floating piece, a feature on the
wrong face or bad proportions, fix it and render again. Don't report a part you have
not looked at.

## Handing off

Report the file paths, the overall dimensions, the print orientation, and anything the
user should check (fit-critical dimensions, any part that needs support). The printer is
skyforge, a Klipper machine whose gantry travels 335 × 320 × 235 mm; ask before
designing anything near that size. The user slices in OrcaSlicer with the "Skyforge"
preset (`orca-slicer <name>.3mf`). Never send anything to the printer yourself.
