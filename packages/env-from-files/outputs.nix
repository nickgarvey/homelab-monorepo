# env-from-files and its image: runs an app with env vars read from the files of
# a directory (pod secrets mounted by the Secrets Store CSI driver). Imported by
# packages/outputs.nix.
{ nixpkgs, system }:
let
  pkgs = nixpkgs.legacyPackages.${system};

  package = pkgs.buildGoModule {
    pname = "env-from-files";
    version = "0.1.0";
    src = ./.;
    vendorHash = null;
    env.CGO_ENABLED = "0";
    ldflags = [ "-s" "-w" ];
    meta.mainProgram = "env-from-files";
  };
in
{
  inherit package;

  # Pushed as oci.garvey.sh/env-from-files:<tag>, the tag being the package's
  # store hash. Pods run it as an init container that copies the static binary
  # into an emptyDir (`env-from-files install /opt/env-from-files`), so it works
  # in app images without a shell.
  image = pkgs.dockerTools.buildLayeredImage {
    name = "env-from-files";
    tag = builtins.substring 11 32 package.outPath;
    config = {
      Entrypoint = [ "${package}/bin/env-from-files" ];
      User = "65534:65534";
    };
  };
}
