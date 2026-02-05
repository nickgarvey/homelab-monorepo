{
  description = "llama.cpp server with Vulkan support for LLM inference";

  inputs.nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs, ... }:
    let
      supportedSystems = [ "x86_64-linux" "aarch64-linux" ];
      forAllSystems = nixpkgs.lib.genAttrs supportedSystems;

      # Override llama-cpp to use version 7938
      llamaCppOverlay = final: prev: {
        llama-cpp = prev.llama-cpp.overrideAttrs (oldAttrs: rec {
          version = "7938";
          src = prev.fetchFromGitHub {
            owner = "ggml-org";
            repo = "llama.cpp";
            tag = "b${version}";
            hash = "sha256-6Lf8RJbK4ar568ZenCRA/rOUtw3U89LaSodcfytIpoE=";
            leaveDotGit = true;
            postFetch = ''
              git -C "$out" rev-parse --short HEAD > $out/COMMIT
              find "$out" -name .git -print0 | xargs -0 rm -rf
            '';
          };
        });
        llama-cpp-vulkan = final.llama-cpp.override { vulkanSupport = true; };
      };
    in
    {
      dockerImage = forAllSystems (system:
        let
          pkgs = import nixpkgs {
            inherit system;
            overlays = [ llamaCppOverlay ];
          };
        in
          pkgs.dockerTools.buildImage {
            name = "zot.home.arpa/llama-cpp-vulkan-ngarvey";
            tag = "latest";
            copyToRoot = pkgs.buildEnv {
              name = "image-root";
              paths = [
                pkgs.llama-cpp-vulkan
                # Vulkan runtime dependencies
                pkgs.vulkan-loader
                pkgs.mesa
                # For debugging if needed
                pkgs.busybox
                # Needed for user resolution
                pkgs.dockerTools.fakeNss
              ];
              pathsToLink = [ "/bin" "/lib" "/share" "/etc" ];
            };

            runAsRoot = ''
              #!${pkgs.runtimeShell}
              mkdir -p /models
              mkdir -p /tmp
            '';

            config = {
              Cmd = [ "/bin/llama-server" "--host" "0.0.0.0" "--port" "8080" ];
              WorkingDir = "/models";
              Volumes = { "/models" = { }; };
              ExposedPorts = { "8080/tcp" = { }; };
              Env = [
                "VK_ICD_FILENAMES=/share/vulkan/icd.d/radeon_icd.x86_64.json"
              ];
            };
          }
      );

      packages = forAllSystems (system:
        let
          pkgs = import nixpkgs {
            inherit system;
            overlays = [ llamaCppOverlay ];
          };
        in {
          default = pkgs.llama-cpp-vulkan;
        }
      );

      devShells = forAllSystems (system:
        let
          pkgs = import nixpkgs {
            inherit system;
            overlays = [ llamaCppOverlay ];
          };
        in {
          default = pkgs.mkShell {
            packages = [
              pkgs.llama-cpp-vulkan
              pkgs.vulkan-tools
            ];
          };
        }
      );
    };
}
