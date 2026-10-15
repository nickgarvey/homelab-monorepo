# hosts outputs: every NixOS configuration. Imported by the root flake.nix.
inputs@{ nixpkgs, disko, sops-nix, nixos-hardware, microvm, nixos-raspberrypi, ... }:
let
  # The k3s nodes share one shape: disko + sops, and the host's
  # configuration.nix and disk-config.nix.
  mkK3sNode = name: nixpkgs.lib.nixosSystem {
    specialArgs = { inherit inputs; };
    modules = [
      disko.nixosModules.disko
      sops-nix.nixosModules.sops
      ./${name}/configuration.nix
      ./${name}/disk-config.nix
    ];
  };
  k3sNodes = nixpkgs.lib.genAttrs [ "fus" "ro" "dah" "joor" "zah" "frul" ] mkK3sNode;
in
{
  # These are all NixOS configurations
  nixosConfigurations = {
    # Workstation (MSI X870E / 9950X3D / RTX 5090)
    talos = nixpkgs.lib.nixosSystem {
      specialArgs = { inherit inputs; };
      modules = [
        disko.nixosModules.disko
        ./talos/configuration.nix
        ./talos/disk-config.nix
      ];
    };

    # Framework 13 Laptop
    dovahkiin = nixpkgs.lib.nixosSystem {
      specialArgs = { inherit inputs; };
      modules = [
        disko.nixosModules.disko
        sops-nix.nixosModules.sops
        nixos-hardware.nixosModules.framework-amd-ai-300-series
        ./dovahkiin/configuration.nix
        ./dovahkiin/disk-config.nix
      ];
    };

    # TV PC (ASUS Zenbook S 16, broken internal panel, drives the living-room TV)
    guevenne = nixpkgs.lib.nixosSystem {
      specialArgs = { inherit inputs; };
      modules = [
        disko.nixosModules.disko
        nixos-hardware.nixosModules.common-cpu-amd-pstate
        nixos-hardware.nixosModules.common-pc-laptop
        nixos-hardware.nixosModules.common-pc-laptop-ssd
        ./guevenne/configuration.nix
        ./guevenne/disk-config.nix
      ];
    };

    # Router
    dragonsreach = nixpkgs.lib.nixosSystem {
      specialArgs = { inherit inputs; };
      modules = [
        disko.nixosModules.disko
        sops-nix.nixosModules.sops
        ./dragonsreach/configuration.nix
        ./dragonsreach/disk-config.nix
      ];
    };

    # Framework Desktop
    wabbajack = nixpkgs.lib.nixosSystem {
      specialArgs = { inherit inputs; };
      modules = [
        disko.nixosModules.disko
        nixos-hardware.nixosModules.framework-desktop-amd-ai-max-300-series
        sops-nix.nixosModules.sops
        ./wabbajack/configuration.nix
        ./wabbajack/disk-config.nix
      ];
    };

    # Skyforge: Raspberry Pi 5 running Klipper for the Voron Trident printer.
    # Uses nvmd/nixos-raspberrypi's pinned nixpkgs (NOT our top-level
    # `nixpkgs` input) because nvmd's vendor-kernel/firmware overlays expect
    # matching userspace versions — mixing nixpkgs versions trips the
    # "kernel module and userspace tooling versions are not matching"
    # assertion (wireguard/zfs/etc).
    skyforge = nixos-raspberrypi.lib.nixosSystem {
      specialArgs = inputs // { inherit inputs; };
      modules = [
        sops-nix.nixosModules.sops
        nixos-raspberrypi.nixosModules.raspberry-pi-5.base
        nixos-raspberrypi.nixosModules.sd-image
        ./skyforge/configuration.nix
      ];
    };

    # Lydia server
    lydia = nixpkgs.lib.nixosSystem {
      specialArgs = { inherit inputs; };
      modules = [
        disko.nixosModules.disko
        sops-nix.nixosModules.sops
        microvm.nixosModules.host
        ./lydia/configuration.nix
        ./lydia/disk-config.nix
      ];
    };

    # Live boot ISO for installation and rescue
    live-iso = nixpkgs.lib.nixosSystem {
      specialArgs = { inherit inputs; };
      modules = [
        ./live-iso/configuration.nix
      ];
    };

    # Same live system, as PXE netboot artifacts served from lydia
    live-netboot = nixpkgs.lib.nixosSystem {
      specialArgs = { inherit inputs; };
      modules = [
        ./live-iso/netboot.nix
      ];
    };
  }
  # K3s nodes
  // k3sNodes;

  checks.x86_64-linux.cert-manager-acme-names = import ../modules/networking/cert-manager-acme-check.nix {
    pkgs = nixpkgs.legacyPackages.x86_64-linux;
    inherit (nixpkgs) lib;
  };
}
