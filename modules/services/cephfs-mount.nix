# Mounts the CephFS filesystem `local-drive` from the Ceph cluster (joor/zah/frul) at ~/local-drive, with the kernel client and a systemd automount.
#
# Each client host logs in as its own cephx user, client.<host>, read-write on
# the whole filesystem, so one host's key can be revoked alone. Adding a host:
#   1. On a Ceph node: docs/ceph-bootstrap/06-cephfs.sh client <host>.
#   2. A .sops.yaml rule for secrets/cephfs/<host>.yaml with the host's
#      identity, and the key piped in without showing it:
#        { printf 'key: '; ssh joor 'sudo ceph auth print-key client.<host>'; echo; } |
#          sops --encrypt --filename-override secrets/cephfs/<host>.yaml \
#            --input-type yaml --output-type yaml /dev/stdin > secrets/cephfs/<host>.yaml
#   3. homelab.cephfs.enable = true on the host. Workstations import this
#      module through common-workstation.nix; other hosts import it and need
#      sops-nix and ngarvey pinned to uid 1000 (CephFS stores numeric ids).
#
# The automount mounts on first access and unmounts after 10 minutes idle;
# off the LAN, an access fails after the mount timeout rather than hanging.
# Never add a tmpfiles rule for the mount point: it would re-stamp the
# mounted filesystem's root on every switch.
{ config, lib, pkgs, ... }:

let
  cfg = config.homelab.cephfs;
  cluster = import ./ceph-cluster.nix;
  inherit (import ../networking/lan-hosts.nix) lanHosts;
  lanV6 = name: (lib.findFirst (h: h.hostname == name) null lanHosts).ipv6;
in
{
  options.homelab.cephfs = {
    enable = lib.mkEnableOption "the CephFS local-drive mount";

    mountPoint = lib.mkOption {
      type = lib.types.str;
      default = "/home/ngarvey/local-drive";
      description = "Where local-drive is mounted.";
    };

    clientName = lib.mkOption {
      type = lib.types.str;
      default = config.networking.hostName;
      description = ''
        The cephx user, without `client.`; its key is `key` in
        secrets/cephfs/<clientName>.yaml.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    sops.secrets.cephfs-key = {
      sopsFile = ../../secrets/cephfs/${cfg.clientName}.yaml;
      key = "key";
    };

    # mount.ceph, which the kernel client needs to resolve the options.
    system.fsPackages = [ pkgs.ceph-client ];

    fileSystems.${cfg.mountPoint} = {
      device = "${cfg.clientName}@${cluster.fsid}.local-drive=/";
      fsType = "ceph";
      options = [
        "mon_addr=${lib.concatMapStringsSep "/" (n: "[${lanV6 n}]:3300") cluster.mons}"
        "secretfile=${config.sops.secrets.cephfs-key.path}"
        # The cluster accepts only encrypted msgr2 (modules/services/ceph.nix).
        "ms_mode=secure"
        "noatime"
        # After a long disconnect the MDS evicts the client; reconnect
        # instead of failing every access until a remount.
        "recover_session=clean"
        "_netdev"
        "nofail"
        "x-systemd.automount"
        "x-systemd.idle-timeout=10min"
        "x-systemd.mount-timeout=30s"
      ];
    };
  };
}
