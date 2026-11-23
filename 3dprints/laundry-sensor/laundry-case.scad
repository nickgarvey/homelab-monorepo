// Open tray for the laundry sensor board (hardware/laundry-sensor): magnets in the outer face, the
// 2xAA holder on the floor, the board on top with its component side exposed. Two M2 screws through
// the board's mounting holes hold the left end; lips on the right wall hold the right end's corners.
// Coordinates follow the KiCad board with y flipped: X = board x, Y = -board y, in mm.
// Print floor down (magnet pockets on the bed); no supports.
include <BOSL2/std.scad>
$fa = 2; $fs = 0.4;

// --- Board (from hardware/laundry-sensor/build_pcb.py) ---
board_x0 = -12; board_x1 = 75;       // left (USB-C) and right (antenna) edges
board_y0 = -25.5; board_y1 = 11.5;
pcb_t    = 1.6;
holes    = [[-9.4, 7.9], [-9.4, -22.9]];   // H1, H2 (M2)
module_y = [-14.1, -0.9];            // ESP32-C6 module's Y span at the right edge (no lip there)
usb      = [-7.5, 12.5, 6.8];        // USB-C opening in the left wall: centre Y, width, height
usb_zc   = 1.6;                      // opening centre above the board top

// --- Battery holder (Keystone 2462, on the board's back) ---
holder_h   = 16.0;   // height below the board (Keystone sheet: 2462 is 59.5 x 32.9 x 16.0)
holder_gap = 0.3;    // room between the holder and the floor

// --- Tray ---
wall      = 2.0;     // side walls
floor_t   = 3.6;     // floor (magnet pockets come out of this)
clr       = 0.3;     // board edge to inner wall
right_gap = 1.0;     // board right edge to inner wall: the board goes in tilted, right end first
rim       = 1.0;     // walls stand this far above the board top
lip       = 1.5;     // how far the right-end lips overhang the board
lip_t     = 1.2;     // lip thickness
corner_r  = 3;       // outer corner rounding

// --- Magnets (disc neodymium, glued into pockets in the outer face) ---
mag_d   = 10;        // diameter
mag_t   = 3;         // thickness
mag_clr = 0.15;      // per side
mag_xy  = [[3, 3], [51, 3], [3, -17], [51, -17]];   // all under the cells, clear of the antenna

// --- Screws: M2 x 6 into M2 x 4 mm heat-set inserts in the standoffs ---
insert_d   = 3.4;   // insert hole: from insert-test.3mf, M2*H4 inserts force in cold at 3.4
insert_l   = 4;     // insert length (kit label M2*H4)
hole_extra = 1.5;   // hole depth past the insert, for the screw tip and displaced plastic
standoff_d = 7.0;   // leaves 1.9 mm of plastic around the insert

// --- Derived ---
z_board_bot = floor_t + holder_h + holder_gap;
z_board_top = z_board_bot + pcb_t;
lip_z       = z_board_top + 0.15;              // lip underside, just above the board
height      = max(z_board_top + rim, lip_z + lip_t);
inner_x0 = board_x0 - clr; inner_x1 = board_x1 + right_gap;
inner_y0 = board_y0 - clr; inner_y1 = board_y1 + clr;

echo(outer = [inner_x1 - inner_x0 + 2 * wall, inner_y1 - inner_y0 + 2 * wall, height],
     z_board_bot = z_board_bot, lip_overlap = lip, slide_room = right_gap);

module inner2d() { translate([inner_x0, inner_y0]) square([inner_x1 - inner_x0, inner_y1 - inner_y0]); }
module outer2d() { offset(r = corner_r) offset(delta = wall - corner_r) inner2d(); }

module tray() {
    difference() {
        intersection() {
            linear_extrude(height) outer2d();
            union() {
                difference() {
                    linear_extrude(height) outer2d();
                    translate([0, 0, floor_t]) linear_extrude(height) inner2d();
                }
                // standoffs under H1/H2, braced into the left wall and the nearest long wall
                for (h = holes) hull() {
                    translate([h[0], h[1], 0]) cylinder(d = standoff_d, h = z_board_bot);
                    translate([inner_x0 - 1, h[1] > 0 ? inner_y1 : inner_y0 - 1, 0]) cube([1, 1, z_board_bot]);
                }
                // rib under the board's right end (behind the module; nothing on the back there)
                translate([62, inner_y0, 0]) cube([2, inner_y1 - inner_y0, z_board_bot]);
                // lips over the board's right corners, either side of the module
                for (seg = [[inner_y0, module_y[0] - 0.5], [module_y[1] + 0.5, inner_y1]])
                    translate([board_x1 - lip, seg[0], lip_z]) cube([inner_x1 - board_x1 + lip + 0.01, seg[1] - seg[0], lip_t]);
            }
        }
        for (h = holes) translate([h[0], h[1], z_board_bot - insert_l - hole_extra]) cylinder(d = insert_d, h = height);
        for (m = mag_xy) translate([m[0], m[1], -0.01]) cylinder(d = mag_d + 2 * mag_clr, h = mag_t + 0.1);
        translate([inner_x0 - wall - 1, usb[0] - usb[1] / 2, z_board_top + usb_zc - usb[2] / 2])
            cube([wall + 2, usb[1], height]);
    }
}

tray();
