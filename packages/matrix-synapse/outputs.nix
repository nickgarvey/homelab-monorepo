# Synapse, the Matrix homeserver, from nixpkgs with Postgres support only, and
# the image k8s/manifests/matrix runs. Imported by packages/outputs.nix.
{ nixpkgs, system }:
let
  pkgs = import nixpkgs { inherit system; };

  # Postgres only: no URL previews (Synapse would fetch arbitrary URLs), and
  # no systemd in a container.
  package = pkgs.matrix-synapse.override { extras = [ "postgres" ]; };
in
{
  inherit package;

  # Pushed as oci.garvey.sh/matrix-synapse:<tag>. The tag is Synapse's
  # version plus the package's store hash, so any change here changes the
  # tag in k8s/manifests/matrix too.
  image = pkgs.dockerTools.buildLayeredImage {
    name = "matrix-synapse";
    tag = "${package.version}-${builtins.substring 11 7 package.outPath}";

    contents = pkgs.buildEnv {
      name = "image-root";
      # cacert: Synapse calls Element's push gateway over public TLS.
      paths = [ package pkgs.cacert ];
      pathsToLink = [ "/bin" "/etc" ];
    };

    config = {
      Entrypoint = [ "/bin/synapse_homeserver" ];
      # The Deployment's fsGroup and the postgres client key's 0440 mode
      # assume it.
      User = "1000:1000";
      ExposedPorts = { "443/tcp" = { }; };
      Env = [
        "SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
      ];
    };
  };
}
