# The homelab monorepo's one flake. Each top-level directory defines its
# outputs in its own outputs.nix; this file holds the shared inputs and merges
# those outputs.
{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

    disko = {
      url = "github:nix-community/disko";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    sops-nix = {
      url = "github:Mic92/sops-nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    home-manager = {
      url = "github:nix-community/home-manager";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    helium-browser = {
      url = "github:ominit/helium-browser-flake";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    microvm = {
      url = "github:microvm-nix/microvm.nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    nixos-hardware = {
      url = "github:NixOS/nixos-hardware/master";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    claude-code-nix = {
      url = "github:sadjow/claude-code-nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    codex-cli-nix = {
      url = "github:sadjow/codex-cli-nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    pi-nix = {
      url = "github:lukasl-dev/pi.nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    # Nix binary cache server and client (packages/celler, k8s/manifests/celler).
    celler = {
      url = "github:celler-cache/celler";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.nixpkgs-stable.follows = "nixpkgs";
    };

    nixos-raspberrypi.url = "github:nvmd/nixos-raspberrypi/main";

    # Cartographer V3 klipper plugin (the current/active one — newer than
    # the legacy Cartographer3D/cartographer-klipper loose-script repo,
    # this is the pip-distributed proper python package that the official
    # docs target). Consumed via overlay on services.klipper.package in
    # hosts/skyforge/configuration.nix.
    cartographer3d-plugin = {
      url = "github:Cartographer3D/cartographer3d-plugin";
      flake = false;
    };

    # ESP-IDF for firmware/. Its packages are built against the
    # nixpkgs in its own lock, which pins the firmware toolchain.
    nixpkgs-esp-dev.url = "github:mirrexagon/nixpkgs-esp-dev";
  };

  outputs = inputs:
    let
      lib = inputs.nixpkgs.lib;

      # Merges the directories' outputs down to <output>.<system>.<name>,
      # throwing when two directories define the same name.
      disjointMerge = depth: path: sets:
        lib.zipAttrsWith (name: values:
          let p = path ++ [ name ]; in
          if builtins.length values == 1 then builtins.head values
          else if depth <= 1 then throw "flake output ${lib.concatStringsSep "." p} is defined by more than one directory"
          else disjointMerge (depth - 1) p values
        ) sets;
    in
    disjointMerge 3 [ ] [
      (import ./hosts/outputs.nix inputs)
      (import ./devshells/outputs.nix inputs)
      (import ./packages/outputs.nix inputs)
      (import ./firmware/outputs.nix inputs)
      (import ./agents/outputs.nix inputs)
    ];
}
