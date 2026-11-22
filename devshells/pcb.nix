# PCB design shell for hardware/: SKiDL (circuit as Python), KiCad (kicad-cli + pcbnew Python), Freerouting.
# Activate with: nix develop .#pcb
{ pkgs }:

let
  kicadLibs = pkgs.kicad.libraries;
in
pkgs.mkShell {
  buildInputs = with pkgs; [
    kicad
    freerouting
    jq
    # skidl for the netlist, pcbnew bindings for placement and routing scripts
    (python3.withPackages (ps: [
      ps.kicad
      ps.skidl
    ]))
  ];

  # Where SKiDL looks up KiCad library symbols and footprints.
  KICAD9_SYMBOL_DIR = "${kicadLibs.symbols}/share/kicad/symbols";
  KICAD9_FOOTPRINT_DIR = "${kicadLibs.footprints}/share/kicad/footprints";
}
