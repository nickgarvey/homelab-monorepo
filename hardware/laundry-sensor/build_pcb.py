# Builds laundry-sensor.kicad_pcb from circuit.py: loads footprints, assigns nets, places parts,
# draws the outline. Placement only; routing is a separate step. Run in `nix develop .#pcb`.
#
# Board layout (top view, mm, y down). The 2xAA holder is on the back and fills x -2.9..57.2.
# x < -3: USB-C and the PPK2 header (their through-hole legs cannot sit over the holder).
# x > 57.5: the module's antenna, with no battery metal under it and no copper on the board.
import builtins
import os
from pathlib import Path

import pcbnew

import circuit  # noqa: F401  (builds the circuit)

HERE = Path(__file__).parent
KICAD_FP = Path(os.environ["KICAD9_FOOTPRINT_DIR"])
LOCAL_FP = {"laundry": HERE / "footprints" / "laundry.pretty"}

BOARD = (-12.0, -10.5, 63.0, 25.5)  # left, top, right, bottom

# ref: (x, y, rotation_deg) on the top side. Rotation is KiCad's (counter-clockwise positive).
PLACE = {
    "U2": (52.0, 7.5, -90),    # module, antenna toward +x, antenna edge at the board edge
    "J1": (-8.3, 7.5, -90),    # USB-C, mouth at the left edge
    "J2": (-6.0, 15.5, 0),     # PPK2 jumper
    # power
    "Q1": (3.5, 19.5, 0),
    "R1": (3.5, 22.5, 0),
    "D1": (2.5, 4.0, 0),
    "U1": (9.0, 11.0, 90),   # VIN/LX1/GND/LX2/VOUT row faces up, toward L1
    # module support
    "C5": (55.5, -1.8, 0),
    "C6": (54.2, 0.0, 0),
    "R7": (49.0, -1.0, 90),
    "C7": (47.6, -1.0, 90),
    "R5": (51.0, -3.0, 0),
    "R6": (51.0, -1.6, 0),
    "C4": (52.8, -1.6, 90),
    "R8": (44.0, 10.5, 90),
    # accelerometer
    "U3": (34.0, 7.5, 0),
    "C8": (31.2, 6.5, 90),
    "C9": (31.2, 8.5, 90),
    "R13": (37.0, 5.0, 90),
    # humidity sensor
    "U4": (20.0, -6.0, 0),
    "C10": (20.0, -3.8, 0),
    "R14": (17.5, -6.6, 90),
    "R15": (22.5, -6.6, 90),
    # USB CC
    "R11": (-1.8, 5.0, 90),
    "R12": (-1.8, 10.0, 90),
    # buttons, LED
    "SW1": (12.0, 22.6, 0),
    "SW2": (20.0, 22.6, 0),
    "SW3": (38.0, 22.6, 0),
    "R9": (41.8, 22.6, 90),
    "D2": (45.0, 23.0, 0),
    "R10": (45.0, 21.2, 0),
    # test pads
    "TP1": (30.0, -8.0, 0),
    "TP2": (33.0, -8.0, 0),
    "TP3": (36.0, -8.0, 0),
    "TP4": (39.0, -8.0, 0),
    "TP5": (42.0, -8.0, 0),
}
HOLDER_PLUS = (0.0, 15.0)  # BT1 pad 1 (+); the holder is flipped onto the back
HOLES = [(-9.4, -7.9), (-9.4, 22.9)]


def mm(x, y):
    return pcbnew.VECTOR2I(pcbnew.FromMM(x), pcbnew.FromMM(y))


def load_fp(fpid):
    lib, name = fpid.split(":")
    path = LOCAL_FP.get(lib) or KICAD_FP / f"{lib}.pretty"
    fp = pcbnew.FootprintLoad(str(path), name)
    if fp is None:
        raise SystemExit(f"footprint not found: {fpid}")
    fp.SetFPID(pcbnew.LIB_ID(lib, name))
    return fp


def setup_rules(board):
    # JLCPCB 2-layer capabilities, with margin.
    ds = board.GetDesignSettings()
    ds.m_TrackMinWidth = pcbnew.FromMM(0.15)
    ds.m_MinClearance = pcbnew.FromMM(0.15)
    ds.m_ViasMinSize = pcbnew.FromMM(0.6)
    ds.m_MinThroughDrill = pcbnew.FromMM(0.3)
    ds.m_CopperEdgeClearance = pcbnew.FromMM(0.3)
    ds.m_HoleClearance = pcbnew.FromMM(0.25)
    ds.SetCopperLayerCount(2)


def pad_xy(fp, num):
    p = next(p for p in fp.Pads() if p.GetNumber() == num)
    return pcbnew.ToMM(p.GetPosition().x), pcbnew.ToMM(p.GetPosition().y)


def put(board, ref, x, y, rot, first_pad_toward=None):
    """Place ref at (x, y). With first_pad_toward=(dx, dy), turn it 180 degrees if pad 1 points away."""
    fp = board.FindFootprintByReference(ref)
    fp.SetOrientationDegrees(rot)
    fp.SetPosition(mm(x, y))
    if first_pad_toward:
        px, py = pad_xy(fp, "1")
        dx, dy = first_pad_toward
        if (px - x) * dx + (py - y) * dy < 0:
            fp.SetOrientationDegrees(rot + 180)


def place_power_stage(board):
    """TPS63900 support parts, after TI's layout example (datasheet Figure 10-1): L1 straight above
    the VIN/LX1/GND/LX2/VOUT pin row, C1 (input) on the VIN end, C3 (output) on the VOUT end."""
    u1 = board.FindFootprintByReference("U1")
    vin, lx1, lx2, vout = (pad_xy(u1, n) for n in ("10", "9", "7", "6"))
    row_y = vin[1]
    up = -1.0 if row_y < pcbnew.ToMM(u1.GetPosition().y) else 1.0  # direction the row faces
    lx_x = (lx1[0] + lx2[0]) / 2
    put(board, "L1", lx_x, row_y + up * 2.5, 0, first_pad_toward=(lx1[0] - lx_x, 0))
    side_vin = 1.0 if vin[0] > vout[0] else -1.0
    put(board, "C1", vin[0] + side_vin * 1.9, row_y - up * 0.6, 90, first_pad_toward=(0, up))
    put(board, "C3", vout[0] - side_vin * 1.9, row_y - up * 0.6, 90, first_pad_toward=(0, up))
    put(board, "C2", vin[0] + side_vin * 3.9, row_y - up * 0.6, 90, first_pad_toward=(0, up))
    # CFG resistors under the opposite pin row (EN, SEL, CFG1-3).
    # Fanned out to 1.3 mm pitch: the pins are on 0.5 mm.
    cx, cy = pad_xy(u1, "4")
    step = 1.3 if pad_xy(u1, "5")[0] > cx else -1.3
    for i, ref in enumerate(("R2", "R3", "R4")):
        put(board, ref, cx + (i - 1) * step, cy - up * 2.3, 90, first_pad_toward=(0, up))


def tidy_silk(board):
    """Hide the LCSC field everywhere and reference designators on small passives (JLC assembles
    from the CPL, not the silkscreen). Buttons and test pads show their function instead."""
    for fp in board.GetFootprints():
        if fp.HasField("LCSC"):
            fp.GetField("LCSC").SetVisible(False)
        ref = fp.GetReference()
        prefix = ref.rstrip("0123456789")
        if prefix in ("R", "C", "L", "D", "Q", "H") or ref in ("U1", "BT1"):
            fp.Reference().SetVisible(False)
        if prefix in ("SW", "TP"):
            fp.Reference().SetVisible(False)
            v = fp.Value()
            v.SetLayer(pcbnew.F_SilkS)
            v.SetTextSize(pcbnew.VECTOR2I(pcbnew.FromMM(0.8), pcbnew.FromMM(0.8)))
            v.SetTextThickness(pcbnew.FromMM(0.12))
            if prefix == "SW":  # label above the button, clear of the board edge
                v.SetPosition(fp.GetPosition() + pcbnew.VECTOR2I(0, pcbnew.FromMM(-2.6)))
        for t in (fp.Reference(),):
            t.SetTextSize(pcbnew.VECTOR2I(pcbnew.FromMM(0.8), pcbnew.FromMM(0.8)))
            t.SetTextThickness(pcbnew.FromMM(0.12))


def outline(board):
    l, t, r, b = BOARD
    pts = [(l, t), (r, t), (r, b), (l, b)]
    for (x1, y1), (x2, y2) in zip(pts, pts[1:] + pts[:1]):
        seg = pcbnew.PCB_SHAPE(board)
        seg.SetShape(pcbnew.SHAPE_T_SEGMENT)
        seg.SetStart(mm(x1, y1))
        seg.SetEnd(mm(x2, y2))
        seg.SetLayer(pcbnew.Edge_Cuts)
        seg.SetWidth(pcbnew.FromMM(0.1))
        board.Add(seg)


def main():
    board = pcbnew.CreateEmptyBoard()
    setup_rules(board)
    nets = {}

    def net(name):
        if name not in nets:
            nets[name] = pcbnew.NETINFO_ITEM(board, name)
            board.Add(nets[name])
        return nets[name]

    for part in builtins.default_circuit.parts:
        fp = load_fp(part.footprint)
        fp.SetReference(part.ref)
        fp.SetValue(str(part.value))
        if "LCSC" in part.fields:
            fp.SetField("LCSC", part.fields["LCSC"])
        pin_net = {}
        for pin in part.pins:
            for n in pin.nets:
                if not n.name.startswith("__NOCONNECT"):
                    pin_net[str(pin.num)] = n.name
        for pad in fp.Pads():
            if pad.GetNumber() in pin_net:
                pad.SetNet(net(pin_net[pad.GetNumber()]))
        board.Add(fp)

        if part.ref in ("L1", "C1", "C2", "C3", "R2", "R3", "R4"):
            pass  # placed relative to U1 below
        elif part.ref == "BT1":
            fp.Flip(fp.GetPosition(), pcbnew.FLIP_DIRECTION_TOP_BOTTOM)
            fp.SetPosition(mm(*HOLDER_PLUS))
        elif part.ref in PLACE:
            x, y, rot = PLACE[part.ref]
            fp.SetOrientationDegrees(rot)
            fp.SetPosition(mm(x, y))
        else:
            print("unplaced:", part.ref)

    for i, (x, y) in enumerate(HOLES, 1):
        h = load_fp("MountingHole:MountingHole_2.2mm_M2")
        h.SetReference(f"H{i}")
        h.SetPosition(mm(x, y))
        board.Add(h)

    place_power_stage(board)
    tidy_silk(board)
    outline(board)
    out = HERE / "laundry-sensor.kicad_pcb"
    board.Save(str(out))
    print("wrote", out)


if __name__ == "__main__":
    main()
