# longhorn-backup-pruner and its image, which the CronJob in
# k8s/manifests/longhorn/backup-pruner.yaml runs. Imported by packages/outputs.nix.
{ nixpkgs, system }:
let
  pkgs = nixpkgs.legacyPackages.${system};

  package = pkgs.writeShellApplication {
    name = "longhorn-backup-pruner";
    runtimeInputs = [ pkgs.kubectl pkgs.jq pkgs.coreutils pkgs.gnugrep ];
    text = builtins.readFile ./longhorn-backup-pruner.sh;
  };
in
{
  inherit package;

  # Pushed as oci.garvey.sh/longhorn-backup-pruner:<tag>, the tag being the
  # script's store hash, so a kubectl or jq bump changes it too.
  image = pkgs.dockerTools.buildLayeredImage {
    name = "longhorn-backup-pruner";
    tag = builtins.substring 11 32 package.outPath;
    contents = [ pkgs.cacert ];
    config = {
      Entrypoint = [ "${package}/bin/longhorn-backup-pruner" ];
      User = "65534:65534";
      Env = [ "SSL_CERT_FILE=/etc/ssl/certs/ca-bundle.crt" ];
    };
  };
}
