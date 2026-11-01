// Open tray holding 5 deck boxes upright in a 3 + 2 grid; the sixth spot is an open compartment for dice.
include <BOSL2/std.scad>
$fa = 2; $fs = 0.4;

cols       = 3;     // boxes per row
rows       = 2;     // rows of boxes; the back-right spot is the dice compartment
box_row    = 64;    // box side running along a row (mm)
box_depth  = 71;    // box side running across the rows (mm)
clearance  = 0.5;   // gap per side around each box
wall       = 1.6;   // side wall thickness
floor_t    = 2.4;    // floor thickness
wall_h     = 30;    // wall height above the floor
groove_w   = 2;     // width of the floor grooves marking each box spot
groove_d   = 1;     // depth of the floor grooves

inner = [cols*box_row + 2*clearance, rows*box_depth + 2*clearance];
outer = [inner.x + 2*wall, inner.y + 2*wall, floor_t + wall_h];

// front-left corner of the compartment: one clearance past the neighbouring boxes
filler_min = [-inner.x/2 + 2*clearance + (cols-1)*box_row,
              -inner.y/2 + 2*clearance + (rows-1)*box_depth];
filler_size = [inner.x/2 - filler_min.x, inner.y/2 - filler_min.y];

echo(inner=inner, outer=outer, compartment_inside=filler_size - [wall, wall]);

difference() {
    cuboid(outer, anchor=BOTTOM);
    up(floor_t) cuboid([inner.x, inner.y, wall_h + 1], anchor=BOTTOM);
    // grooves along the boundaries between box spots
    for (c = [1:cols-1])
        right(-inner.x/2 + clearance + c*box_row)
            up(floor_t - groove_d) cuboid([groove_w, inner.y, groove_d + 1], anchor=BOTTOM);
    for (r = [1:rows-1])
        back(-inner.y/2 + clearance + r*box_depth)
            up(floor_t - groove_d) cuboid([inner.x, groove_w, groove_d + 1], anchor=BOTTOM);
}
// compartment: two walls facing the boxes, fused with the outer walls and floor
translate([filler_min.x, filler_min.y, 0])
    difference() {
        cube([filler_size.x + wall/2, filler_size.y + wall/2, outer.z]);
        translate([wall, wall, floor_t]) cube([filler_size.x, filler_size.y, outer.z]);
    }
