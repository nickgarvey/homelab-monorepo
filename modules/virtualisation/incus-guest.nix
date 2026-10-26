# Baseline for NixOS guests in Incus VMs: disk layout, boot and agent from nixpkgs' Incus VM module, with this repo's access rules on top.

{ lib, modulesPath, ... }:
{
  imports = [
    "${modulesPath}/virtualisation/incus-virtual-machine.nix"
    ../core/nixos-common.nix
  ];

  nixpkgs.hostPlatform = "x86_64-linux";

  # The nixpkgs image module is written for interactive images: root logs in
  # with an empty password, sshd is socket-activated, and the image carries a
  # channel and a copy of its configuration. Our guests are reached only by
  # SSH key and configured from this flake.
  users.users.root.initialHashedPassword = lib.mkForce "!";
  services.getty.helpLine = lib.mkForce "";
  services.openssh.startWhenNeeded = false;
  system.installer.channel.enable = false;
  installer.cloneConfig = false;

  # No age key on the guests yet, so no sops secrets: Vector needs the
  # remote-write password.
  homelab.metrics.enable = false;
}
