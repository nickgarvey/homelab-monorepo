# homelab-nixpkgs outputs: homelab packages, their dev shells, and the
# container images deploy.sh pushes. Imported by the root flake.nix.
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
    thinkrail = thinkrail.packages.${system}.thinkrail;
    thinkrail-node-modules = thinkrail.packages.${system}.node-modules;
    lan-table = lan-table.packages.${system}.lan-table;
  };

  # The images deploy.sh builds and pushes to zot. legacyPackages because
  # `nix flake check` rejects nested attrsets under packages.
  legacyPackages.${system}.images = {
    playwright-renderer = playwright-renderer.dockerImage.${system};
    trmnl-display = trmnl-display.dockerImage.${system};
    ninfer = ninfer.dockerImage.${system};
  };

  devShells.${system} = {
    playwright-renderer = playwright-renderer.devShells.${system}.default;
    trmnl-display = trmnl-display.devShells.${system}.default;
    ninfer = ninfer.devShells.${system}.default;
    thinkrail = thinkrail.devShells.${system}.default;
    lan-table = lan-table.devShells.${system}.default;
  };
}
