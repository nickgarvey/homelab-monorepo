# laundry-sensor

Battery-powered vibration sensor for the washer: ESP32-C6 module, ADXL362
accelerometer, SHT40, 2×AA through a TPS63900 buck-boost, reporting over
Matter/Thread. Firmware lives in `firmware/devices/laundry-sensor/`.

Work in `nix develop .#pcb`.

- `circuit.py`: the circuit in SKiDL. `python3 circuit.py` runs ERC and writes `laundry-sensor.net`.
- `report.py`: prints the parts list and every net as Markdown, for review.
- `gen_footprints.py`: writes footprints KiCad's libraries lack (ADXL362) into `footprints/laundry.pretty`.
- `build_pcb.py`: writes `laundry-sensor.kicad_pcb` from the circuit: footprints, nets, placement, outline.
- `check.sh [dir]`: design-rule check summary plus top/bottom SVG renders.
- `route_pcb.py`: routes the board in place (run after `build_pcb.py`): net classes, the TPS63900
  loops by hand, a via from every SMD GND pad, Freerouting for the rest, then ground pours on both
  layers with stitching vias. Freerouting varies between runs, so it keeps the best of up to five.
- `fab.py`: writes JLCPCB files into `fab/`: `laundry-sensor-gerbers.zip`, `laundry-sensor-bom.csv`
  and `laundry-sensor-cpl.csv`. Its `CORRECTIONS` table turns KiCad rotations and origins into
  JLC's (EasyEDA's) for the packages that differ.

## Ordering from JLCPCB

1. Upload `fab/laundry-sensor-gerbers.zip`. 2 layers, 1.6 mm, 87×36 mm; defaults otherwise
   (lead-free HASL is fine; ENIG is flatter for the 0.5 mm-pitch LGA and WSON).
2. Turn on PCB Assembly, top side, Economic. Upload the BOM and CPL.
3. In the placement preview, check pin 1 of U1 (TPS63900), U3 (ADXL362) and U4 (SHT40), and the
   outline of U2 (ESP32-C6 module). These rotations and origins follow package
   conventions; the others match what JLC's preview has been seen to do.
4. BT1 (battery holder, back side) and J2 (PPK2 header) are through-hole and not in the BOM:
   solder them by hand. Fit a jumper on J2 unless a PPK2 is connected.
