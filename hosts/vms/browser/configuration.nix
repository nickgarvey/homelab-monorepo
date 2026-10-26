# Incus VM on lydia with a desktop and browser that Claude drives (ait epic nix-EGphx).

{ lib, ... }:

let
  inherit (import ../../../modules/networking/lan-hosts.nix) lanHosts;
  host = lib.findFirst (h: h.hostname == "browser") (throw "browser: no lan-hosts.nix entry") lanHosts;
in
{
  imports = [
    ../../../modules/virtualisation/incus-guest.nix
    ../../../modules/networking/networkd.nix
    ./desktop.nix
    ./mcp.nix
  ];

  networking.hostName = "browser";

  # Static IPv6 from lan-hosts.nix, matched by the MAC that lydia's Incus
  # profile pins.
  homelab.network.enable = true;

  # IPv4 is static too, not DHCP. The seed takes the router's static lease
  # first, and the router will not give that address to a second client ID
  # until the seed's lease expires, a day later.
  systemd.network.networks."25-static" = {
    networkConfig.DHCP = lib.mkForce "no";
    address = [ "${host.ipv4}/16" ];
    gateway = [ "10.28.0.1" ];
    dns = [ "10.28.0.1" ];
  };

  system.stateVersion = "26.11";
}
