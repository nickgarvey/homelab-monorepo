# The OpenShell CLI at the version of the gateway in k8s/vendor/openshell/chart.env.
# nixpkgs packages an older release (0.0.x) that cannot talk to a 0.1 gateway;
# this overrides its version and hashes. Drop it once nixpkgs has 0.1.x.
{ nixpkgs, system }:
let
  pkgs = nixpkgs.legacyPackages.${system};
in
pkgs.openshell.overrideAttrs (finalAttrs: old: {
  version = "0.1.2";
  src = pkgs.fetchFromGitHub {
    owner = "NVIDIA";
    repo = "OpenShell";
    tag = "v${finalAttrs.version}";
    hash = "sha256-pRLYDqeQZ2HFckZH/QUFCbp4NV7sjQ4ZyyL/6rcShQo=";
  };
  # overrideAttrs does not re-derive cargoDeps from cargoHash.
  cargoDeps = pkgs.rustPlatform.fetchCargoVendor {
    inherit (finalAttrs) pname version src;
    hash = "sha256-zQo6Z62V4n1vQU59fD2Rx3y0nMQuD2Q6A9lzsRyaswI=";
  };
})
