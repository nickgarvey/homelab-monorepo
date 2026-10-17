// Open pentagonal tray for five sleeved commander decks, with a dice well in the middle.
// Print flat, bottom down; no supports.
include <BOSL2/std.scad>
$fa = 2; $fs = 0.4;

// --- Contents ---
sleeve_w   = 66.5;  // Dragon Shield matte sleeve, short edge (lies along the slot)
sleeve_h   = 92;    // Dragon Shield matte sleeve, long edge (stands vertical)
deck_thick = 72;    // 100 sleeved cards (~65) + 15 unsleeved tokens (~5), rounded up
slack      = 4;     // total extra room per slot in width and depth (loose fit)

// --- Body ---
wall       = 2.4;   // all walls
floor_t    = 2;     // floor under every pocket
wall_h     = 55;    // overall height; decks stand sleeve_h tall, so the rest sticks out to grab
corner_r   = 2;     // rounding of pocket corners
pocket_min = 1.5;   // drop pocket slivers thinner than this

// --- Finger notches (outer and inner wall of each slot) ---
notch_w     = 30;   // notch width
notch_depth = 30;   // notch depth from the top edge

// --- Derived ---
n      = 5;
slot_w = sleeve_w + slack;
slot_d = deck_thick + slack;
// Smallest inner apothem that leaves a full wall between neighbouring slots' inner corners.
a_in   = (slot_w / 2 + wall / 2) / tan(180 / n);
a_out  = a_in + slot_d;

module slot2d() { translate([-slot_w / 2, a_in]) square([slot_w, slot_d]); }
module slots2d() { for (i = [0 : n - 1]) rotate(i * 360 / n) slot2d(); }
module outline2d() { offset(r = wall) hull() slots2d(); }
module rounded(r) { offset(r = r) offset(delta = -r) children(); }

// Pockets: the deck slots, plus the center well and the wedges between slots,
// which are whatever is left of the inside once every slot has its wall.
module pockets2d() {
    rounded(corner_r) slots2d();
    rounded(corner_r) rounded(pocket_min / 2)
        difference() { offset(delta = -wall) outline2d(); offset(delta = wall) slots2d(); }
}

module notch_cut() {
    // U-shaped cut through a wall, centred on the radial axis
    r = notch_w / 2;
    zc = wall_h - notch_depth + r;
    translate([0, 0, zc]) {
        xrot(90) cyl(r = r, h = 4 * wall + 2, anchor = CENTER);
        cuboid([notch_w, 4 * wall + 2, notch_depth], anchor = BOTTOM);
    }
}

difference() {
    linear_extrude(wall_h) outline2d();
    translate([0, 0, floor_t]) linear_extrude(wall_h) pockets2d();
    for (i = [0 : n - 1]) rotate(i * 360 / n) {
        translate([0, a_out + wall / 2, 0]) notch_cut();  // outer wall
        translate([0, a_in - wall / 2, 0]) notch_cut();   // inner wall, into the dice well
    }
}

echo(slot = [slot_w, slot_d], a_in = a_in, a_out = a_out,
     deck_above_wall = sleeve_h - (wall_h - floor_t),
     well_across_flats = 2 * (a_in - wall));
