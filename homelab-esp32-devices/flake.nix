{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-25.11";
    nixpkgs-esp-dev.url = "github:mirrexagon/nixpkgs-esp-dev";
  };

  outputs = { self, nixpkgs, nixpkgs-esp-dev }: let
    system = "x86_64-linux";
    pkgs = import nixpkgs {
      inherit system;
      overlays = [ nixpkgs-esp-dev.overlays.default ];
      config.permittedInsecurePackages = [ "python3.13-ecdsa-0.19.1" ];
    };
  in {
    devShells.${system}.default = pkgs.mkShell {
      buildInputs = [
        pkgs.esp-idf-full
        pkgs.gcc
        pkgs.cmake
        pkgs.ninja
      ];
    };
  };
}
