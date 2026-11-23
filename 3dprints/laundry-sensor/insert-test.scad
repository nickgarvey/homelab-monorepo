// Heat-set insert test coupon: through-holes in 0.4 mm steps, each labelled with its diameter.
// Press an insert into each with the iron; the snug one sets insert_d in laundry-case.scad.
// Print flat; no supports.
include <BOSL2/std.scad>
$fa = 2; $fs = 0.2;

sizes   = [3.0, 3.4, 3.8];                  // hole diameters to try
pitch   = 10;                           // hole spacing
block_h = 5;                            // insert (4 mm) plus 1 mm
block_w = 12;

difference() {
    cuboid([pitch * len(sizes), block_w, block_h], rounding = 1, except = [TOP, BOTTOM], anchor = BOTTOM);
    for (i = idx(sizes)) {
        x = (i - (len(sizes) - 1) / 2) * pitch;
        translate([x, 2.2, -1]) cylinder(d = sizes[i], h = block_h + 2);
        translate([x, -3.6, block_h - 0.6]) linear_extrude(1)
            text(str(sizes[i]), size = 2.6, halign = "center", valign = "center");
    }
}
