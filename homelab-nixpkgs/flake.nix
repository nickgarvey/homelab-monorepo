{
  description = "Homelab container images";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    playwright-renderer = {
      url = "path:./playwright-renderer";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    ninfer = {
      url = "path:./ninfer";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    thinkrail = {
      url = "path:./thinkrail";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    trmnl-display = {
      url = "path:./trmnl-display";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    lan-table = {
      url = "path:./lan-table";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs = { self, nixpkgs, flake-utils, playwright-renderer, trmnl-display, ninfer, thinkrail, lan-table }:
    flake-utils.lib.eachDefaultSystem (system: {
      packages = {
        playwright-renderer = playwright-renderer.dockerImage.${system};
        trmnl-display = trmnl-display.dockerImage.${system};
        ninfer = ninfer.dockerImage.${system};
        thinkrail = thinkrail.packages.${system}.default;
        lan-table = lan-table.packages.${system}.default;
      };
      devShells.default =
        let pkgs = nixpkgs.legacyPackages.${system}; in
        pkgs.mkShell {
          buildInputs = [ pkgs.skopeo pkgs.jq ];
        };
    });
}
