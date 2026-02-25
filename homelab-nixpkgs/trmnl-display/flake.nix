{
  description = "TRMNL eink display service";

  inputs.nixpkgs.url = "github:nixos/nixpkgs/nixos-25.05";

  outputs = { self, nixpkgs, ... }:
    let
      supportedSystems = [ "x86_64-linux" "aarch64-linux" ];
      forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
    in
    {
      dockerImage = forAllSystems (system:
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
              pkgs.dockerTools.fakeNss
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
            ];
          };
        }
      );

      devShells = forAllSystems (system:
        let pkgs = nixpkgs.legacyPackages.${system};
        in {
          default = pkgs.mkShell {
            packages = [
              pkgs.go
              pkgs.gopls
              pkgs.gotools
            ];
          };
        }
      );
    };
}
