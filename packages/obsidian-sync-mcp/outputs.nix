# MCP server for an Obsidian LiveSync vault (es617/obsidian-sync-mcp), and its image. Imported by packages/outputs.nix.
{ nixpkgs, system }:
let
  pkgs = nixpkgs.legacyPackages.${system};

  package = pkgs.buildNpmPackage {
    pname = "obsidian-sync-mcp";
    version = "0.10.0";

    src = pkgs.fetchFromGitHub {
      owner = "es617";
      repo = "obsidian-sync-mcp";
      rev = "1a4d53fbb95cd92288e861a34c15c436e2d0a472";
      # Brings lib/livesync-commonlib, which tsup bundles into dist/.
      fetchSubmodules = true;
      hash = "sha256-Bw6wb3hHN04SZdgjhCsa1/O0k941k7YfKp9kXbZFFI4=";
    };

    patches = [ ../../patches/obsidian-sync-mcp-read-only-instructions.patch ];

    npmDepsHash = "sha256-XbNfjgXJnnDhKarfv7LPKQOgcIoU8tllIlAbt6h8MfM=";
    nodejs = pkgs.nodejs_22;
    npmFlags = [ "--ignore-scripts" ];

    meta.mainProgram = "obsidian-sync-mcp";
  };
in
{
  inherit package;

  # Pushed as oci.garvey.sh/obsidian-sync-mcp:<tag>. The tag is the upstream
  # version plus the package's store hash, so a new pin changes the tag in
  # k8s/manifests/mcp-notes too.
  image = pkgs.dockerTools.buildLayeredImage {
    name = "obsidian-sync-mcp";
    tag = "${package.version}-${builtins.substring 11 7 package.outPath}";

    contents = [ pkgs.cacert ];

    fakeRootCommands = ''
      mkdir -p /tmp /data
      chmod 1777 /tmp
      chown 1000:1000 /data
    '';
    enableFakechroot = true;

    config = {
      Cmd = [ (pkgs.lib.getExe package) ];
      User = "1000:1000";
      ExposedPorts = { "8787/tcp" = { }; };
      Env = [
        "DATA_DIR=/data"
        "TMPDIR=/tmp"
      ];
    };
  };
}
