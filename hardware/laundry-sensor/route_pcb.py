# Routes laundry-sensor.kicad_pcb in place: net classes, hand-placed power-stage traces,
# Freerouting for everything else, then ground pours on both layers. Run after build_pcb.py,
# in `nix develop .#pcb`.
import subprocess
import tempfile
from pathlib import Path

import pcbnew

HERE = Path(__file__).parent
PCB = HERE / "laundry-sensor.kicad_pcb"
BOARD = (-12.0, -11.5, 75.0, 25.5)
ANTENNA_X = 69.6  # the module's antenna keepout starts here
POWER_NETS = ("VIN", "VBAT", "BAT+", "VBUS")
# Items created here stay referenced for the whole run: a SWIG wrapper that goes out of scope can
# free an object the board still points at.
KEEP = []


def mm(v):
    return pcbnew.FromMM(v)


def pt(x, y):
    return pcbnew.VECTOR2I(mm(x), mm(y))


def rules(board):
    # JLCPCB 2-layer: 0.127 mm (5 mil) track and space at the conservative reading of their
    # capabilities, 0.3 mm drill. Freerouting necks traces into fine-pitch pins; widen_necks lifts
    # those back to the minimum.
    ds = board.GetDesignSettings()
    ds.m_TrackMinWidth = mm(0.127)
    ds.m_MinClearance = mm(0.2)
    ds.m_ViasMinSize = mm(0.6)
    ds.m_MinThroughDrill = mm(0.3)
    ds.m_CopperEdgeClearance = mm(0.3)
    ds.m_HoleClearance = mm(0.25)


def net_classes(board):
    ns = board.GetDesignSettings().m_NetSettings
    default = ns.GetDefaultNetclass()
    default.SetTrackWidth(mm(0.15))
    default.SetClearance(mm(0.2))
    default.SetViaDiameter(mm(0.6))
    default.SetViaDrill(mm(0.3))
    power = pcbnew.NETCLASS("Power")
    power.SetTrackWidth(mm(0.4))
    power.SetClearance(mm(0.2))
    power.SetViaDiameter(mm(0.6))
    power.SetViaDrill(mm(0.3))
    ns.SetNetclass("Power", power)
    for n in POWER_NETS:
        ns.SetNetclassPatternAssignment(n, "Power")
    # +3V3 reaches the module's 0.8 mm-pitch pads; 0.3 mm still carries its ~300 mA TX peaks easily.
    rail = pcbnew.NETCLASS("Rail")
    rail.SetTrackWidth(mm(0.3))
    rail.SetClearance(mm(0.2))
    rail.SetViaDiameter(mm(0.6))
    rail.SetViaDrill(mm(0.3))
    ns.SetNetclass("Rail", rail)
    ns.SetNetclassPatternAssignment("+3V3", "Rail")
    ns.RecomputeEffectiveNetclasses()


def pad(board, ref, num):
    fp = board.FindFootprintByReference(ref)
    return next(p for p in fp.Pads() if p.GetNumber() == num)


def track(board, a, b, width, layer=pcbnew.F_Cu, net=None):
    t = pcbnew.PCB_TRACK(board)
    t.SetStart(a)
    t.SetEnd(b)
    t.SetWidth(mm(width))
    t.SetLayer(layer)
    t.SetNet(net)
    t.SetLocked(True)  # exported as fixed wiring, so Freerouting leaves it alone
    board.Add(t)
    KEEP.append(t)


def via(board, at, net):
    v = pcbnew.PCB_VIA(board)
    v.SetPosition(at)
    v.SetWidth(mm(0.6))
    v.SetDrill(mm(0.3))
    v.SetNet(net)
    v.SetLocked(True)
    board.Add(v)
    KEEP.append(v)


def path(board, net, points, width):
    for a, b in zip(points, points[1:]):
        track(board, a, b, width, net=net)


def power_stage(board):
    """TPS63900 input, output and inductor loops: short, direct, top layer (datasheet 10.1).

    The VIN/LX1/GND/LX2/VOUT pins are on 0.5 mm pitch, so each trace leaves its pin at pad
    width and only widens once clear of the row."""
    u1 = board.FindFootprintByReference("U1")
    p = {n: pad(board, "U1", n) for n in ("6", "7", "8", "9", "10", "11")}
    xy = {n: (pcbnew.ToMM(q.GetPosition().x), pcbnew.ToMM(q.GetPosition().y)) for n, q in p.items()}
    row_y = xy["10"][1]
    up = -1.0 if row_y < pcbnew.ToMM(u1.GetPosition().y) else 1.0
    half_len = pcbnew.ToMM(p["10"].GetSize().y if abs(p["10"].GetSize().y) > abs(p["10"].GetSize().x)
                           else p["10"].GetSize().x) / 2
    out = 1.0 if xy["10"][0] > xy["6"][0] else -1.0   # direction from VOUT end toward VIN end

    def P(x, y):
        return pt(x, y)

    def pad_xy(ref, num):
        q = pad(board, ref, num).GetPosition()
        return pcbnew.ToMM(q.x), pcbnew.ToMM(q.y)

    # LX1 -> L1 pad 1, LX2 -> L1 pad 2: straight out of the row, then a diagonal to the inductor.
    for pin, lpad in (("9", "1"), ("7", "2")):
        x, y = xy[pin]
        lx, ly = pad_xy("L1", lpad)
        esc = (x, y + up * (half_len + 0.5))
        net = p[pin].GetNet()
        track(board, P(x, y), P(*esc), 0.25, net=net)
        path(board, net, [P(*esc), P(lx, ly - up * 0.6), P(lx, ly)], 0.3)
    # VIN -> C1 pad 1 and VOUT -> C3 pad 1: out of the row end, sideways, then into the cap.
    for pin, cref in (("10", "C1"), ("6", "C3")):
        x, y = xy[pin]
        cx, cy = pad_xy(cref, "1")
        side = out if pin == "10" else -out
        edge = (x + side * 0.08, y)  # toward the outer edge of the pad, away from the neighbour
        net = p[pin].GetNet()
        path(board, net, [P(*edge), P(cx, y), P(cx, cy)], 0.3)
    # C1 to the bulk cap C2.
    path(board, p["10"].GetNet(), [pad(board, "C1", "1").GetPosition(), pad(board, "C2", "1").GetPosition()], 0.4)
    # EN and SEL (pins 1, 2) to VIN: joined along the row, then through a via and a short
    # bottom-layer run to a via beside C2's VIN pad.
    p1, p2 = pad(board, "U1", "1"), pad(board, "U1", "2")
    x1, y1 = (pcbnew.ToMM(v) for v in (p1.GetPosition().x, p1.GetPosition().y))
    vin_net = p["10"].GetNet()
    track(board, p1.GetPosition(), p2.GetPosition(), 0.25, net=vin_net)
    esc = (x1, y1 - up * (half_len + 0.35))
    v1 = (x1 + out * 0.6, esc[1] - up * 0.6)
    path(board, vin_net, [p1.GetPosition(), P(*esc), P(*v1)], 0.25)
    via(board, P(*v1), vin_net)
    c2x, c2y = pad_xy("C2", "1")
    v2 = (c2x, c2y + up * 1.1)
    via(board, P(*v2), vin_net)
    track(board, P(c2x, c2y), P(*v2), 0.4, net=vin_net)
    lane = c2x - 1.2  # left of C2 and its ground via
    for a, b in zip([v1, (lane, v1[1]), (lane, v2[1])], [(lane, v1[1]), (lane, v2[1]), v2]):
        track(board, P(*a), P(*b), 0.4, layer=pcbnew.B_Cu, net=vin_net)
    gnd = p["8"].GetNet()
    # GND pin 8 into the exposed pad, two vias in the pad to the bottom pour.
    track(board, P(*xy["8"]), P(*xy["11"]), 0.25, net=gnd)
    # CFG2 and CFG3 (pins 4, 5) are tied to GND: straight into the exposed pad beside them.
    for n in ("4", "5"):
        q = pad(board, "U1", n).GetPosition()
        track(board, q, pcbnew.VECTOR2I(q.x, p["11"].GetPosition().y), 0.25, net=gnd)
    ex, ey = xy["11"]
    for dx in (-0.45, 0.45):
        via(board, P(ex + dx, ey), gnd)
    # Capacitor grounds: a via right next to each.
    for ref in ("C1", "C2", "C3"):
        gx, gy = pad_xy(ref, "2")
        v = (gx, gy - up * 1.0)
        track(board, P(gx, gy), P(*v), 0.4, net=gnd)
        via(board, P(*v), gnd)


def sht40_supply(board):
    """SHT40 VDD (pad 3) sits between its GND pad and the pour; give it a direct run to R12's
    +3V3 pad, which the router then connects onward."""
    a = pad(board, "U4", "3").GetPosition()
    b = pad(board, "R12", "1").GetPosition()
    mid = pcbnew.VECTOR2I(a.x + mm(0.6), a.y)
    path(board, pad(board, "U4", "3").GetNet(), [a, mid, b], 0.2)


def widen_necks(board, minimum=0.127):
    for t in board.GetTracks():
        if t.GetClass() == "PCB_TRACK" and t.GetWidth() < mm(minimum):
            t.SetWidth(mm(minimum))


def stitch_islands(board):
    """Every piece of the top pour needs a via down to the bottom pour, and every unlocked GND via
    needs pour on both layers. Adds or removes vias, refilling, until both hold."""
    gnd = board.FindNet("GND")
    code = gnd.GetNetCode()
    top = next(z for z in board.Zones() if not z.GetIsRuleArea() and z.GetLayer() == pcbnew.F_Cu)
    bot = next(z for z in board.Zones() if not z.GetIsRuleArea() and z.GetLayer() == pcbnew.B_Cu)
    for _ in range(4):
        changed = False
        for v in [t for t in board.GetTracks() if t.GetClass() == "PCB_VIA" and t.GetNetCode() == code]:
            if v.IsLocked():
                continue
            p = v.GetPosition()
            on_top = top.HitTestFilledArea(pcbnew.F_Cu, p)
            on_bot = bot.HitTestFilledArea(pcbnew.B_Cu, p)
            touching = [t for t in board.GetTracks() if t.GetClass() == "PCB_TRACK" and t.GetNetCode() == code
                        and (v.HitTest(t.GetStart()) or v.HitTest(t.GetEnd()))]
            if not on_bot or (not on_top and not touching):
                board.Remove(v)
                KEEP.append(v)
                changed = True
        polys = top.GetFilledPolysList(pcbnew.F_Cu)
        vias = [t.GetPosition() for t in board.GetTracks() if t.GetClass() == "PCB_VIA" and t.GetNetCode() == code]
        for i in range(polys.OutlineCount()):
            outline = polys.Outline(i)
            if any(outline.PointInside(p) for p in vias):
                continue
            bb = outline.BBox()
            step = mm(0.5)
            placed = False
            y = bb.GetTop() + step
            while y < bb.GetBottom() and not placed:
                x = bb.GetLeft() + step
                while x < bb.GetRight():
                    at = pcbnew.VECTOR2I(x, y)
                    if outline.PointInside(at) and bot.HitTestFilledArea(pcbnew.B_Cu, at) \
                            and clear_of(board, at, mm(0.3), skip_net=code, avoid_silk=True):
                        via(board, at, gnd)
                        placed = changed = True
                        break
                    x += step
                y += step
        if not changed:
            return
        fill(board)


def clear_of(board, at, radius, layers=(pcbnew.F_Cu, pcbnew.B_Cu), skip_net=None, avoid_silk=False):
    """True if a disc of `radius` (internal units) at `at` keeps 0.2 mm from every pad, track,
    via, rule area and the board edge, ignoring items on skip_net. SMD pads on skip_net still
    block: a via in a pad wicks solder away from the joint."""
    margin = radius + mm(0.2)
    l, t, r, b = (mm(v) for v in BOARD)
    if not (l + margin + mm(0.3) < at.x < r - margin - mm(0.3) and t + margin + mm(0.3) < at.y < b - margin - mm(0.3)):
        return False
    if at.x > mm(ANTENNA_X) - margin:  # the module's antenna keepout
        return False
    for fp in board.GetFootprints():
        for q in fp.Pads():
            if q.GetNetCode() == skip_net:
                if q.GetAttribute() == pcbnew.PAD_ATTRIB_SMD and q.HitTest(at, radius + mm(0.05)):
                    return False
                continue
            if any(q.IsOnLayer(ly) for ly in layers) or q.GetAttribute() in (pcbnew.PAD_ATTRIB_PTH, pcbnew.PAD_ATTRIB_NPTH):
                if q.HitTest(at, margin):
                    return False
        for z in fp.Zones():
            if z.GetIsRuleArea() and z.Outline().Collide(at, margin):
                return False
        # silkscreen text, for vias that may go anywhere: the fab clips silk around untented vias
        for text in (fp.Reference(), fp.Value()) if avoid_silk else ():
            if text.IsVisible() and text.GetLayer() == pcbnew.F_SilkS:
                bb = text.GetBoundingBox()
                bb.Inflate(radius)
                if bb.Contains(at):
                    return False
    for d in board.GetDrawings() if avoid_silk else ():
        if d.GetClass() == "PCB_TEXT" and d.GetLayer() == pcbnew.F_SilkS:
            bb = d.GetBoundingBox()
            bb.Inflate(radius)
            if bb.Contains(at):
                return False
    for z in board.Zones():
        if z.GetIsRuleArea() and z.Outline().Collide(at, margin):
            return False
    for t_ in board.GetTracks():
        # vias are drilled holes: keep clear of them whatever their net
        if t_.GetNetCode() == skip_net and t_.GetClass() != "PCB_VIA":
            continue
        if t_.HitTest(at, margin):
            return False
    return True


def ground_fanout(board):
    """Before routing: a short locked trace and via from every SMD GND pad to the bottom pour, so
    no pad depends on the top pour finding a way to it. Tried in eight directions, 1.0-1.6 mm."""
    gnd = board.FindNet("GND")
    code = gnd.GetNetCode()
    r = mm(0.3)
    for fp in board.GetFootprints():
        for q in fp.Pads():
            if q.GetNetCode() != code or q.GetAttribute() != pcbnew.PAD_ATTRIB_SMD:
                continue
            if fp.GetReference() == "U1":
                continue  # the power stage has its own
            c = q.GetPosition()
            for d in (1.0, 1.3, 1.6):
                done = False
                for dx, dy in ((1, 0), (-1, 0), (0, 1), (0, -1), (1, 1), (1, -1), (-1, 1), (-1, -1)):
                    k = d / (2 ** 0.5) if dx and dy else d
                    at = pcbnew.VECTOR2I(c.x + mm(dx * k), c.y + mm(dy * k))
                    if not clear_of(board, at, r, skip_net=code):
                        continue
                    # the short track from pad to via must not cross other copper either
                    mid = pcbnew.VECTOR2I((c.x + at.x) // 2, (c.y + at.y) // 2)
                    if not clear_of(board, mid, mm(0.15), layers=(pcbnew.F_Cu,), skip_net=code):
                        continue
                    t = pcbnew.PCB_TRACK(board)
                    t.SetStart(c)
                    t.SetEnd(at)
                    t.SetWidth(mm(0.25))
                    t.SetLayer(pcbnew.F_Cu)
                    t.SetNet(gnd)
                    t.SetLocked(True)
                    board.Add(t)
                    KEEP.append(t)
                    via(board, at, gnd)
                    done = True
                    break
                if done:
                    break


def stitching(board):
    """After routing: GND vias on a 4 mm grid wherever there is room, tying the two pours."""
    gnd = board.FindNet("GND")
    code = gnd.GetNetCode()
    l, t_, rr, b = BOARD
    y = t_ + 2.0
    while y < b - 1.0:
        x = l + 2.0
        while x < ANTENNA_X:
            at = pt(x, y)
            if clear_of(board, at, mm(0.3), skip_net=code, avoid_silk=True):
                via(board, at, gnd)
            x += 4.0
        y += 4.0


def unconnected(board):
    board.BuildConnectivity()
    return board.GetConnectivity().GetUnconnectedCount(True)


def ground_pours(board):
    gnd = board.FindNet("GND")
    l, t, r, b = BOARD
    for layer in (pcbnew.F_Cu, pcbnew.B_Cu):
        z = pcbnew.ZONE(board)
        z.SetLayer(layer)
        z.SetNet(gnd)
        outline = z.Outline()
        outline.NewOutline()
        for x, y in ((l, t), (r, t), (r, b), (l, b)):
            outline.Append(mm(x), mm(y))
        z.SetLocalClearance(mm(0.25))
        z.SetMinThickness(mm(0.2))
        z.SetPadConnection(pcbnew.ZONE_CONNECTION_THT_THERMAL)  # solid to SMD pads
        z.SetThermalReliefGap(mm(0.25))
        z.SetThermalReliefSpokeWidth(mm(0.3))
        z.SetIslandRemovalMode(pcbnew.ISLAND_REMOVAL_MODE_ALWAYS)
        board.Add(z)
        KEEP.append(z)


def fill(board):
    pcbnew.ZONE_FILLER(board).Fill(board.Zones())


def freeroute(board, passes=100):
    # GND is left to the pours: strip it from the pads while Freerouting runs.
    gnd = board.FindNet("GND")
    unnet = board.FindNet("")  # net 0
    gnd_pads = [q for fp in board.GetFootprints() for q in fp.Pads() if q.GetNetname() == "GND"]
    for q in gnd_pads:
        q.SetNet(unnet)
    with tempfile.TemporaryDirectory() as d:
        dsn, ses = Path(d) / "board.dsn", Path(d) / "board.ses"
        if not pcbnew.ExportSpecctraDSN(board, str(dsn)):
            raise SystemExit("DSN export failed")
        subprocess.run(
            ["freerouting", "--gui.enabled=false", "-de", str(dsn), "-do", str(ses), "-mp", str(passes)],
            check=True,
        )
        if not pcbnew.ImportSpecctraSES(board, str(ses)):
            raise SystemExit("SES import failed")
    for q in gnd_pads:
        q.SetNet(gnd)
    remove_dangling(board)


def remove_dangling(board):
    """Drop Freerouting's unused fanout vias and stubs: vias touched by tracks on one layer only,
    then track ends that touch nothing. Repeats until nothing changes. GND is left to the pours."""
    def key(p):
        # 10 um grid: Freerouting's endpoints and via centres can differ by rounding
        return (round(p.x / 10000), round(p.y / 10000))

    changed = True
    while changed:
        changed = False
        tracks = [t for t in board.GetTracks() if t.GetClass() == "PCB_TRACK"]
        vias = [t for t in board.GetTracks() if t.GetClass() == "PCB_VIA"]
        ends = {}
        for t in tracks:
            for p in (t.GetStart(), t.GetEnd()):
                ends.setdefault((t.GetNetCode(),) + key(p), []).append(t)
        for v in vias:
            if v.GetNetname() == "GND" or v.IsLocked():
                continue
            touching = ends.get((v.GetNetCode(),) + key(v.GetPosition()), [])
            if len({t.GetLayer() for t in touching}) < 2:
                board.Remove(v)
                KEEP.append(v)
                changed = True
        if changed:
            continue
        anchors = {(v.GetNetCode(),) + key(v.GetPosition()) for v in vias}
        for t in tracks:
            if t.IsLocked():
                continue
            for p in (t.GetStart(), t.GetEnd()):
                k = (t.GetNetCode(),) + key(p)
                if len(ends.get(k, [])) > 1 or k in anchors:
                    continue
                if any(pad.HitTest(p) and pad.GetNetCode() == t.GetNetCode() and pad.IsOnLayer(t.GetLayer())
                       for fp in board.GetFootprints() for pad in fp.Pads()):
                    continue
                board.Remove(t)
                KEEP.append(t)
                changed = True
                break


def attempt():
    board = pcbnew.LoadBoard(str(PCB))
    KEEP.append(board)
    rules(board)
    net_classes(board)
    power_stage(board)
    sht40_supply(board)
    ground_fanout(board)
    freeroute(board)
    freeroute(board)  # a second run starts from the first's result and only finishes what is left
    widen_necks(board)
    stitching(board)
    ground_pours(board)
    fill(board)
    stitch_islands(board)
    return board


def main(attempts=5):
    # Freerouting is not deterministic; keep the run that leaves the fewest connections open.
    best, best_n = None, None
    for i in range(attempts):
        board = attempt()
        n = unconnected(board)
        print(f"attempt {i + 1}: {n} unconnected")
        if best_n is None or n < best_n:
            best, best_n = board, n
        if n == 0:
            break
    pcbnew.SaveBoard(str(PCB), best)  # also writes the rules into laundry-sensor.kicad_pro
    print("routed", PCB, "unconnected:", best_n)


if __name__ == "__main__":
    main()
