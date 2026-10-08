# Temporal server with certificate-identity authorization (main.go, ./authz),
# its temporal-sql-tool (cmd/temporal-sql-tool), and the image
# k8s/manifests/temporal runs. Imported by packages/outputs.nix.
{ nixpkgs, system }:
let
  pkgs = import nixpkgs { inherit system; };

  # Bumping Temporal: change go.temporal.io/server in go.mod, `go mod tidy`,
  # then update vendorHash. buildGoModule runs `go test ./...`, so the authz
  # tests gate the build.
  package = pkgs.buildGoModule {
    pname = "temporal-server";
    version = "1.29.7";
    src = ./.;
    vendorHash = "sha256-jNZNmAg/TCYizl9oWtGskJ5aoY7JxGEciH5pZJCfmT8=";
    env.CGO_ENABLED = "0";
    meta.mainProgram = "temporal-server";
  };
in
{
  inherit package;

  # Pushed as oci.garvey.sh/temporal-server:<tag>. The tag is the Temporal
  # version plus the package's store hash, so any change to this directory
  # changes the tag in k8s/manifests/temporal too.
  image = pkgs.dockerTools.buildLayeredImage {
    name = "temporal-server";
    tag = "${package.version}-${builtins.substring 11 7 package.outPath}";

    contents = pkgs.buildEnv {
      name = "image-root";
      # socat forwards the pod IP's 7233 to the frontend on loopback (see
      # k8s/manifests/temporal/deployment.yaml).
      paths = [ package pkgs.socat pkgs.cacert ];
      pathsToLink = [ "/bin" "/etc" ];
    };

    config = {
      Entrypoint = [ "/bin/temporal-server" ];
      Cmd = [ "start" ];
      # The temporal user of the stock image, which the Deployment's fsGroup
      # and the postgres client key's 0440 mode assume.
      User = "1000:1000";
      ExposedPorts = { "7233/tcp" = { }; "9090/tcp" = { }; };
      Env = [
        "SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
        # The image has no /etc/passwd, and without cgo Go's user.Current
        # falls back to $USER; the server fails to start without it.
        "USER=temporal"
      ];
    };
  };
}
