# agents outputs: sandbox images and OpenShell provider profiles for the
# agents run on the OpenShell gateway. Imported by the root flake.nix.
{ self, nixpkgs, ... }:
let
  system = "x86_64-linux";
  pkgs = nixpkgs.legacyPackages.${system};
  lib = nixpkgs.lib;

  # The gateway, supervisor and sandbox runtime are pinned to the images of
  # NVIDIA/OpenShell#3702 (nix-lNoiG); without it no sandbox can resolve a
  # name in this IPv6-only cluster. The pin is meant to be temporary.
  prPinned = lib.hasInfix "9ab8076a" (builtins.readFile ../k8s/vendor/openshell/values.yaml);
  pinExpired = prPinned
    && lib.max self.lastModified nixpkgs.lastModified >= 1796083200; # 2026-12-01T00:00:00Z
  checkPin = lib.throwIf pinExpired ''
    The OpenShell PR #3702 image pins in k8s/vendor/openshell/values.yaml
    (nix-lNoiG) expired on 2026-12-01. Move to a release that includes #3702
    and drop the pins and this check in agents/outputs.nix, or push the
    deadline out deliberately.
  '';

  # The provider profile's binaries entry must be the exe that opens the
  # connection: the node that pi's wrapper execs, resolved past symlinks.
  # Fails the build if pi stops running under pkgs.nodejs.
  piLitellmProfile = pkgs.runCommand "pi-agent-litellm-profile.yaml" { } ''
    grep -qF '${pkgs.nodejs}/bin/node' ${pkgs.pi-coding-agent}/bin/.pi-wrapped
    node=$(readlink -f ${pkgs.nodejs}/bin/node)
    substitute ${./pi/litellm-profile.yaml} $out --replace-fail @node@ "$node"
  '';
in
{
  packages.${system} = lib.mapAttrs (_: checkPin) {
    pi-agent-image = import ./pi/image.nix { inherit pkgs; };
    pi-agent-litellm-profile = piLitellmProfile;
  };
}
