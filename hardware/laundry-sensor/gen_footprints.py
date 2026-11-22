# Writes the footprints that KiCad's libraries lack into footprints/laundry.pretty.
# ADXL362 (LGA-16, 3 x 3.25 mm): land pattern from Figure 52 of the ADXL362 datasheet (Rev. B):
# 0.8 x 0.3 mm pads on 0.5 mm pitch, 3.5 mm across the long-side pads, 3.35 mm across the short-side pads.
from pathlib import Path

OUT = Path(__file__).parent / "footprints" / "laundry.pretty"


def pad(num, x, y, w, h):
    return (f'\t(pad "{num}" smd roundrect (at {x:.4f} {y:.4f}) (size {w} {h}) '
            f'(layers "F.Cu" "F.Paste" "F.Mask") (roundrect_rratio 0.25))\n')


def line(x1, y1, x2, y2, layer, width):
    return (f'\t(fp_line (start {x1} {y1}) (end {x2} {y2}) '
            f'(stroke (width {width}) (type solid)) (layer "{layer}"))\n')


def adxl362():
    pads = []
    # Pins 1-5: left column, top to bottom. Pins 9-13: right column, bottom to top.
    for i, y in enumerate([-1.0, -0.5, 0.0, 0.5, 1.0]):
        pads.append(pad(1 + i, -1.35, y, 0.8, 0.3))
        pads.append(pad(13 - i, 1.35, y, 0.8, 0.3))
    # Pins 6-8: bottom row, left to right. Pins 14-16: top row, right to left.
    for i, x in enumerate([-0.5, 0.0, 0.5]):
        pads.append(pad(6 + i, x, 1.275, 0.3, 0.8))
        pads.append(pad(16 - i, x, -1.275, 0.3, 0.8))
    bx, by = 1.5, 1.625  # half body: 3.0 x 3.25 mm
    body = [
        line(-bx, -by, bx, -by, "F.Fab", 0.1), line(bx, -by, bx, by, "F.Fab", 0.1),
        line(bx, by, -bx, by, "F.Fab", 0.1), line(-bx, by, -bx, -by, "F.Fab", 0.1),
    ]
    cy = 1.95
    court = [
        line(-2.0, -cy, 2.0, -cy, "F.CrtYd", 0.05), line(2.0, -cy, 2.0, cy, "F.CrtYd", 0.05),
        line(2.0, cy, -2.0, cy, "F.CrtYd", 0.05), line(-2.0, cy, -2.0, -cy, "F.CrtYd", 0.05),
    ]
    silk = [line(-1.9, -1.85, -1.4, -1.85, "F.SilkS", 0.12)]  # pin 1 marker
    return (
        '(footprint "ADXL362_LGA-16_3x3.25mm_P0.5mm"\n'
        '\t(version 20241229)\n\t(generator "gen_footprints.py")\n\t(layer "F.Cu")\n'
        '\t(descr "ADXL362 LGA-16 3x3.25mm, land pattern per datasheet Figure 52")\n'
        '\t(attr smd)\n'
        '\t(property "Reference" "REF**" (at 0 -2.6 0) (layer "F.SilkS") '
        '(effects (font (size 0.8 0.8) (thickness 0.12))))\n'
        '\t(property "Value" "ADXL362" (at 0 2.6 0) (layer "F.Fab") '
        '(effects (font (size 0.8 0.8) (thickness 0.12))))\n'
        + "".join(body + court + silk + pads) + ")\n"
    )


if __name__ == "__main__":
    OUT.mkdir(parents=True, exist_ok=True)
    (OUT / "ADXL362_LGA-16_3x3.25mm_P0.5mm.kicad_mod").write_text(adxl362())
