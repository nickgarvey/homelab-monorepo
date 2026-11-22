#!/usr/bin/env bash
# DRC summary and renders of laundry-sensor.kicad_pcb into $1 (default: ./out). Run in `nix develop .#pcb`.
set -euo pipefail
cd "$(dirname "$0")"
out=${1:-out}
mkdir -p "$out"
kicad-cli pcb drc -o "$out/drc.rpt" laundry-sensor.kicad_pcb >/dev/null
grep -E '^\[' "$out/drc.rpt" | sed 's/:.*//' | sort | uniq -c | sort -rn
kicad-cli pcb export svg --mode-single --fit-page-to-board --exclude-drawing-sheet \
  --layers F.Cu,F.SilkS,F.Fab,F.CrtYd,Edge.Cuts -o "$out/top.svg" laundry-sensor.kicad_pcb >/dev/null
kicad-cli pcb export svg --mode-single --fit-page-to-board --exclude-drawing-sheet --mirror \
  --layers B.Cu,B.SilkS,B.Fab,B.CrtYd,Edge.Cuts -o "$out/bottom.svg" laundry-sensor.kicad_pcb >/dev/null
