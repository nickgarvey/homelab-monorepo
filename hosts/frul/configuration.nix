{ config, lib, pkgs, inputs, ... }:
{
  imports = [
    ./hardware-configuration.nix
    ../../modules/k3s/k3s-common.nix
    ../../modules/core/nixos-common.nix
    ../../modules/networking/thunderbolt-mesh.nix
    ../../modules/services/ceph.nix
    ../../modules/services/reboot-watchdog.nix
  ];

  networking.hostName = "frul";

  # Worker only: the control plane (and etcd) stays on fus/ro/dah.
  services.k3s.role = "agent";

  # The newest stable kernel for this Wildcat Lake box (AZW EQi): better
  # platform support (xe graphics, USB4, power management) than the 6.18 LTS.
  # joor, zah and frul run the same kernel; the Thunderbolt mesh has only been
  # seen to misbehave between mixed kernels.
  boot.kernelPackages = pkgs.linuxPackages_latest;
}
