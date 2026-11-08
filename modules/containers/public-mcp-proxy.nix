# Public :443 front door for the MCP gateway: a TLS-passthrough TCP relay from the WAN IPv4 to the IPv6-only gateway LoadBalancer.
#
# Claude's connectors (claude.ai, Duet) reach us from Anthropic's IPv4-only
# egress range, and the cluster is IPv6-only. The router DNATs WAN:443 to this
# container, but only from that range (modules/router/nftables.nix); this
# container then relays the raw TCP stream to the gateway, so TLS is terminated
# by the gateway's Caddy and never here.
#
#   Anthropic 160.79.104.0/21 -> WAN 64.186.8.205:443 -> DNAT 10.28.0.7:443
#     -> systemd-socket-proxyd -> [2001:470:482f:2::5013]:443 (k8s/manifests/mcp)
#
# Same shape as public-dns-proxy: a dedicated LAN IPv4 on br-lan for the DNAT
# target, a GUA for reaching the LB pool. The gateway sees this container's
# address as the client, not the caller's; the allowlist lives in nftables.

{ config, lib, pkgs, ... }:

{
  imports = [ ./common.nix ];

  nspawn.network.public-mcp-proxy = {
    attachment = "bridge";
    hostBridge = "br-lan";
    localAddress = "10.28.0.7/16";
    ipv4Gateway = "10.28.0.1";
    ipv4Nameservers = [ "10.28.0.1" ];
    # Replies go back to Anthropic's public IPs, so the container needs a real
    # IPv4 default route (conntrack un-DNATs them at the router).
    ipv4DefaultRoute = true;
    localAddress6 = "2001:470:482f::4430/64";
    hostBridgeAddress = "2001:470:482f::1";
  };

  containers.public-mcp-proxy = {
    config = { config, pkgs, ... }: {
      # FreeBind: the socket may be created before eth0 has 10.28.0.7.
      systemd.sockets.mcp-relay = {
        wantedBy = [ "sockets.target" ];
        listenStreams = [ "10.28.0.7:443" ];
        socketConfig.FreeBind = true;
      };

      # One process for all connections; each upstream connection is opened
      # when its client connects, so it always gets eth0's GUA as its source.
      # The target is unbracketed on purpose: socket-proxyd splits host and
      # port at the last ':' and hands the host to getaddrinfo verbatim, so
      # "[addr]:443" fails to resolve.
      systemd.services.mcp-relay = {
        requires = [ "mcp-relay.socket" ];
        after = [ "mcp-relay.socket" "network-addresses-eth0.service" "nspawn-intrasite-route6.service" ];
        wants = [ "network-addresses-eth0.service" "nspawn-intrasite-route6.service" ];
        serviceConfig = {
          ExecStart = "${config.systemd.package}/lib/systemd/systemd-socket-proxyd 2001:470:482f:2::5013:443";
          DynamicUser = true;
          PrivateTmp = true;
          ProtectSystem = "strict";
          ProtectHome = true;
          NoNewPrivileges = true;
        };
      };
    };
  };
}
