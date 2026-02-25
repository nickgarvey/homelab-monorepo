{
  description = "Homelab container images";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    llama-cpp = {
      url = "path:./llama-cpp";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    nickgarvey-com = {
      url = "path:./nickgarvey-com";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    playwright-renderer = {
      url = "path:./playwright-renderer";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    trmnl-display = {
      url = "path:./trmnl-display";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs = { self, nixpkgs, flake-utils, llama-cpp, nickgarvey-com, playwright-renderer, trmnl-display }:
    flake-utils.lib.eachDefaultSystem (system: {
      packages = {
        llama-cpp = llama-cpp.dockerImage.${system};
        nickgarvey-com = nickgarvey-com.dockerImage.${system};
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
