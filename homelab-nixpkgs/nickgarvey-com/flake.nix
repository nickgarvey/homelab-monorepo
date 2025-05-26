{
  description = "Service that acts as the webserver for nickgarvey.com";

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
          nginxConf = ./nginx.conf;
          localDataDir = ./data;

          dataDir = pkgs.runCommand "cp-data" {} ''
            mkdir -p $out/data
            cp -r ${localDataDir}/. $out/data
          '';
        in
          pkgs.dockerTools.buildImage {
            name = "ngarvey-nginx";
            tag = "latest";
            copyToRoot = pkgs.buildEnv {
              name = "image-root";
              paths = [
                pkgs.nginx
                # busybox is just for debugging is needed as nginx runs as nobody
                pkgs.busybox
                # fakeNss is needed as nginx runs as nobody
                pkgs.dockerTools.fakeNss
                dataDir
              ];
            };

            runAsRoot = ''
              #!${pkgs.runtimeShell}
              mkdir -p /var/log/nginx
              mkdir -p /data/
            '';

            config = {
              Cmd = [ "/bin/nginx" "-c" nginxConf ];
              WorkingDir = "/data";
              Volumes = { "/data" = { }; };
            };

          }
      );

      devShells = forAllSystems (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
        in {
          default = pkgs.mkShell {
            packages = [
              pkgs.nginx
            ];
          };
        }
      );
    };
}
