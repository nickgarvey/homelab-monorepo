# The diabetes MCP server (k8s/manifests/mcp-diabetes) and its image. Imported by packages/outputs.nix.
{ nixpkgs, system }:
let
  pkgs = import nixpkgs { inherit system; };
  python = pkgs.python3;

  package = python.pkgs.buildPythonApplication {
    pname = "diabetes-mcp";
    version = "0.1.0";
    pyproject = true;
    src = ./.;
    build-system = [ python.pkgs.setuptools ];
    # tzdata: zoneinfo's fallback database, so time zones work in the image.
    dependencies = with python.pkgs; [ httpx mcp tzdata uvicorn ];
    nativeCheckInputs = [ python.pkgs.pytestCheckHook ];
  };
in
{
  inherit package;

  # Pushed as oci.garvey.sh/diabetes-mcp:<tag>, the tag being the package's
  # store hash, so any change to it changes the tag.
  image = pkgs.dockerTools.buildLayeredImage {
    name = "diabetes-mcp";
    tag = builtins.substring 11 32 package.outPath;
    contents = pkgs.buildEnv {
      name = "diabetes-mcp-root";
      paths = [ package pkgs.cacert ];
      pathsToLink = [ "/bin" "/etc" ];
    };
    extraCommands = ''
      mkdir -m 1777 tmp
    '';
    config = {
      Entrypoint = [ "/bin/diabetes-mcp" ];
      User = "1000:1000";
      ExposedPorts."8000/tcp" = { };
      Env = [
        "HOME=/tmp"
        "SSL_CERT_FILE=/etc/ssl/certs/ca-bundle.crt"
      ];
    };
  };
}
