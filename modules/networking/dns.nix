# DNS records for the local zone (home.garvey.sh).
# Consumed by: blocky DNS (router).
#
# Split from lan-hosts.nix so that "physical host" data (MAC → IP for DHCP)
# is separate from "DNS records" data (what names point where). Supports
# multi-IP (e.g. k3s-api HA round-robin) natively via list-valued fields,
# and references into lanHosts to avoid duplicating IPs.
{ lib }:

let
  inherit (import ./lan-hosts.nix) lanHosts;
  hostV6 = name:
    let h = lib.findFirst (h: h.hostname == name)
              (throw "dns.nix: unknown host '${name}'") lanHosts;
    in h.ipv6;
in rec {
  # A/AAAA records. v4/v6 are lists; empty list = no record of that family.
  records = {
    router        = { v4 = [ "10.28.0.1" ];    v6 = [ "2001:470:482f::1" ]; };
    dragonsreach  = { v4 = [ "10.28.0.1" ];    v6 = [ "2001:470:482f::1" ]; };
    frigate       = { v4 = [ "10.28.12.109" ]; v6 = [ "2001:470:482f:200::3" ]; };
    # Incus API and web UI on lydia :8443, with its own Let's Encrypt certificate
    # (modules/virtualisation/incus.nix).
    incus         = { v4 = [ "10.28.12.108" ]; v6 = [ (hostV6 "lydia") ]; };
    smb           = { v4 = [ "10.28.12.110" ]; v6 = [ "2001:470:482f::14" ]; };
    # Both garage nodes. Clients round-robin; any node serves the whole S3 API.
    # Peer-to-peer RPC does not use this — see modules/containers/garage.nix.
    garage        = { v4 = []; v6 = [ "2001:470:482f:200::2" "2001:470:482f:202::2" ]; };
    storj-gateway = { v4 = [ "10.28.0.3" ]; v6 = [ "2001:470:482f:300::2" ]; };
    # UniFi controller. New or factory-reset devices look up "unifi" to find it.
    # v4-only: the container has IPv6 disabled (modules/containers/unifi.nix).
    unifi         = { v4 = [ "10.28.0.4" ]; v6 = []; };
    k3s-api       = { v4 = []; v6 = map hostV6 [ "fus" "ro" "dah" ]; };
    # The three Ceph RGW gateways (S3 over HTTPS on 443). Clients round-robin; any
    # gateway serves the whole S3 API. See modules/services/ceph.nix.
    rgw           = { v4 = []; v6 = map hostV6 [ "joor" "zah" "frul" ]; };
    # trmnl-display keeps A+AAAA: the ESP32 client is IPv4-only and hits an
    # IPv4→IPv6 proxy at 10.28.0.2. The AAAA is for dual-stack clients.
    trmnl-display = { v4 = [ "10.28.0.2" ]; v6 = [ "2001:470:482f:2::5" ]; };
    # A Caddy sidecar in the anki pod terminates TLS on 443 off this LB IP.
    anki          = { v4 = []; v6 = [ "2001:470:482f:2::5003" ]; };
    # CouchDB for Obsidian LiveSync: a Caddy sidecar terminates TLS on 443 off
    # this LB IP (k8s/manifests/couchdb).
    # obsidian-livesync's A record is the IPv4 gateway (ipv4Gateway below), for
    # IPv4-only LAN clients (Android): HAProxy routes the name to CouchDB by SNI.
    obsidian-livesync = { v4 = [ ipv4Gateway.v4 ]; v6 = [ "2001:470:482f:2::5004" ]; };
    # Authelia (logins, OIDC and forward auth); its Caddy sidecar terminates TLS
    # on 443 (k8s/manifests/authelia). The A record is the WAN front door: the
    # router forwards WAN :443 to the HAProxy SNI router
    # (k8s/manifests/ipv4-gateway), which lets this name through from any
    # source. LAN clients get the overrides in garveyShOverrides below.
    auth          = { v4 = [ wanIPv4 ]; v6 = [ "2001:470:482f:2::5005" ]; };
    # SPIRE's OIDC discovery provider serves the trust domain's JWKS on 443 off
    # this LB IP (k8s/manifests/spire).
    spiffe-oidc   = { v4 = []; v6 = [ "2001:470:482f:2::5007" ]; };
    # The SPIRE server API, for NixOS hosts' agents; it presents its SVID, not
    # a certificate for this name (k8s/manifests/spire).
    spire         = { v4 = []; v6 = [ "2001:470:482f:2::5016" ]; };
    # Celler Nix binary cache; Caddy sidecar terminates TLS on 443
    # (k8s/manifests/celler).
    celler        = { v4 = []; v6 = [ "2001:470:482f:2::5008" ]; };
    # Prometheus; Caddy sidecar terminates TLS on 443 and authenticates remote
    # write (k8s/manifests/prometheus).
    prometheus    = { v4 = []; v6 = [ "2001:470:482f:2::5009" ]; };
    # The Matrix Authentication Service, every Matrix login; it terminates TLS
    # on 443 itself (k8s/manifests/matrix). A record as for auth above, but
    # HAProxy lets every WAN source through: Element X refreshes its tokens
    # here from wherever the phone is.
    matrix-auth   = { v4 = [ wanIPv4 ]; v6 = [ "2001:470:482f:2::5011" ]; };
    # Nightscout (CGM data from Loop); Caddy sidecar terminates TLS on 443
    # (k8s/manifests/nightscout).
    nightscout    = { v4 = []; v6 = [ "2001:470:482f:2::5012" ]; };
    # The MCP gateway: Caddy that checks Authelia tokens in front of the MCP
    # modules (k8s/manifests/mcp). A record as for auth above.
    mcp           = { v4 = [ wanIPv4 ]; v6 = [ "2001:470:482f:2::5013" ]; };
    # Static pages from the RGW bucket `pages`, behind an Authelia login; a
    # Caddy terminates TLS on 443 (k8s/manifests/pages). A record as for
    # matrix-auth above: HAProxy lets every WAN source through.
    pages         = { v4 = [ wanIPv4 ]; v6 = [ "2001:470:482f:2::5017" ]; };
    # OpenBao, the private CA's issuing tier, on 8200 (modules/services/openbao.nix).
    # Clients use the shared name; any node serves the whole API (standbys
    # forward to the active node). The per-node names are the Raft peers' API
    # addresses, and each node's certificate covers its own and the shared one.
    openbao       = { v4 = []; v6 = map hostV6 [ "fus" "ro" "dah" ]; };
    openbao-fus   = { v4 = []; v6 = [ (hostV6 "fus") ]; };
    openbao-ro    = { v4 = []; v6 = [ (hostV6 "ro") ]; };
    openbao-dah   = { v4 = []; v6 = [ (hostV6 "dah") ]; };
  };

  # The router's DHCP-assigned WAN IPv4: ns1's address, and the public front
  # door for mcp, auth, matrix-auth and pages above. Nothing updates it if the lease
  # changes; modules/router/wan-dns-drift.nix alerts when it no longer matches.
  wanIPv4 = "64.186.8.205";

  # IPv4 front door for the IPv6-only k8s services: the router's Jool SIIT
  # (modules/router/siit.nix) maps v4 1:1 onto v6, the HAProxy SNI router's LB
  # address (k8s/manifests/ipv4-gateway). IPv4 clients reach HAProxy with
  # source addresses inside translationPrefix (their IPv4 in the last 32 bits).
  ipv4Gateway = {
    v4 = "10.28.0.8";
    v6 = "2001:470:482f:2::5014";
    translationPrefix = "2001:470:482f:64::/96";
  };

  # CNAMEs (targets are FQDNs with trailing dot).
  cnames = { };

  # Names cert-manager gets Let's Encrypt certificates for, validated by DNS-01
  # in this zone (letsencrypt-rfc2136-prod, k8s/manifests/cert-manager). Its
  # TSIG key (acme-cert-manager in modules/containers/knot-auth.nix) may write
  # TXT only at the challenge names derived from these lists, and the
  # cert-manager-acme-names flake check fails if they differ from the Certificates.
  #   home:     <name>.home.garvey.sh; the challenge is
  #             _acme-challenge.<name>.home.garvey.sh, here.
  #   garveySh: <name>.garvey.sh; Cloudflare CNAMEs
  #             _acme-challenge.<name>.garvey.sh to <name>.challenge.home.garvey.sh,
  #             where cert-manager writes the TXT (cnameStrategy: Follow).
  certManagerAcme = {
    home = [
      "anki" "auth" "celler" "matrix-auth" "mcp" "nightscout"
      "obsidian-livesync" "pages" "prometheus" "spiffe-oidc"
    ];
    garveySh = [ "jellyfin" "matrix" "oci" "temporal" ];
  };

  # Split-horizon overrides: public (garvey.sh) names answered internally with
  # in-cluster LB IPs, so TLS-verified internal access name-matches the cert
  # instead of hairpinning to the public IP. Keys are full FQDNs. Add future
  # public-named services here to keep this declarative. Everything else under
  # garvey.sh still resolves via the normal upstream (public) path.
  garveyShOverrides = {
    "oci.garvey.sh" = "2001:470:482f:2::5000";
    # The A record is the IPv4 gateway (ipv4Gateway above): HAProxy routes the
    # name to Jellyfin by SNI. IPv6 clients keep going straight to the LB.
    "jellyfin.garvey.sh" = "${ipv4Gateway.v4},2001:470:482f:2::5001";
    "temporal.garvey.sh" = "2001:470:482f:2::5002";
    # Synapse and MAS (k8s/manifests/matrix). Publicly, both have only an A
    # record, wanIPv4, which HAProxy (k8s/manifests/ipv4-gateway) passes from
    # the WAN. The router rejects LAN traffic to its own :443, so LAN clients
    # get the IPv4 gateway (HAProxy routes both names by SNI) and the LB AAAA.
    # Without an A here, blocky forwards A queries upstream and returns wanIPv4.
    "matrix.garvey.sh" = "${ipv4Gateway.v4},2001:470:482f:2::5010";
    "matrix-auth.home.garvey.sh" = "${ipv4Gateway.v4},2001:470:482f:2::5011";
    # As for matrix-auth: the Authelia portal (k8s/manifests/authelia).
    "auth.home.garvey.sh" = "${ipv4Gateway.v4},2001:470:482f:2::5005";
    # As for matrix-auth (k8s/manifests/pages).
    "pages.home.garvey.sh" = "${ipv4Gateway.v4},2001:470:482f:2::5017";
  };
}
