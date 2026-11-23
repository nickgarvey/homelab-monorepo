# failed-pod-pruner and its image, which the CronJob in
# k8s/manifests/failed-pod-pruner runs. Imported by packages/outputs.nix.
{ nixpkgs, system }:
let
  pkgs = nixpkgs.legacyPackages.${system};

  package = pkgs.writeShellApplication {
    name = "failed-pod-pruner";
    runtimeInputs = [ pkgs.kubectl pkgs.jq pkgs.coreutils ];
    text = builtins.readFile ./failed-pod-pruner.sh;
  };
in
{
  inherit package;

  # Pushed as oci.garvey.sh/failed-pod-pruner:<tag>, the tag being the
  # script's store hash, so a kubectl or jq bump changes it too.
  image = pkgs.dockerTools.buildLayeredImage {
    name = "failed-pod-pruner";
    tag = builtins.substring 11 32 package.outPath;
    contents = [ pkgs.cacert ];
    config = {
      Entrypoint = [ "${package}/bin/failed-pod-pruner" ];
      User = "65534:65534";
      Env = [ "SSL_CERT_FILE=/etc/ssl/certs/ca-bundle.crt" ];
    };
  };
}
