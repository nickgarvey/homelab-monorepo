# The static pages MCP server (k8s/manifests/mcp-pages) and its image. Imported by packages/outputs.nix.
{ nixpkgs, system }:
let
  pkgs = import nixpkgs { inherit system; };
  python = pkgs.python3;

  package = python.pkgs.buildPythonApplication {
    pname = "pages-mcp";
    version = "0.1.0";
    pyproject = true;
    src = ./.;
    build-system = [ python.pkgs.setuptools ];
    dependencies = with python.pkgs; [ boto3 mcp uvicorn ];
    nativeCheckInputs = [ python.pkgs.pytestCheckHook python.pkgs.moto ];
  };
in
{
  inherit package;

  # Pushed as oci.garvey.sh/pages-mcp:<tag>, the tag being the package's
  # store hash, so any change to it changes the tag.
  image = pkgs.dockerTools.buildLayeredImage {
    name = "pages-mcp";
    tag = builtins.substring 11 32 package.outPath;
    contents = pkgs.buildEnv {
      name = "pages-mcp-root";
      paths = [ package pkgs.cacert ];
      pathsToLink = [ "/bin" "/etc" ];
    };
    extraCommands = ''
      mkdir -m 1777 tmp
    '';
    config = {
      Entrypoint = [ "/bin/pages-mcp" ];
      User = "1000:1000";
      ExposedPorts."8000/tcp" = { };
      Env = [
        "HOME=/tmp"
        "SSL_CERT_FILE=/etc/ssl/certs/ca-bundle.crt"
        "AWS_CA_BUNDLE=/etc/ssl/certs/ca-bundle.crt"
      ];
    };
  };
}
