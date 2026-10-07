# homelab-nixpkgs outputs: homelab packages and container images. Imported by
# the root flake.nix.
{ nixpkgs, ... }:
let
  system = "x86_64-linux";

  playwright-renderer = import ./playwright-renderer/outputs.nix { inherit nixpkgs; };
  trmnl-display = import ./trmnl-display/outputs.nix { inherit nixpkgs; };
  ninfer = import ./ninfer/outputs.nix { inherit nixpkgs; };
  thinkrail = import ./thinkrail/outputs.nix { inherit nixpkgs; };
  lan-table = import ./lan-table/outputs.nix { inherit nixpkgs; };
in
{
  packages.${system} = {
    playwright-renderer = playwright-renderer.dockerImage.${system};
    trmnl-display = trmnl-display.dockerImage.${system};
    ninfer = ninfer.packages.${system}.ninfer;
    ninfer-image = ninfer.dockerImage.${system};
    thinkrail = thinkrail.packages.${system}.thinkrail;
    thinkrail-node-modules = thinkrail.packages.${system}.node-modules;
    lan-table = lan-table.packages.${system}.lan-table;
  };
}
