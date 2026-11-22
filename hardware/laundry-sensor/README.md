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
