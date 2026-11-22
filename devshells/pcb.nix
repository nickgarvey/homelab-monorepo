# PCB design shell for hardware/: atopile, KiCad (kicad-cli + pcbnew Python), Freerouting.
# Activate with: nix develop .#pcb
{ pkgs }:

pkgs.mkShell {
  buildInputs = with pkgs; [
    atopile
    kicad
    freerouting
    # pcbnew bindings, for placement and routing scripts
    (python3.withPackages (ps: [ ps.kicad ]))
    jq
  ];
}
