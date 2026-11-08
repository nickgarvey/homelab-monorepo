# The Anki MCP server (k8s/manifests/mcp-anki) and its image. Imported by packages/outputs.nix.
{ nixpkgs, system }:
let
  pkgs = import nixpkgs { inherit system; };
  python = pkgs.python3;
  sp = python.sitePackages;

  # Only the `anki` library from nixpkgs' Anki, the same build as
  # anki-sync-server (packages/anki-sync-server), so client and server
  # versions match. Its lib output also carries the desktop app's
  # dependencies (PyQt6 and more); the library needs only those listed below.
  ankiLib = python.pkgs.toPythonModule (pkgs.runCommand "anki-pylib-${pkgs.anki.version}" { } ''
    mkdir -p $out/${sp}
    cp -r ${pkgs.anki.lib}/${sp}/anki ${pkgs.anki.lib}/${sp}/anki-*.dist-info $out/${sp}/
  '');

  package = python.pkgs.buildPythonApplication {
    pname = "anki-mcp";
    version = "0.1.0";
    pyproject = true;
    src = ./.;
    build-system = [ python.pkgs.setuptools ];
    dependencies = with python.pkgs; [
      ankiLib
      mcp
      uvicorn
      # The anki library's runtime dependencies (pythonDeps in nixpkgs' anki).
      decorator
      distro
      markdown
      orjson
      protobuf
      requests
      typing-extensions
    ];
    # The tests run a real anki-sync-server on localhost.
    nativeCheckInputs = [ python.pkgs.pytestCheckHook pkgs.anki-sync-server ];
    preCheck = ''
      export HOME=$TMPDIR
    '';
  };
in
{
  inherit package;

  # Pushed as oci.garvey.sh/anki-mcp:<tag>, the tag being the package's store
  # hash, so any change to it or to nixpkgs' anki changes the tag.
  image = pkgs.dockerTools.buildLayeredImage {
    name = "anki-mcp";
    tag = builtins.substring 11 32 package.outPath;
    contents = pkgs.buildEnv {
      name = "anki-mcp-root";
      # tzdata for the TZ the deployment sets, which Anki uses to store the
      # collection's UTC offset (localOffset).
      paths = [ package pkgs.cacert pkgs.tzdata ];
      pathsToLink = [ "/bin" "/etc" ];
    };
    extraCommands = ''
      mkdir -m 1777 tmp
    '';
    config = {
      Entrypoint = [ "/bin/anki-mcp" ];
      User = "1000:1000";
      ExposedPorts."8000/tcp" = { };
      Env = [
        "HOME=/tmp"
        "ANKI_DATA_DIR=/data"
        "SSL_CERT_FILE=/etc/ssl/certs/ca-bundle.crt"
        "TZDIR=${pkgs.tzdata}/share/zoneinfo"
      ];
    };
  };
}
