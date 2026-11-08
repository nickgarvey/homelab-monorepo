# Image of nixpkgs' anki-sync-server for k8s/manifests/anki. Imported by packages/outputs.nix.
{ nixpkgs, system }:
let
  pkgs = import nixpkgs { inherit system; };
  server = pkgs.anki-sync-server;
in
# Pushed as oci.garvey.sh/anki-sync-server:<tag>. The tag is the Anki version
# plus the package's store hash, so a nixpkgs bump changes the tag in
# k8s/manifests/anki too. Clients that sync here (the Anki MCP server) use the
# `anki` package from the same nixpkgs, so client and server versions match.
pkgs.dockerTools.buildLayeredImage {
  name = "anki-sync-server";
  tag = "${server.version}-${builtins.substring 11 7 server.outPath}";

  contents = pkgs.buildEnv {
    name = "image-root";
    paths = [ server pkgs.cacert ];
    pathsToLink = [ "/bin" "/etc" ];
  };

  # The server writes only under SYNC_BASE, but keep a writable /tmp.
  extraCommands = ''
    mkdir -m 1777 tmp
  '';

  config = {
    Entrypoint = [ "/bin/anki-sync-server" ];
    # UID/GID 1000 own the existing files on the anki-data volume (the
    # previous jeankhawand image ran as anki:anki-group = 1000:1000).
    User = "1000:1000";
    ExposedPorts = { "8080/tcp" = { }; };
    Env = [
      "SYNC_PORT=8080"
      "SYNC_BASE=/anki_data"
      "HOME=/tmp"
      "SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
    ];
  };
}
