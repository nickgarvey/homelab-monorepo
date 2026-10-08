# Ceph run directly on the storage nodes (no Rook): a mon, mgr, OSD and RGW on each of joor/zah/frul.
#
# The cluster is bootstrapped by hand (keys, monmap, OSD mkfs; scripts in
# docs/ceph-bootstrap/); this module only configures and runs the daemons.
# A daemon whose keyring is missing does not start (the NixOS module's
# ConditionPathExists). A recovery copy of the admin keyring is in
# secrets/ceph-admin.yaml, which only ngarvey can decrypt.
#
# Public network (clients, mons, RGW) is the LAN; OSD replication and recovery
# use the Thunderbolt mesh (modules/networking/thunderbolt-mesh.nix).
{ config, lib, pkgs, ... }:

let
  ceph = pkgs.ceph;
  # Ceph major upgrades need an ordered restart (mon, mgr, OSD, RGW), so a
  # nixpkgs bump must not change the major on its own: the assertion below
  # fails evaluation instead. Raise this deliberately when upgrading.
  cephMajor = "20";

  hostname = config.networking.hostName;
  inherit (import ../networking/lan-hosts.nix) lanHosts;
  lanV6 = name: (lib.findFirst (h: h.hostname == name) null lanHosts).ipv6;

  # OSD id and the whole disk it owns, per node.
  nodes = {
    joor = { osd = "0"; disk = "/dev/disk/by-id/nvme-WD_Blue_SN5100_4TB_25459R801411"; };
    zah = { osd = "1"; disk = "/dev/disk/by-id/nvme-WD_Blue_SN5100_4TB_25459R802281"; };
    frul = { osd = "2"; disk = "/dev/disk/by-id/nvme-WD_Blue_SN5100_4TB_25459R802230"; };
  };
  node = nodes.${hostname} or (throw "ceph.nix: ${hostname} is not a Ceph node");
  monNames = lib.attrNames nodes;

  rgwDomain = "rgw.home.garvey.sh";
  rgwCertDir = "/var/lib/acme/${rgwDomain}";
  # Knot (knot-auth container, 10.28.0.6) through the router's NAT64.
  knotViaNat64 = "[64:ff9b::a1c:6]:53";
  cephConf = config.environment.etc."ceph/ceph.conf".source;
in
{
  assertions = [{
    assertion = lib.versions.major ceph.version == cephMajor;
    message = "nixpkgs moved Ceph to ${ceph.version}, but ceph.nix expects major ${cephMajor}. A major upgrade needs an ordered restart (mon, mgr, OSD, RGW); bump cephMajor in modules/services/ceph.nix when doing it.";
  }];

  services.ceph = {
    enable = true;
    global = {
      fsid = "8d324da3-f3ab-40f8-aaa1-71ed33705f75";
      monInitialMembers = lib.concatStringsSep "," monNames;
      monHost = lib.concatMapStringsSep ","
        (n: "[v2:[${lanV6 n}]:3300]") monNames;
      # Only the static addresses: matching the whole LAN /64 lets daemons bind a
      # temporary privacy address, which expires.
      publicNetwork = lib.concatMapStringsSep "," (n: "${lanV6 n}/128") monNames;
      clusterNetwork = "fd2f:b7dd:75bc::/48";
      mgrModulePath = "${ceph.lib}/lib/ceph/mgr";
    };
    extraConfig = {
      "ms bind ipv6" = "true";
      "ms bind ipv4" = "false";
      # msgr2 only. The legacy protocol has no secure mode, so it would be a
      # way around the encryption below. The monmap lists only v2 addresses
      # too (ceph mon set-addrs); ceph-csi talks to the mons on 3300
      # (k8s/vendor/ceph-csi-rbd/values.yaml).
      "ms bind msgr1" = "false";
      # Read by the mons when a pool is created.
      "osd pool default size" = "3";
      "osd pool default min size" = "2";
      # Never mark a down OSD out. With one OSD per host and size 3 there is
      # nowhere to re-replicate to, and an OSD that was out gets a full
      # backfill when it returns instead of catching up from the PG log.
      # Revisit if a fourth storage node is added.
      "mon osd down out interval" = "0";
      # Ceph raises OSD_NO_DOWN_OUT_INTERVAL while the interval is 0, which
      # would hold the cluster at HEALTH_WARN for good.
      "mon warn on osd down out interval zero" = "false";
      # Encrypt every messenger connection (AES-GCM): clients' RBD I/O and the
      # daemons' own traffic cross the LAN, where crc mode would let anyone
      # who can spoof ARP/NDP read and rewrite it. cephx authenticates either
      # way. Every client here speaks msgr2 secure (kernel RBD from 5.11,
      # ceph-csi's librbd). The client picks among the modes the server
      # allows, so the server must allow secure alone for ceph-csi's
      # ms_mode=prefer-crc maps to end up encrypted.
      "ms cluster mode" = "secure";
      "ms service mode" = "secure";
      "ms client mode" = "secure";
      "ms mon cluster mode" = "secure";
      "ms mon service mode" = "secure";
      "ms mon client mode" = "secure";
    };

    mon = { enable = true; daemons = [ hostname ]; package = ceph; };
    mgr = { enable = true; daemons = [ hostname ]; package = ceph; };
    osd = {
      enable = true;
      daemons = [ node.osd ];
      package = ceph;
      # Replaces the module's defaults (FileStore journal size, fixed PG
      # counts); the PG autoscaler sizes pools instead.
      extraConfig = { };
    };
    rgw = { enable = true; daemons = [ "rgw.${hostname}" ]; package = ceph; };
    client = {
      enable = true;
      # HTTPS only, on 443 with this node's certificate for the shared name
      # (below). There is no plain HTTP endpoint: STS replies carry temporary
      # S3 credentials.
      extraConfig."client.rgw.${hostname}" = {
        "rgw frontends" =
          "beast ssl_endpoint=[::]:443"
          + " ssl_certificate=${rgwCertDir}/fullchain.pem ssl_private_key=${rgwCertDir}/key.pem";
        # STS, for AssumeRoleWithWebIdentity with SPIRE's JWT-SVIDs
        # (k8s/manifests/spire/README.md). rgw_sts_key comes from sops (below).
        # rgw_enable_jwks_url_verification stays at its default, off: its
        # thumbprint check connects over IPv4 only, and the JWKS is fetched
        # over HTTPS with the system CAs either way.
        "rgw s3 auth use sts" = "true";
      };
    };
  };

  # rgw_sts_key encrypts the session tokens STS hands out, so every RGW needs
  # the same one. It reaches radosgw through CEPH_ARGS rather than ceph.conf,
  # which is in the Nix store. Changing it only invalidates outstanding
  # temporary credentials.
  sops.secrets.rgw-sts-key = {
    sopsFile = ../../secrets/rgw-sts.yaml;
    key = "sts_key";
  };
  sops.templates."rgw-sts.env" = {
    content = "CEPH_ARGS=--rgw_sts_key=${config.sops.placeholder.rgw-sts-key}";
    restartUnits = [ "ceph-rgw-rgw.${hostname}.service" ];
  };

  # Every node issues its own certificate for rgw.home.garvey.sh (the
  # round-robin S3 name), by DNS-01 against our Knot over RFC2136, as the garage
  # nodes do for theirs (docs/tls.md). The TSIG key may only write TXT at
  # _acme-challenge.rgw (modules/containers/knot-auth.nix). Knot's container is
  # IPv4-only and these nodes are IPv6-only, so lego reaches it through NAT64.
  sops.secrets.rgw-acme-tsig = {
    sopsFile = ../../secrets/rgw-acme.yaml;
    key = "acme_tsig_secret";
  };
  sops.templates."rgw-acme.env".content = ''
    RFC2136_NAMESERVER=${knotViaNat64}
    RFC2136_TSIG_KEY=acme-rgw
    RFC2136_TSIG_ALGORITHM=hmac-sha256.
    RFC2136_TSIG_SECRET=${config.sops.placeholder.rgw-acme-tsig}
  '';
  security.acme = {
    acceptTerms = true;
    defaults.email = "garvey.nick@gmail.com";
    certs.${rgwDomain} = {
      dnsProvider = "rfc2136";
      dnsResolver = knotViaNat64;
      environmentFile = config.sops.templates."rgw-acme.env".path;
      # Same reason as garage.nix: lego's propagation check queries the zone's
      # NS via blocky, whose negative cache outlasts its timeout; Knot is the
      # only authoritative server, so there is nothing to wait for.
      dnsPropagationCheck = false;
      # RGW reads the key as the ceph user.
      group = "ceph";
      # RGW loads its certificate only at start (its reload signal just
      # reopens logs), so restart it on renewal.
      postRun = ''
        ${pkgs.systemd}/bin/systemctl restart ceph-rgw-rgw.${hostname}.service || true
      '';
    };
  };
  systemd.services."ceph-rgw-rgw.${hostname}" = {
    # acme-<name>.service puts a placeholder certificate in place before the
    # first real one is issued, so RGW always has files to load.
    wants = [ "acme-${rgwDomain}.service" ];
    after = [ "acme-${rgwDomain}.service" ];
    restartTriggers = [ cephConf ];
    serviceConfig.AmbientCapabilities = [ "CAP_NET_BIND_SERVICE" ];
    serviceConfig.EnvironmentFile = config.sops.templates."rgw-sts.env".path;
  };

  # The NixOS module does not restart Ceph daemons when ceph.conf changes, and
  # most options there (the messenger modes, RGW's frontends) are read only at
  # start. Restart them on a change; the deploy tool's Ceph gate does one host
  # at a time and waits for every PG to be active+clean.
  systemd.services."ceph-mon-${hostname}".restartTriggers = [ cephConf ];
  systemd.services."ceph-mgr-${hostname}".restartTriggers = [ cephConf ];
  systemd.services."ceph-osd-${node.osd}".restartTriggers = [ cephConf ];

  environment.systemPackages = [ ceph ];

  # Mesh interfaces are already trusted; these are for LAN clients and peers.
  # 9283: the mgr prometheus module, scraped by the k3s cluster's Prometheus.
  networking.firewall.allowedTCPPorts = [ 443 3300 9283 ];
  # OSD and mgr messengers (ms_bind_port_min..max).
  networking.firewall.allowedTCPPortRanges = [ { from = 6800; to = 7568; } ];
}
