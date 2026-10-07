# TRMNL eink display service. Imported by packages/outputs.nix.
{ nixpkgs, system }:
let
  pkgs = nixpkgs.legacyPackages.${system};

  trmnlDisplay = pkgs.buildGoModule {
    pname = "trmnl-display";
    version = "0.1.0";
    src = ./.;
    vendorHash = "sha256-cgQ++eoQkyf6iT4/IANFm28C0EezkDT7cDGCBrmfHnc=";
  };
in
pkgs.dockerTools.buildLayeredImage {
  name = "trmnl-display";
  tag = "latest";

  contents = pkgs.buildEnv {
    name = "image-root";
    paths = [
      trmnlDisplay
      pkgs.cacert
      pkgs.busybox
      pkgs.tzdata
    ];
    pathsToLink = [ "/bin" "/etc" "/share" ];
  };

  fakeRootCommands = ''
    mkdir -p /config /tmp
    chmod 1777 /tmp
  '';
  enableFakechroot = true;

  config = {
    Cmd = [ "/bin/server" "--config" "/config/config.yaml" ];
    ExposedPorts = { "8080/tcp" = { }; };
    Volumes = { "/config" = { }; };
    Env = [
      "SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
      "TZDIR=${pkgs.tzdata}/share/zoneinfo"
    ];
  };
}
