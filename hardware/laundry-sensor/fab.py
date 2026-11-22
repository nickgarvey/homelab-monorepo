# Writes JLCPCB fabrication and assembly files into fab/: Gerbers and drill files (zipped),
# BOM and CPL. Run in `nix develop .#pcb` after route_pcb.py.
#
# JLC places each part by its EasyEDA footprint, whose zero rotation and origin differ from
# KiCad's for some packages. CORRECTIONS holds those differences. Where JLC's own preview has been
# observed (kicad-jlcpcb-tools, tests/fixtures/jlcfootprint/truth.csv) the value comes from there;
# the rest follow the package conventions in JLCKicadTools' cpl_rotations_db.csv and need a look in
# JLC's placement preview when ordering (CHECK_IN_PREVIEW).
import csv
import math
import re
import shutil
import subprocess
import zipfile
from pathlib import Path

import pcbnew

HERE = Path(__file__).parent
PCB = HERE / "laundry-sensor.kicad_pcb"
OUT = HERE / "fab"

# Footprint name pattern: (added rotation in degrees, origin offset in mm in the footprint's frame).
CORRECTIONS = [
    (r"^SOT-23$", 180, (0.0, 0.0)),                              # observed
    (r"^USB_C_Receptacle_HRO_TYPE-C-31-M-12", 0, (0.0, -1.571)),  # observed
    (r"^WSON-", 270, (0.0, 0.0)),                                # like DFN/VSON/USON
    (r"^ADXL362_LGA-16", 270, (0.0, 0.0)),                       # pin 1 top-left, like QFN
    (r"^Sensirion_DFN-4", 270, (0.0, 0.0)),                      # like DFN
    (r"^ESP32-C6-MINI-1$", 0, (0.0, -2.7)),                      # body centre, not pad centre
    (r"^SW_SPST_TS-1088", 0, (0.0, -0.975)),                     # body centre, not pad centre
]
CHECK_IN_PREVIEW = {"U1", "U2", "U3", "U4", "SW1", "SW2", "SW3"}
HAND_SOLDERED = {"BT1", "J2"}  # through-hole, soldered after assembly
LAYERS = "F.Cu,B.Cu,F.Paste,B.Paste,F.SilkS,B.SilkS,F.Mask,B.Mask,Edge.Cuts"


def correction(fp_name):
    for pattern, rot, off in CORRECTIONS:
        if re.search(pattern, fp_name):
            return rot, off
    return 0, (0.0, 0.0)


def gerbers():
    g = OUT / "gerbers"
    shutil.rmtree(g, ignore_errors=True)
    g.mkdir(parents=True)
    run = lambda *a: subprocess.run(["kicad-cli", "pcb", "export", *a, str(PCB)], check=True,
                                    stdout=subprocess.DEVNULL)
    run("gerbers", "-o", f"{g}/", "-l", LAYERS, "--subtract-soldermask", "--no-x2", "--no-netlist",
        "--check-zones")
    run("drill", "-o", f"{g}/", "--format", "excellon", "--excellon-units", "mm",
        "--excellon-zeros-format", "decimal", "--excellon-oval-format", "alternate")
    with zipfile.ZipFile(OUT / "laundry-sensor-gerbers.zip", "w", zipfile.ZIP_DEFLATED) as z:
        for f in sorted(g.iterdir()):
            z.write(f, f.name)


def assembled(board):
    for fp in sorted(board.GetFootprints(), key=lambda f: (re.sub(r"\d", "", f.GetReference()),
                                                           int(re.sub(r"\D", "", f.GetReference()) or 0))):
        if fp.HasField("LCSC") and fp.GetField("LCSC").GetText() and fp.GetReference() not in HAND_SOLDERED:
            yield fp


def bom(board):
    groups = {}
    for fp in assembled(board):
        key = (fp.GetValue(), fp.GetFPID().GetLibItemName().wx_str(), fp.GetField("LCSC").GetText())
        groups.setdefault(key, []).append(fp.GetReference())
    with open(OUT / "laundry-sensor-bom.csv", "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["Comment", "Designator", "Footprint", "LCSC Part #"])
        for (value, footprint, lcsc), refs in groups.items():
            w.writerow([value, ",".join(refs), footprint, lcsc])


def cpl(board):
    with open(OUT / "laundry-sensor-cpl.csv", "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["Designator", "Mid X", "Mid Y", "Layer", "Rotation"])
        for fp in assembled(board):
            rot, (ox, oy) = correction(fp.GetFPID().GetLibItemName().wx_str())
            a = math.radians(fp.GetOrientationDegrees())
            # KiCad rotates counter-clockwise on screen, with y pointing down.
            dx = ox * math.cos(a) + oy * math.sin(a)
            dy = -ox * math.sin(a) + oy * math.cos(a)
            x = pcbnew.ToMM(fp.GetPosition().x) + dx
            y = pcbnew.ToMM(fp.GetPosition().y) + dy
            layer = "Bottom" if fp.IsFlipped() else "Top"
            # JLC's coordinates have y up, like the Gerbers.
            w.writerow([fp.GetReference(), f"{x:.3f}mm", f"{-y:.3f}mm", layer,
                        f"{(fp.GetOrientationDegrees() + rot) % 360:g}"])


def main():
    OUT.mkdir(exist_ok=True)
    gerbers()
    board = pcbnew.LoadBoard(str(PCB))
    bom(board)
    cpl(board)
    print("wrote", OUT)
    print("check in JLC's placement preview:", ", ".join(sorted(CHECK_IN_PREVIEW)))
    print("hand-solder after assembly:", ", ".join(sorted(HAND_SOLDERED)))


if __name__ == "__main__":
    main()
