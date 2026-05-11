{
  description = "llama.cpp server builds (vulkan / cuda) for LLM inference";

  inputs.nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs, ... }:
    let
      inherit (nixpkgs) lib;
      supportedSystems = [ "x86_64-linux" ];
      forAllSystems = lib.genAttrs supportedSystems;

      llamaCppOverlay = final: prev: {
        llama-cpp-vulkan = prev.llama-cpp.override { vulkanSupport = true; };
        llama-cpp-cuda   = prev.llama-cpp.override { cudaSupport   = true; };
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
