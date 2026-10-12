# Run as root. The CephFS filesystem `local-drive` and its MDSs
# (modules/services/ceph.nix).
#   On each node:          06-cephfs.sh mds
#   Then once, any node:   06-cephfs.sh fs
#                          06-cephfs.sh root
#   Per client host:       06-cephfs.sh client <host>
set -euo pipefail

case ${1:-} in
mds)
  # This node's MDS key. The NixOS unit ceph-mds-<host> starts once it exists.
  H=$(hostname)
  d=/var/lib/ceph/mds/ceph-$H; install -d -o ceph -g ceph $d
  ceph auth get-or-create mds.$H mon 'profile mds' mgr 'profile mds' mds 'allow *' osd 'allow *' > $d/keyring
  chown ceph:ceph $d/keyring; chmod 600 $d/keyring
  systemctl start ceph-mds-$H
  systemctl is-active ceph-mds-$H
  ;;
fs)
  # Pools by hand rather than `ceph fs volume create`, which also asks an
  # orchestrator (there is none) to deploy MDSs.
  ceph osd pool create cephfs.local-drive.meta 16
  ceph osd pool set cephfs.local-drive.meta pg_autoscale_bias 4
  ceph osd pool create cephfs.local-drive.data
  ceph fs new local-drive cephfs.local-drive.meta cephfs.local-drive.data
  ceph fs set local-drive allow_standby_replay true

  # Share of capacity the autoscaler plans PGs for (relative to each other).
  ceph osd pool set kubernetes target_size_ratio 0.5
  ceph osd pool set default.rgw.buckets.data target_size_ratio 0.2
  ceph osd pool set cephfs.local-drive.data target_size_ratio 0.3

  # RGW makes default.rgw.otp on first use with 32 PGs, like its other
  # metadata pools; pin it at 8 for the same reason (05-kubernetes-pool.sh).
  if ceph osd pool ls | grep -qx default.rgw.otp; then
    ceph osd pool set default.rgw.otp pg_autoscale_mode off
    ceph osd pool set default.rgw.otp pg_num 8
  fi

  ceph fs status local-drive
  ceph osd pool autoscale-status
  ;;
root)
  # The root belongs to ngarvey: uid 1000, group users (100), as on every
  # client (CephFS stores numeric ids). Ownership is stored in the
  # filesystem, so this is once, not per client.
  M=$(mktemp -d)
  mount.ceph admin@$(ceph fsid).local-drive=/ "$M" -o ms_mode=secure
  chown 1000:100 "$M"; chmod 755 "$M"
  stat -c "%U:%G %a %n" "$M"
  umount "$M"; rmdir "$M"
  ;;
client)
  # A client host's own key (modules/services/cephfs-mount.nix), read-write
  # on the whole filesystem, revocable on its own (`ceph auth rm
  # client.<host>`). Prints no secret: its key goes from `ceph auth print-key`
  # straight into secrets/cephfs/<host>.yaml (see that module).
  H=${2:?usage: $0 client <host>}
  ceph fs authorize local-drive client.$H / rw >/dev/null
  ceph auth get client.$H 2>/dev/null | grep caps
  ;;
*)
  echo "usage: $0 mds|fs|root|client <host>" >&2; exit 2 ;;
esac
