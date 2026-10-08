# packages outputs: homelab packages, container images and the deploy tool.
# Imported by the root flake.nix.
{ nixpkgs, celler, ... }:
let
  system = "x86_64-linux";
  pkgs = import nixpkgs { inherit system; };

  playwright-renderer = import ./playwright-renderer/outputs.nix { inherit nixpkgs system; };
  trmnl-display = import ./trmnl-display/outputs.nix { inherit nixpkgs system; };
  ninfer = import ./ninfer/outputs.nix { inherit nixpkgs system; };
  thinkrail = import ./thinkrail/outputs.nix { inherit nixpkgs system; };
  lan-table = import ./lan-table/outputs.nix { inherit nixpkgs system; };
  openshell = import ./openshell/outputs.nix { inherit nixpkgs system; };
  celler-pkg = import ./celler/outputs.nix { inherit nixpkgs celler system; };

  # The deploy binary, built from ./deploy. buildGoModule runs `go test`
  # as part of the build, so this derivation is also the test check.
  deploy = pkgs.buildGoModule {
    pname = "deploy";
    version = "0.1.0";
    src = ./deploy;
    vendorHash = null; # no external deps
  };
in
{
  packages.${system} = {
    inherit playwright-renderer;
    inherit trmnl-display;
    ninfer = ninfer.ninfer;
    ninfer-image = ninfer.image;
    thinkrail = thinkrail.thinkrail;
    thinkrail-node-modules = thinkrail.node-modules;
    inherit lan-table;
    inherit openshell;
    celler = celler-pkg.package;
    celler-image = celler-pkg.image;
    inherit deploy;
  };

  checks.${system}.deploy-tests = deploy;
}
