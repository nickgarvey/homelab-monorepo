# Dev shells. Imported by the root flake.nix. firmware/ defines the esp32 shell.
{ self, nixpkgs, ... }:
let
  pkgs = import nixpkgs { system = "x86_64-linux"; };
in
{
  devShells.x86_64-linux = {
    # The repo-wide shell: deploy, plus the k8s/ and packages/ tooling.
    default = pkgs.mkShell {
      buildInputs = with pkgs; [
        python312
        go
        self.packages.x86_64-linux.deploy
        kubectl
        skopeo
        jq
      ];
    };

    makernexus = import ./makernexus.nix { inherit pkgs; };
  };
}
