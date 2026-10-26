# Bootstrap image for Incus VMs on lydia: SSH key, DHCP on the LAN and the Incus agent, enough for the deploy tool to install the VM's own configuration.

{ ... }:
{
  imports = [
    ../../modules/virtualisation/incus-guest.nix
  ];

  networking.hostName = "incus-vm-seed";

  # DHCPv4 and SLAAC on whatever NIC Incus gives us. The router hands each VM
  # its static lease by MAC (modules/networking/lan-hosts.nix), so the VM is
  # reachable by its DNS name before its own configuration is deployed.
  networking.useNetworkd = true;
  # The MAC as client ID, like every LAN host: a VM re-created from the seed
  # then asks as the same client, where a machine-id DUID would be a new one
  # that the router refuses the address to while the old lease lasts.
  systemd.network.networks."99-ethernet-default-dhcp".dhcpV4Config.ClientIdentifier = "mac";

  system.stateVersion = "26.11";
}
