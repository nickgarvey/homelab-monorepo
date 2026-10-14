# packages outputs: homelab packages, container images and the deploy tool.
# Imported by the root flake.nix.
{ nixpkgs, celler, ... }:
let
  system = "x86_64-linux";
  pkgs = import nixpkgs { inherit system; };

  playwright-renderer = import ./playwright-renderer/outputs.nix { inherit nixpkgs system; };
  trmnl-display = import ./trmnl-display/outputs.nix { inherit nixpkgs system; };
  ninfer = import ./ninfer/outputs.nix { inherit nixpkgs system; };
  lan-table = import ./lan-table/outputs.nix { inherit nixpkgs system; };
  openshell = import ./openshell/outputs.nix { inherit nixpkgs system; };
  celler-pkg = import ./celler/outputs.nix { inherit nixpkgs celler system; };
  temporal-server = import ./temporal-server/outputs.nix { inherit nixpkgs system; };
  ceph-backup-worker = import ./ceph-backup-worker/outputs.nix { inherit nixpkgs system; };
  matrix-synapse = import ./matrix-synapse/outputs.nix { inherit nixpkgs system; };

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
    inherit lan-table;
    openshell = openshell.cli;
    openshell-gateway-image = openshell.gateway-image;
    openshell-supervisor-image = openshell.supervisor-image;
    openshell-sandbox-image = openshell.sandbox-image;
    celler = celler-pkg.package;
    celler-image = celler-pkg.image;
    inherit deploy;
    temporal-server = temporal-server.package;
    temporal-server-image = temporal-server.image;
    ceph-backup-worker = ceph-backup-worker.package;
    ceph-backup-worker-image = ceph-backup-worker.image;
    matrix-synapse = matrix-synapse.package;
    matrix-synapse-image = matrix-synapse.image;
  };

  checks.${system} = {
    deploy-tests = deploy;
    temporal-server-tests = temporal-server.package;
    ceph-backup-worker-tests = ceph-backup-worker.package;
    openshell-images-test = openshell.images-test;
  };
}
