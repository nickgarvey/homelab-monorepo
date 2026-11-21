// Pill bottle case insert: a plate with a U-shaped rim on both faces and rib slots in each end; prints standing on its rimmed long edge.
//
// The plate has a raised rim on both faces along one long edge and the two short
// ends; the other long edge is bare plate. Each end face has two slots, one either
// side of the plate, that clear ribs inside the case. Modelled lying flat
// (X = length, Y = width, Z = thickness), then stood on the rimmed edge, which
// needs no supports.
include <BOSL2/std.scad>

$fa = 2; $fs = 0.2;

// --- Overall size (fitted by test prints; the case holds it on length) ---
length      = 32.9;  // X: end face to end face
width       = 15.8;  // Y: rimmed edge to the free plate edge
thickness   = 6.5;   // Z: rim, outer face to outer face

// --- Plate and rim ---
plate_t     = 1.2;   // central plate thickness
wall        = 1.4;   // rim width (end walls and the long rim)
wall_len    = 11.7;  // how far the end walls run in from the rimmed edge
r_rim       = 1.5;   // outer corner radius at the rimmed edge
r_free      = 1.0;   // corner radius at the free (bare plate) edge
r_inner     = 1.5;   // inside fillet where end walls meet the long rim

// --- Rib slots in each end face ---
slot_gap    = 0.1;   // plate face to the inner side of each slot
slot_w      = 1.2;   // slot width
slot_d      = 0.6;   // slot depth
slot_chamf  = 0.3;   // 45° lead-in chamfer at the slot mouth

echo(size = [length, width, thickness], plate_t = plate_t, wall = wall,
     wall_len = wall_len, free_plate = width - wall_len,
     slot = [slot_w, slot_d], wall_behind_slot = wall - slot_d);

module outline() {
    // BOSL2 rect rounding order: [X+Y+, X-Y+, X-Y-, X+Y-]; Y- is the rimmed edge.
    rect([length, width], rounding = [r_free, r_free, r_rim, r_rim],
         anchor = FRONT + LEFT);
}

module rim2d() {
    difference() {
        intersection() {
            outline();
            square([length, wall_len]);
        }
        translate([wall, wall])
            rect([length - 2 * wall, width],
                 rounding = [0, 0, r_inner, r_inner], anchor = FRONT + LEFT);
    }
}

// Slot cross-section in the X-Z plane at the x = 0 end face, opening toward -X:
// a rectangle w wide and d deep with a 45° chamfer at the mouth.
module slot2d(w, d, zc) {
    c = slot_chamf;
    polygon([[-1, zc - w / 2 - c - 1], [0, zc - w / 2 - c], [c, zc - w / 2],
             [d, zc - w / 2], [d, zc + w / 2], [c, zc + w / 2],
             [0, zc + w / 2 + c], [-1, zc + w / 2 + c + 1]]);
}

module insert() {
    zc = plate_t / 2 + slot_gap + slot_w / 2;
    difference() {
        union() {
            linear_extrude(plate_t, center = true) outline();
            linear_extrude(thickness, center = true) rim2d();
        }
        // Slots run along Y across the full height of each end wall.
        for (end = [0, 1], s = [-1, 1])
            translate([end * length, 0, 0]) mirror([end, 0, 0])
                translate([0, -1, 0]) rotate([-90, 0, 0])
                    linear_extrude(wall_len + 2)
                        mirror([0, 1, 0]) slot2d(slot_w, slot_d, s * zc);
    }
}

// Rimmed long edge (Y = 0) down on the bed.
rotate([90, 0, 0]) insert();
