// Eye tracker mount: an upside-down U taped to the flat underside of the monitor either side of
// the ROG logo tab, with a bar under the tab carrying two camera boards (one at each end) and a
// half-size Perma-Proto board (IR LEDs, Pico, transistors) in the middle. Cameras and board
// face the user, tilted up; the cameras also turn in toward the middle.
//
// Modelled in use position: X across the monitor (+X to the user's right), Y front-to-back
// (the monitor's front face is y = 0, the monitor is at +y, the user at -y), Z up (the
// monitor's underside is z = 0). The final rotate() lays it back-face down for printing.
include <BOSL2/std.scad>
$fa = 2; $fs = 0.4;

// --- Monitor ---
tab_clear  = 80;     // inner gap between the legs; the logo tab is ~53 mm wide at the top
tab_drop   = 20;     // top of the bar below the underside; the tab hangs down ~12 mm
depth      = 25;     // pad and bar depth, front-to-back, from the monitor's front face

// --- Pads, legs, bar ---
pad_w      = 30;     // pad width (X) on the underside, from the leg's inner face outward
pad_t      = 4;      // pad thickness
leg_w      = 12;     // leg width (X)
bar_h      = 10;     // bar height (Z)

// --- Aim ---
cam_spacing = 200;   // camera centre to camera centre
tilt        = 18;    // up from horizontal: cameras and board look up at the eyes
toe         = 9.5;   // cameras turn in toward the middle: atan(100 / 600) for eyes at ~60 cm

// --- Camera boards (32 x 32, M2 holes on a 28 mm square) ---
cam_board  = 32;
cam_pitch  = 28;
cam_z      = -26;    // board centre height
face_off   = 12;     // board-face centre in front of the monitor's front face (-y)
cam_block  = 40;     // camera block face size
// USB connector on the back at one edge; mount that edge at the bottom. The slot covers the
// bottom of the board and runs straight back under the bar and out the back.
slot_w     = 20;     // slot width across the board
slot_top   = -3;     // slot runs from below the board's bottom edge up to this local y

// --- Perma-Proto half-size (81 x 51, two 3.2 mm holes 73.7 apart on the long centre line) ---
pp_size    = [81, 51];
pp_pitch   = 73.7;
pp_z       = -48;    // board centre height; keeps its top edge below the logo tab
pp_block   = [88, 56];

// --- Inserts ---
m2_d = 3.4; m2_l = 4;         // M2*H4 (same as 3dprints/laundry-sensor)
m3_d = 4.0; m3_l = 5;         // M3 heat-set insert hole; check against your inserts
hole_extra = 1.5;             // hole depth past the insert for the screw tip
standoff_d = 7;               // camera standoffs
pp_standoff_d = 8;            // Perma-Proto standoffs
standoff_h = 6;               // gap behind the boards for solder joints and the USB plug

cam_x = cam_spacing / 2;
bar_half = cam_x + cam_block / 2;

// Frame on a board face: local z is the face normal (toward the user and up), local x is
// across, local y is up the face.
module on_face(x, z, yaw)
    translate([x, -face_off, z]) rotate([0, 0, yaw]) rotate([-tilt, 0, 0]) rotate([90, 0, 0]) children();

// A wedge from the bed plane (y = depth) to the tilted face.
module block(x, z, yaw, size) {
    hull() {
        translate([x - size[0] / 2, depth - 0.01, z - size[1] / 2]) cube([size[0], 0.01, size[1]]);
        on_face(x, z, yaw) translate([-size[0] / 2, -size[1] / 2, -0.01]) cube([size[0], size[1], 0.01]);
    }
}

module frame() {
    for (s = [-1, 1]) {
        // pad on the underside
        translate([s > 0 ? tab_clear / 2 : -tab_clear / 2 - pad_w, 0, -pad_t]) cube([pad_w, depth, pad_t]);
        // leg
        translate([s > 0 ? tab_clear / 2 : -tab_clear / 2 - leg_w, 0, -tab_drop]) cube([leg_w, depth, tab_drop]);
    }
    translate([-bar_half, 0, -tab_drop - bar_h]) cube([2 * bar_half, depth, bar_h]);
}

module standoffs(pts, d) {
    for (p = pts) translate([p[0], p[1], -1]) cylinder(d = d, h = standoff_h + 1);
}
module insert_holes(pts, d, l) {
    for (p = pts) translate([p[0], p[1], standoff_h - l - hole_extra]) cylinder(d = d, h = l + hole_extra + 0.01);
}

cam_pts = [for (x = [-1, 1], y = [-1, 1]) [x * cam_pitch / 2, y * cam_pitch / 2]];
pp_pts  = [[-pp_pitch / 2, 0], [pp_pitch / 2, 0]];

module mount() {
    difference() {
        union() {
            frame();
            for (s = [-1, 1]) {
                block(s * cam_x, cam_z, -s * toe, [cam_block, cam_block + 2]);
                on_face(s * cam_x, cam_z, -s * toe) standoffs(cam_pts, standoff_d);
            }
            block(0, pp_z, 0, pp_block);
            on_face(0, pp_z, 0) standoffs(pp_pts, pp_standoff_d);
        }
        for (s = [-1, 1]) on_face(s * cam_x, cam_z, -s * toe) {
            insert_holes(cam_pts, m2_d, m2_l);
            // cable slot: bottom of the board, straight back along the face normal
            translate([-slot_w / 2, -cam_block / 2 - 2, -100]) cube([slot_w, cam_block / 2 + 2 + slot_top, 100 + standoff_h]);
        }
        on_face(0, pp_z, 0) insert_holes(pp_pts, m3_d, m3_l);
    }
}

// Print on the back face (y = depth): the U and bar are prisms up from the bed and the board
// faces point up.
print = true;   // false: show it in use position (for previews)
if (print) translate([0, 0, depth]) rotate([-90, 0, 0]) mount();
else mount();

echo(overall_width = 2 * bar_half, cam_spacing = cam_spacing, tilt = tilt, toe = toe,
     tab_gap = tab_clear, bar_top_below_underside = tab_drop,
     cam_centre_below_underside = -cam_z, pp_centre_below_underside = -pp_z,
     pp_top_edge_z = pp_z + pp_size[1] / 2 * cos(tilt));
