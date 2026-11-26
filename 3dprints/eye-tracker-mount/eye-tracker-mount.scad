// Eye tracker mount: two clips, one either side of the ROG logo tab, each a plate taped to the
// flat back of the monitor's bottom edge with a ledge under the edge, joined by a bar below the
// tab. The bar carries two camera boards (one at each end) and a quarter-size Perma-Proto board
// (IR LEDs and the transistors that switch them) in the middle; the Pico that drives them sits
// flat on the back of the middle block, USB port down. Cameras and LED board face the user,
// tilted up; the cameras also turn in toward the middle.
//
// Modelled in use position: X across the monitor (+X to the user's right), Y front-to-back
// (the monitor's front face is y = 0, its back face y = panel_t, the user at -y), Z up (the
// monitor's bottom edge is z = 0). The final rotate() lays it on its back plates for printing.
include <BOSL2/std.scad>
$fa = 2; $fs = 0.4;

// --- Monitor ---
panel_t    = 19;     // monitor thickness at the bottom edge, front face to the flat back
tape_t     = 1.1;    // 3M VHB 5952
tab_clear  = 80;     // inner gap between the clips; the logo tab is ~53 mm wide at the top
tab_drop   = 26;     // top of the bar below the bottom edge; the tab hangs down ~20 mm at the back

// --- Clips, bar ---
pad_w      = 30;     // back plate width (X), from the clip's inner face outward
pad_h      = 30;     // back plate height above the bottom edge (the taped area is pad_w x pad_h)
plate_t    = 4;      // back plate thickness
ledge_t    = 4;      // ledge under the bottom edge, the full plate width
leg_w      = 12;     // leg from the ledge down to the bar, on the clip's inner side
bar_h      = 10;     // bar height (Z)
depth      = panel_t + tape_t + plate_t;   // front face to the back of the plates (print bed)

// --- Aim ---
cam_spacing = 200;   // camera centre to camera centre
tilt        = 18;    // up from horizontal: cameras and board look up at the eyes
toe         = 9.5;   // cameras turn in toward the middle: atan(100 / 600) for eyes at ~60 cm

// --- Camera boards (32 x 32, M2 holes on a 28 mm square) ---
cam_board  = 32;
cam_pitch  = 28;
cam_z      = -40;    // board centre height; its cable slot passes under the bar
face_off   = 12;     // board-face centre in front of the monitor's front face (-y)
cam_block  = 40;     // camera block face size
// USB connector on the back at one edge; mount that edge at the bottom. The slot covers the
// bottom of the board and runs straight back under the bar and out the back.
slot_w     = 20;     // slot width across the board
slot_top   = -3;     // slot runs from below the board's bottom edge up to this local y

// --- Perma-Proto quarter-size (43 x 50.8, two 3.2 mm holes 35.56 apart across the 43 mm width,
// on the centre line) ---
pp_size    = [43, 50.8];
pp_pitch   = 35.56;
pp_z       = -48;    // board centre height; keeps its top edge below the logo tab
pp_block   = [50, 58];

// --- Raspberry Pi Pico (51 x 21, M2 holes 11.4 x 47) flat on the back of the middle block ---
pico_holes = [11.4, 47];

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

// A wedge from the bed plane (y = depth) to the tilted face. back_z: its [bottom, top] at the
// bed; by default the face's own height, or narrower so it tapers from the face to the bed
// (keep the taper under 45° from the print direction).
module block(x, z, yaw, size, back_z) {
    bz = is_undef(back_z) ? [z - size[1] / 2, z + size[1] / 2] : back_z;
    hull() {
        translate([x - size[0] / 2, depth - 0.01, bz[0]]) cube([size[0], 0.01, bz[1] - bz[0]]);
        on_face(x, z, yaw) translate([-size[0] / 2, -size[1] / 2, -0.01]) cube([size[0], size[1], 0.01]);
    }
}

module frame() {
    for (s = [-1, 1]) {
        x0 = s > 0 ? tab_clear / 2 : -tab_clear / 2 - pad_w;
        // back plate: taped to the flat back, from the bar up past the bottom edge
        translate([x0, panel_t + tape_t, -tab_drop]) cube([pad_w, plate_t, tab_drop + pad_h]);
        // ledge under the bottom edge carries the weight; a leg joins it to the bar
        translate([x0, 0, -ledge_t]) cube([pad_w, depth, ledge_t]);
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
                // tapers from the camera face back to the bar
                block(s * cam_x, cam_z, -s * toe, [cam_block, cam_block + 2], [-tab_drop - bar_h, -tab_drop]);
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
        // Pico inserts, straight in from the back face
        for (x = [-1, 1], z = [-1, 1])
            translate([x * pico_holes[0] / 2, depth - m2_l - hole_extra, pp_z + z * pico_holes[1] / 2])
                rotate([-90, 0, 0]) cylinder(d = m2_d, h = m2_l + hole_extra + 0.01);
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
