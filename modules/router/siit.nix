# Jool SIIT in its own network namespace: gives the IPv6-only k8s HAProxy SNI router an IPv4 address on the LAN.
#
# Stateless 1:1 translation (an EAMT entry), separate from the stateful NAT64
# instance in nat64.nix: that one routes 64:ff9b::/96 and owns no LAN IPv4.
# Running Jool in its own namespace keeps its netfilter hook from bypassing
# the main namespace's nftables, as in nat64.nix.
#
# Topology:
#   main ns:  siit0 (10.99.0.5/30, fd99::5/126)  ←veth→  siit1 (10.99.0.6/30, fd99::6/126)  :siit ns
#
# Flow (gateway values in modules/networking/dns.nix ipv4Gateway):
#   IPv4 client → 10.28.0.8 (router answers ARP via a proxy neighbour entry)
#   → route 10.28.0.8/32 via siit0 → Jool: dst 10.28.0.8 → HAProxy LB v6 (EAMT),
#     src 10.28.x.y → <translationPrefix>::10.28.x.y (pool6)
#   → back out siit1 → main ns forwards to the LB on br-lan
#   Replies to <translationPrefix> route via siit0 and translate in reverse.

{ config, lib, pkgs, ... }:

let
  cfg = config.routerConfig;
  gw = (import ../networking/dns.nix { inherit lib; }).ipv4Gateway;
  jool-cli = pkgs.jool-cli;

  joolConf = (pkgs.formats.json { }).generate "jool-siit.conf" {
    instance = "default";
    framework = "netfilter";
    global.pool6 = gw.translationPrefix;
    eamt = [
      { "ipv6 prefix" = "${gw.v6}/128"; "ipv4 prefix" = "${gw.v4}/32"; }
    ];
  };
in
{
  config = {
    # The kernel module package and jool-cli come from nat64.nix; jool-cli
    # ships jool_siit alongside jool.
    systemd.services.jool-siit = {
      description = "Jool SIIT in network namespace";
      after = [ "network-online.target" ];
      wants = [ "network-online.target" ];
      wantedBy = [ "multi-user.target" ];
      path = [ pkgs.iproute2 pkgs.procps jool-cli pkgs.kmod ];

      serviceConfig = {
        Type = "oneshot";
        RemainAfterExit = true;
        Restart = "on-failure";
        RestartSec = 10;
        # Clear any leftovers from a partial start; ExecStop never runs when
        # ExecStart fails, and "ip netns add" on an existing namespace is fatal.
        ExecStartPre = pkgs.writeShellScript "jool-siit-pre" ''
          ip netns exec siit jool_siit instance remove 2>/dev/null || true
          ip neigh del proxy ${gw.v4} dev ${cfg.lanInterface} 2>/dev/null || true
          ip route del ${gw.v4}/32 2>/dev/null || true
          ip -6 route del ${gw.translationPrefix} 2>/dev/null || true
          ip link del siit0 2>/dev/null || true
          ip netns del siit 2>/dev/null || true
        '';
        ExecStart = pkgs.writeShellScript "jool-siit-start" ''
          set -e

          ip netns add siit
          ip link add siit0 type veth peer name siit1
          ip link set siit1 netns siit

          ip addr add 10.99.0.5/30 dev siit0
          ip addr add fd99::5/126 dev siit0
          ip link set siit0 up

          ip netns exec siit ip link set lo up
          ip netns exec siit ip link set siit1 up
          ip netns exec siit ip addr add 10.99.0.6/30 dev siit1
          ip netns exec siit ip addr add fd99::6/126 dev siit1
          ip netns exec siit ip route add default via 10.99.0.5 dev siit1
          ip netns exec siit ip -6 route add default via fd99::5 dev siit1
          ip netns exec siit sysctl -w net.ipv6.conf.all.forwarding=1
          ip netns exec siit sysctl -w net.ipv4.ip_forward=1

          modprobe jool_siit
          ip netns exec siit jool_siit file handle ${joolConf}

          # Route traffic in and claim the address on the LAN only once
          # translation is up, so a failed instance leaves the gateway address
          # unanswered rather than blackholed. A proxy neighbour entry, not a local
          # address: a local address would make the router keep the traffic.
          ip route add ${gw.v4}/32 via 10.99.0.6 dev siit0
          ip -6 route add ${gw.translationPrefix} via fd99::6 dev siit0
          ip neigh add proxy ${gw.v4} dev ${cfg.lanInterface}
        '';
        ExecStop = pkgs.writeShellScript "jool-siit-stop" ''
          ip neigh del proxy ${gw.v4} dev ${cfg.lanInterface} || true
          ip route del ${gw.v4}/32 || true
          ip -6 route del ${gw.translationPrefix} || true
          ip netns exec siit jool_siit instance remove || true
          ip link del siit0 || true
          ip netns del siit || true
        '';
      };
    };
  };
}
