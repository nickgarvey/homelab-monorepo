# The Matrix Authentication Service (MAS), which handles every Matrix login,
# from nixpkgs, and the image k8s/manifests/matrix runs. Imported by
# packages/outputs.nix.
{ nixpkgs, system }:
let
  pkgs = import nixpkgs { inherit system; };
  package = pkgs.matrix-authentication-service;
in
{
  inherit package;

  # Pushed as oci.garvey.sh/matrix-authentication-service:<tag>, the version
  # plus the package's store hash, like matrix-synapse.
  image = pkgs.dockerTools.buildLayeredImage {
    name = "matrix-authentication-service";
    tag = "${package.version}-${builtins.substring 11 7 package.outPath}";

    contents = pkgs.buildEnv {
      name = "image-root";
      # /share/matrix-authentication-service holds the templates, assets,
      # translations and policy the config points at. cacert: MAS calls
      # Authelia and Synapse over TLS.
      paths = [ package pkgs.cacert ];
      pathsToLink = [ "/bin" "/etc" "/share" ];
    };

    config = {
      Entrypoint = [ "/bin/mas-cli" ];
      Cmd = [ "server" ];
      # The Deployment's fsGroup and the keys' 0440 mode assume it.
      User = "1000:1000";
      ExposedPorts = { "443/tcp" = { }; };
      Env = [
        "SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
        "MAS_CONFIG=/etc/mas/config/config.yaml"
      ];
    };
  };
}
