// Temporary desk stand for the 32 x 32 mm OV9281 USB camera board: the board faces up and
// toward -X at 45°, its centre 3.5" above the table. Four M2 screws into heat-set inserts.
// Print base down; no supports.
include <BOSL2/std.scad>
$fa = 2; $fs = 0.4;

// --- Placement ---
board_center_h = 88.9;   // height of the board's front-face centre above the table (3.5")
tilt           = 45;     // board angle from vertical; the camera looks up at this angle

// --- Camera board (from its drawing) ---
board      = 32;         // board is board x board mm
hole_pitch = 28;         // M2 holes on a hole_pitch square
pcb_t      = 1.6;        // board thickness

// --- Inserts (same as 3dprints/laundry-sensor: M2*H4, 3.4 mm holes from its insert-test) ---
insert_d   = 3.4;        // insert hole diameter
insert_l   = 4;          // insert length
hole_extra = 1.5;        // hole depth past the insert for the screw tip
standoff_d = 7;          // standoff diameter
standoff_h = 6;          // gap behind the board for its back-side parts and cable plug

// --- Body ---
face_len = 40;           // length of the sloped face along the slope (board + margin)
width    = 40;           // tower width (Y)
base     = [70, 60, 4];  // base plate X, Y, thickness

n  = [-sin(tilt), 0, cos(tilt)];   // face normal: up and toward -X
u  = [ cos(tilt), 0, sin(tilt)];   // up the slope, toward +X
// Board front face is standoff_h + pcb_t out along n from the face centre.
C  = [0, 0, board_center_h] - (standoff_h + pcb_t) * n;
A  = C - face_len / 2 * u;         // low end of the face
B  = C + face_len / 2 * u;         // high end of the face

echo(face_center = C, face_low_z = A[2], face_high_z = B[2], overall_h = B[2]);
echo(board_front_center = C + (standoff_h + pcb_t) * n);

// Profile in XZ, extruded across Y.
module tower()
    rotate([90, 0, 0]) linear_extrude(width, center = true)
        polygon([[A[0], 0], [B[0], 0], [B[0], B[2]], [A[0], A[2]]]);

// Local frame on the face: z along the normal, x up the slope.
module on_face() translate(C) rotate([0, -tilt, 0]) children();

difference() {
    union() {
        cuboid(base, rounding = 3, edges = "Z", anchor = BOTTOM);
        tower();
        on_face() for (x = [-1, 1], y = [-1, 1])
            translate([x * hole_pitch / 2, y * hole_pitch / 2, -1]) cylinder(d = standoff_d, h = standoff_h + 1);
    }
    on_face() for (x = [-1, 1], y = [-1, 1])
        translate([x * hole_pitch / 2, y * hole_pitch / 2, standoff_h - insert_l - hole_extra])
            cylinder(d = insert_d, h = insert_l + hole_extra + 0.01);
}
