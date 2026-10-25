# The LAN's NTP server: chrony on the router, synced only from NTS-authenticated public servers.
#
# Every other host follows this one (modules/core/time.nix). Upstream is
# limited to NTS (Network Time Security), so an on-path attacker cannot shift
# the LAN's clocks by spoofing NTP replies: that would break TLS validity
# checks, TOTP and the Ceph mons. Four servers from three operators
# (Cloudflare, Netnod, PTB), so one going away or misbehaving is outvoted.
# NTS-KE uses TCP 4460 out; nftables.nix filters no outgoing traffic. The
# router reaches upstreams natively (IPv4 or the HE tunnel), not through NAT64
# as the IPv6-only hosts would.
#
# Clients on the LAN are served plain NTP (UDP 123, opened in nftables.nix for
# the LAN interface only). With no reachable upstream, chrony keeps serving
# from its learned drift, and clients also have the pool as a fallback.
#
# NTS checks the servers' certificates against the system clock, so a router
# whose RTC is badly wrong cannot sync until it is fixed. Clients then fall
# back to the pool.
{ lib, ... }:
{
  services.chrony = {
    servers = lib.mkForce [
      "time.cloudflare.com"
      "nts.netnod.se"
      "ptbtime1.ptb.de"
      "ptbtime2.ptb.de"
    ];
    serverOption = lib.mkForce "iburst";
    enableNTS = true;
    extraConfig = lib.mkForce ''
      allow 10.28.0.0/16
      allow 2001:470:482f::/48
    '';
  };
}
