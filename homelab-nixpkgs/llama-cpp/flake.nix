{
  description = "llama.cpp server builds (vulkan / cuda) for LLM inference";

  inputs.nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs, ... }:
    let
      inherit (nixpkgs) lib;
      supportedSystems = [ "x86_64-linux" "aarch64-linux" ];
      forAllSystems = lib.genAttrs supportedSystems;

      # Override llama-cpp to use version 8149
      llamaCppOverlay = final: prev: {
        llama-cpp = prev.llama-cpp.overrideAttrs (oldAttrs: rec {
          version = "8149";
          src = prev.fetchFromGitHub {
            owner = "ggml-org";
            repo = "llama.cpp";
            tag = "b${version}";
            hash = "sha256-E1Qm6a31v0MPyaj47rB1PL0XzsXNSqBuBYJmJJ5nHtE=";
            leaveDotGit = true;
            postFetch = ''
              git -C "$out" rev-parse --short HEAD > $out/COMMIT
              find "$out" -name .git -print0 | xargs -0 rm -rf
            '';
          };
          npmDepsHash = "sha256-FKjoZTKm0ddoVdpxzYrRUmTiuafEfbKc4UD2fz2fb8A=";
          npmDeps = prev.fetchNpmDeps {
            name = "${oldAttrs.pname}-${version}-npm-deps";
            inherit src;
            inherit (oldAttrs) patches;
            preBuild = ''
              pushd ${oldAttrs.npmRoot}
            '';
            hash = "sha256-FKjoZTKm0ddoVdpxzYrRUmTiuafEfbKc4UD2fz2fb8A=";
          };
        });
        llama-cpp-vulkan = final.llama-cpp.override { vulkanSupport = true; };
        llama-cpp-cuda   = final.llama-cpp.override { cudaSupport   = true; };
      };
    in
    {
      packages = forAllSystems (system:
        let
          pkgs = import nixpkgs {
            inherit system;
            overlays = [ llamaCppOverlay ];
          };
          pkgsCuda =
            if system == "x86_64-linux"
            then import nixpkgs {
              inherit system;
              config = { allowUnfree = true; cudaSupport = true; };
              overlays = [ llamaCppOverlay ];
            }
            else null;
        in {
          default = pkgs.llama-cpp-vulkan;
          vulkan  = pkgs.llama-cpp-vulkan;
        } // lib.optionalAttrs (pkgsCuda != null) {
          cuda = pkgsCuda.llama-cpp-cuda;
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
