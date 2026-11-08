# Caddy with the caddy-jwt plugin, and the image k8s/manifests/mcp runs: the
# public gateway that checks Authelia JWTs in front of the MCP modules.
# Imported by packages/outputs.nix.
{ nixpkgs, system }:
let
  pkgs = import nixpkgs { inherit system; };

  package = pkgs.caddy.withPlugins {
    plugins = [ "github.com/ggicci/caddy-jwt@v1.4.0" ];
    hash = "sha256-28ZA3CVc/meSglrvMYWlnDpmDzbXgYsZJHze8t1Xa7o=";
  };

  # The reload sidecar: Caddy reads `tls` files only when it loads a config,
  # so when the mounted certificate, key or Caddyfile changes (kubelet updates
  # Secret and ConfigMap volumes in place), force a reload through the admin
  # API on the pod's loopback. A failed reload is retried on the next poll.
  reloader = pkgs.writeShellApplication {
    name = "mcp-gateway-reload";
    runtimeInputs = [ pkgs.coreutils pkgs.curl ];
    text = ''
      files=(/certs/tls.crt /certs/tls.key /etc/caddy/Caddyfile)
      loaded=$(cat "''${files[@]}" | sha256sum)
      echo "watching ''${files[*]}"
      while sleep 60; do
        current=$(cat "''${files[@]}" | sha256sum)
        [ "$current" = "$loaded" ] && continue
        # must-revalidate: reload even though the config text is unchanged.
        if curl -fsS -X POST -H 'Content-Type: text/caddyfile' \
            -H 'Cache-Control: must-revalidate' \
            --data-binary @/etc/caddy/Caddyfile http://127.0.0.1:2019/load; then
          loaded=$current
          echo "reloaded after a change to the certificate or Caddyfile"
        else
          echo "reload failed; retrying in 60s" >&2
        fi
      done
    '';
  };

  imageRoot = pkgs.buildEnv {
    name = "image-root";
    # cacert: caddy-jwt and the auth.home.garvey.sh route reach Authelia over
    # TLS with its Let's Encrypt certificate.
    paths = [ package pkgs.cacert reloader ];
    pathsToLink = [ "/bin" "/etc" ];
  };
in
{
  inherit package;

  # Pushed as oci.garvey.sh/mcp-gateway:<tag>. The tag is Caddy's version plus
  # the image root's store hash, so a plugin or reloader change changes the
  # tag in k8s/manifests/mcp too.
  image = pkgs.dockerTools.buildLayeredImage {
    name = "mcp-gateway";
    tag = "${package.version}-${builtins.substring 11 7 imageRoot.outPath}";

    contents = imageRoot;

    config = {
      Entrypoint = [ "/bin/caddy" "run" "--config" "/etc/caddy/Caddyfile" "--adapter" "caddyfile" ];
      User = "65534:65534";
      ExposedPorts = { "443/tcp" = { }; };
      Env = [
        "SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
        # Caddy writes its autosave and locks here; nothing in it must persist.
        "XDG_DATA_HOME=/tmp"
        "XDG_CONFIG_HOME=/tmp"
      ];
    };
  };
}
