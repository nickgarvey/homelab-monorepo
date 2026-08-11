{
  description = "Homelab container images";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    playwright-renderer = {
      url = "path:./playwright-renderer";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    trmnl-display = {
      url = "path:./trmnl-display";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs = { self, nixpkgs, flake-utils, playwright-renderer, trmnl-display }:
    flake-utils.lib.eachDefaultSystem (system: {
      packages = {
        playwright-renderer = playwright-renderer.dockerImage.${system};
        trmnl-display = trmnl-display.dockerImage.${system};
      };
      devShells.default =
        let pkgs = nixpkgs.legacyPackages.${system}; in
        pkgs.mkShell {
          buildInputs = [ pkgs.skopeo pkgs.jq ];
        };
    });
}
