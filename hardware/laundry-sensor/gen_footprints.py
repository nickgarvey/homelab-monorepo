# Writes our own footprints into footprints/laundry.pretty.
# LIS2DW12 (LGA-12, 2 x 2 mm): package from Figure 18 of the LIS2DW12 datasheet (DS11811 Rev 9):
# 0.275 x 0.25 mm terminals on 0.5 mm pitch, centred 0.7625 mm from the body centre. Each land is
# the terminal lengthened 0.1 mm outward, which keeps 0.25 mm between neighbouring lands.
from pathlib import Path

OUT = Path(__file__).parent / "footprints" / "laundry.pretty"


def pad(num, x, y, w, h):
    return (f'\t(pad "{num}" smd roundrect (at {x:.4f} {y:.4f}) (size {w} {h}) '
            f'(layers "F.Cu" "F.Paste" "F.Mask") (roundrect_rratio 0.25))\n')


def line(x1, y1, x2, y2, layer, width):
    return (f'\t(fp_line (start {x1} {y1}) (end {x2} {y2}) '
            f'(stroke (width {width}) (type solid)) (layer "{layer}"))\n')


def box(half, layer, width):
    h = half
    return [line(-h, -h, h, -h, layer, width), line(h, -h, h, h, layer, width),
            line(h, h, -h, h, layer, width), line(-h, h, -h, -h, layer, width)]


def lis2dw12():
    # Top view, y down: pins 1-4 left column top to bottom, 5-6 bottom row left to right,
    # 7-10 right column bottom to top, 11-12 top row right to left (datasheet Figure 2, mirrored).
    c, length, width = 0.8125, 0.375, 0.25
    pads = []
    for i, y in enumerate([-0.75, -0.25, 0.25, 0.75]):
        pads.append(pad(1 + i, -c, y, length, width))
        pads.append(pad(10 - i, c, y, length, width))
    for i, x in enumerate([-0.25, 0.25]):
        pads.append(pad(5 + i, x, c, width, length))
        pads.append(pad(12 - i, x, -c, width, length))
    silk = [line(-1.3, -1.2, -0.9, -1.2, "F.SilkS", 0.12)]  # pin 1 marker
    return (
        '(footprint "LIS2DW12_LGA-12_2x2mm_P0.5mm"\n'
        '\t(version 20241229)\n\t(generator "gen_footprints.py")\n\t(layer "F.Cu")\n'
        '\t(descr "ST LIS2DW12 LGA-12 2x2mm, lands from datasheet Figure 18 terminals + 0.1 mm outward")\n'
        '\t(attr smd)\n'
        '\t(property "Reference" "REF**" (at 0 -1.9 0) (layer "F.SilkS") '
        '(effects (font (size 0.8 0.8) (thickness 0.12))))\n'
        '\t(property "Value" "LIS2DW12" (at 0 1.9 0) (layer "F.Fab") '
        '(effects (font (size 0.8 0.8) (thickness 0.12))))\n'
        + "".join(box(1.0, "F.Fab", 0.1) + box(1.3, "F.CrtYd", 0.05) + silk + pads) + ")\n"
    )


if __name__ == "__main__":
    OUT.mkdir(parents=True, exist_ok=True)
    for old in OUT.glob("*.kicad_mod"):
        old.unlink()
    (OUT / "LIS2DW12_LGA-12_2x2mm_P0.5mm.kicad_mod").write_text(lis2dw12())
