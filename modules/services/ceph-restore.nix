# Restore tools for the Ceph backups on one storage node: `ceph-restore` (an RBD volume back from a snapshot) and `ceph-restore-kopia` (kopia connected to the repository).
#
# The backups themselves are made by the ceph-backup worker on k3s
# (k8s/manifests/ceph-backup, packages/ceph-backup-worker) into the Kopia
# repository in the `ceph-backup` bucket on Garage, encrypted with the
# password in secrets/ceph-backup.yaml. These tools need neither k3s nor
# Temporal, only the admin keyring and Garage. Recovery: docs/ceph-backup.md.
{ config, lib, pkgs, ... }:

let
  ceph = config.services.ceph.mon.package;
  stateDir = "/var/lib/ceph-backup";

  # Shared by ceph-restore and ceph-restore-kopia: environment, repository
  # connection, and the lock that keeps two of them from running at once.
  common = ''
    # The worker's snapshots and clones carry this prefix (packages/ceph-backup-worker).
    # shellcheck disable=SC2034
    PREFIX=kopia-backup
    STATE=${stateDir}
    export KOPIA_CONFIG_PATH=$STATE/repository.config
    export KOPIA_CACHE_DIRECTORY=/var/cache/ceph-backup
    export KOPIA_LOG_DIR=/var/log/ceph-backup
    export KOPIA_CHECK_FOR_UPDATES=false
    # AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY (Garage) and KOPIA_PASSWORD.
    set -a
    # shellcheck disable=SC1091
    . ${config.sops.templates."ceph-backup.env".path}
    set +a

    exec 9>/run/lock/ceph-restore.lock
    flock -n 9 || { echo "another ceph-restore is running" >&2; exit 1; }

    connect() {
      kopia repository status >/dev/null 2>&1 && return 0
      # The hostname/username overrides are the identity the snapshots are
      # taken as, whichever node takes them.
      kopia repository connect s3 --bucket=ceph-backup \
        --endpoint=garage.home.garvey.sh --region=garage \
        --override-hostname=ceph-backup --override-username=root
    }

    rbd_pools() {
      ceph osd pool ls detail --format json |
        jq -r '.[] | select(.application_metadata | has("rbd")) | .pool_name'
    }
    # Images in a pool, without the worker's clones.
    rbd_images() { rbd ls "$1" | grep -v "^$PREFIX-" || true; }
    image_meta() { rbd image-meta get "$1/$2" "csi.storage.k8s.io/pvc/$3" 2>/dev/null; }
  '';

  runtimeInputs = [
    ceph pkgs.kopia pkgs.jq pkgs.util-linux pkgs.e2fsprogs
    pkgs.findutils pkgs.coreutils pkgs.gnugrep
  ];

  restore = pkgs.writeShellApplication {
    name = "ceph-restore";
    inherit runtimeInputs;
    text = ''
      image=""
      if [ "''${1:-}" = --image ]; then image=''${2:-}; shift 2; fi
      if [ $# -lt 2 ] || [ $# -gt 3 ]; then
        echo "usage: ceph-restore [--image [<pool>/]<rbd-image>] <namespace> <pvc> [kopia-snapshot-id]" >&2
        echo "Replaces the PVC's contents with the snapshot (default: latest)." >&2
        echo "Scale the app to 0 first; list snapshots with:" >&2
        echo "  sudo ceph-restore-kopia snapshot list --all --tags pvc:<pvc>" >&2
        exit 2
      fi
      ns=$1; pvc=$2; snapid=''${3:-}
    '' + common + ''
      find_images() {
        local pool img
        for pool in $(rbd_pools); do
          for img in $(rbd_images "$pool"); do
            [ "$(image_meta "$pool" "$img" namespace || true)" = "$ns" ] &&
              [ "$(image_meta "$pool" "$img" name || true)" = "$pvc" ] && echo "$pool/$img"
          done
        done
      }
      if [ -n "$image" ]; then
        # ceph-csi's pool when none is given.
        case $image in */*) img=$image ;; *) img=kubernetes/$image ;; esac
      else
        # A re-created PVC's old image lingers for a few seconds while ceph-csi
        # deletes it, so give it time to go before calling this ambiguous.
        for _ in $(seq 12); do
          mapfile -t matches < <(find_images)
          [ "''${#matches[@]}" -eq 1 ] && break
          sleep 5
        done
        if [ "''${#matches[@]}" -ne 1 ]; then
          echo "expected one image for $ns/$pvc, found ''${#matches[@]}:" >&2
          for m in "''${matches[@]}"; do
            echo "  $m (PV $(rbd image-meta get "$m" csi.storage.k8s.io/pv/name 2>/dev/null || echo unknown))" >&2
          done
          echo "If the PVC was deleted, re-apply it from k8s/ first. To choose, pass" >&2
          echo "--image for the PV named by: kubectl -n $ns get pvc $pvc -o jsonpath='{.spec.volumeName}'" >&2
          exit 1
        fi
        img=''${matches[0]}
      fi
      if [ "$(rbd status --format json "$img" | jq '.watchers | length')" -ne 0 ]; then
        echo "$ns/$pvc ($img) is in use; scale its app to 0 first" >&2
        exit 1
      fi

      connect
      if [ -z "$snapid" ]; then
        snapid=$(kopia snapshot list --all --json |
          jq -r --arg ns "$ns" --arg pvc "$pvc" \
            '[.[] | select(.tags["tag:namespace"] == $ns and .tags["tag:pvc"] == $pvc)]
             | sort_by(.startTime) | last | .id // empty')
        [ -n "$snapid" ] || { echo "no backups of $ns/$pvc" >&2; exit 1; }
      fi
      echo "restoring $ns/$pvc ($img) from snapshot $snapid"

      mnt=/run/ceph-restore/$ns/$pvc
      dev=$(rbd device map "$img")
      trap 'umount "$mnt" 2>/dev/null || true; rbd device unmap "$dev" || true' EXIT
      # A PVC that has never been mounted has no filesystem yet; ceph-csi
      # keeps an existing ext4 as it is. ceph-csi runs resize2fs from its own
      # e2fsprogs 1.46 on every mount, which refuses the features newer
      # mkfs.ext4 turns on by default, so leave those off.
      blkid -p "$dev" >/dev/null 2>&1 ||
        mkfs.ext4 -q -O ^orphan_file,^metadata_csum_seed "$dev"
      mkdir -p "$mnt"
      mount "$dev" "$mnt"
      find "$mnt" -mindepth 1 -maxdepth 1 -exec rm -rf {} +
      kopia snapshot restore "$snapid" "$mnt"
      echo "done; scale the app back up"
    '';
  };

  # Plain kopia against the backup repository, for listing and file-level
  # restores (docs/ceph-backup.md).
  kopiaWrapper = pkgs.writeShellApplication {
    name = "ceph-restore-kopia";
    inherit runtimeInputs;
    text = common + ''
      connect
      exec kopia "$@"
    '';
  };

in
{
  sops.secrets.ceph-backup-key-id = { sopsFile = ../../secrets/ceph-backup.yaml; key = "garage_key_id"; };
  sops.secrets.ceph-backup-secret-key = { sopsFile = ../../secrets/ceph-backup.yaml; key = "garage_secret_key"; };
  sops.secrets.ceph-backup-kopia-password = { sopsFile = ../../secrets/ceph-backup.yaml; key = "kopia_password"; };
  sops.templates."ceph-backup.env".content = ''
    AWS_ACCESS_KEY_ID=${config.sops.placeholder.ceph-backup-key-id}
    AWS_SECRET_ACCESS_KEY=${config.sops.placeholder.ceph-backup-secret-key}
    KOPIA_PASSWORD=${config.sops.placeholder.ceph-backup-kopia-password}
  '';

  # Plain kopia and rclone too, for the by-hand steps in docs/ceph-backup.md.
  environment.systemPackages = [ restore kopiaWrapper pkgs.kopia pkgs.rclone ];

  # The repository connection (repository.config) and Kopia's cache and logs.
  systemd.tmpfiles.rules = [
    "d ${stateDir} 0700 root root -"
    "d /var/cache/ceph-backup 0700 root root -"
    "d /var/log/ceph-backup 0700 root root -"
  ];
}
