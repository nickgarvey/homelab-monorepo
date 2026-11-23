# release-watch and its image, which k8s/manifests/release-watch runs.
# Imported by packages/outputs.nix.
{ nixpkgs, system }:
let
  pkgs = import nixpkgs { inherit system; };
  package = pkgs.callPackage ./. { };
  root = pkgs.buildEnv {
    name = "release-watch-root";
    paths = [ package pkgs.cacert ];
    pathsToLink = [ "/bin" "/etc" ];
  };
in
{
  inherit package;

  # Pushed as oci.garvey.sh/release-watch:<tag>, the tag being the store
  # hash of everything in the image.
  image = pkgs.dockerTools.buildLayeredImage {
    name = "release-watch";
    tag = builtins.substring 11 32 root.outPath;
    contents = root;
    config = {
      Entrypoint = [ "/bin/release-watch" ];
      User = "65532:65532";
      Env = [ "SSL_CERT_FILE=/etc/ssl/certs/ca-bundle.crt" ];
      ExposedPorts."9090/tcp" = { };
    };
  };
}
