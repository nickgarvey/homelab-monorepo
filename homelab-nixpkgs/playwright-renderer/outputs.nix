# HTTP service that renders HTML to PNG using Playwright. Imported by homelab-nixpkgs/outputs.nix.
{ nixpkgs }:
let
  supportedSystems = [ "x86_64-linux" "aarch64-linux" ];
  forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
in
{
  dockerImage = forAllSystems (system:
    let
      pkgs = nixpkgs.legacyPackages.${system};

      pythonEnv = pkgs.python3.withPackages (ps: [ ps.playwright ]);

      fontsConf = pkgs.makeFontsConf {
        fontDirectories = [ pkgs.dejavu_fonts pkgs.noto-fonts-cjk-sans ];
      };

      serverScript = pkgs.writeTextFile {
        name = "playwright-renderer-server";
        destination = "/server.py";
        text = builtins.readFile ./server.py;
      };
    in
    pkgs.dockerTools.buildLayeredImage {
      name = "playwright-renderer";
      tag = "latest";

      contents = pkgs.buildEnv {
        name = "image-root";
        paths = [
          pythonEnv
          serverScript
          pkgs.playwright-driver.browsers
          pkgs.dejavu_fonts
          pkgs.noto-fonts-cjk-sans
          pkgs.busybox
        ];
        pathsToLink = [ "/bin" "/lib" "/share" "/etc" "/" ];
      };

      fakeRootCommands = ''
        mkdir -p /tmp
        chmod 1777 /tmp
      '';
      enableFakechroot = true;

      config = {
        Cmd = [ "${pythonEnv}/bin/python3" "/server.py" ];
        ExposedPorts = { "3000/tcp" = { }; };
        Env = [
          "PLAYWRIGHT_BROWSERS_PATH=${pkgs.playwright-driver.browsers}"
          "PLAYWRIGHT_SKIP_VALIDATE_HOST_REQUIREMENTS=true"
          "TMPDIR=/tmp"
          "FONTCONFIG_FILE=${fontsConf}"
        ];
      };
    }
  );

  devShells = forAllSystems (system:
    let pkgs = nixpkgs.legacyPackages.${system};
    in {
      default = pkgs.mkShell {
        packages = [
          (pkgs.python3.withPackages (ps: [ ps.playwright ]))
        ];
        env = {
          PLAYWRIGHT_BROWSERS_PATH = pkgs.playwright-driver.browsers;
          PLAYWRIGHT_SKIP_VALIDATE_HOST_REQUIREMENTS = "true";
        };
      };
    }
  );
}
