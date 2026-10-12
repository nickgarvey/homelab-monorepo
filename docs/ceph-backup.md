# Ceph backups and recovery

Everything stored in Ceph is backed up daily at 10:00 UTC by the ceph-backup
worker, a Temporal worker on the storage nodes (k8s/manifests/ceph-backup,
packages/ceph-backup-worker), into a Kopia repository in the `ceph-backup`
bucket on Garage (lydia + wabbajack), then copied to Storj. Each run is a
`CephBackup` workflow in Temporal namespace `ceph-backup`
(https://temporal.garvey.sh): every item's backup and its check, with errors.
Retention: the last 14 daily snapshots. What exists is discovered on every
run:

| Kind | What | How it is read | Kopia source |
|---|---|---|---|
| `rbd` | every image in every pool with the `rbd` application (today: the ceph-csi volumes in `kubernetes`) | snapshot, clone, mount the clone (the filesystem replays its journal) | `/run/ceph-backup/<namespace>/<pvc>`; other images `/run/ceph-backup-rbd/<pool>/<image>` |
| `rgw` | every bucket of the `k8s` RGW account | rclone mount through S3, as the read-only STS role `ceph-backup` (`modules/services/rgw-roles.nix`) | `/run/ceph-backup-rgw/<bucket>` |
| `rgw-metadata` | accounts, users (with keys), roles, OIDC providers, bucket metadata (not the zone/zonegroup, which `docs/ceph-bootstrap/` recreates) | the RGW admin-ops API (`/admin/metadata`, the same JSON as `radosgw-admin metadata get`), `rados get` for OIDC providers | `/run/ceph-backup-rgw-metadata` |
| `cephfs` | every CephFS filesystem (today: `local-drive`) | a CephFS snapshot of the root (`.snap/kopia-backup-<time>`), mounted with the worker's key `client.ceph-backup` | `/run/ceph-backup-cephfs/<fs>` |

Right after each snapshot the worker checks it: no errors, not under half
the size or file count of the previous one, and 10% of its files read back
from the repository (all of rgw-metadata's). On Sundays maintenance reads back
every file of the newest snapshot of every source. Each backup is crash-consistent per item, not across items. Not captured:
RGW object versions, tags, ACLs, Content-Type and user metadata (the S3 view
through rclone carries only names, contents and times). Kubernetes objects are
not backed up; they are in k8s/.

## What recovery needs

- The Kopia repository password and the Garage key: `secrets/ceph-backup.yaml`,
  decryptable by ngarvey and joor.
- Network access to `garage.home.garvey.sh`, or, for the off-site copy, the
  Storj access grant in the same file and IPv4 (below).
- `kopia`. joor has everything wired up (`ceph-restore`, `ceph-restore-kopia`,
  `rclone`, from `modules/services/ceph-restore.nix`); any other machine can
  connect by hand (below).

Nothing here depends on Ceph or Kubernetes still working.

## Look at the backups

On joor:

    sudo ceph-restore-kopia snapshot list --all                       # everything
    sudo ceph-restore-kopia snapshot list --all --tags pvc:<pvc>      # one volume
    sudo ceph-restore-kopia snapshot list --all --tags bucket:<b>     # one bucket

`ceph-restore-kopia` is plain `kopia` already connected to the repository.
Every snapshot is tagged `kind:<kind>`, plus `namespace:` `pvc:` `pool:`
`image:` (rbd), `bucket:` (rgw) or `fs:` (cephfs). Snapshots taken before
2026-10-12 have only `namespace:`, `pvc:` and `image:`.

## 1. A few files back

Works for any kind. The app can keep running.

    sudo ceph-restore-kopia snapshot restore <snapshot-id>/<path/in/source> /tmp/restored
    # or browse a snapshot as a directory:
    sudo mkdir -p /tmp/snap && sudo ceph-restore-kopia mount <snapshot-id> /tmp/snap

Then copy what you need back: `kubectl cp` into a pod, the rclone upload in
section 3 for objects, or a CephFS mount (section 4).

## 2. One volume back to a snapshot

For a corrupted volume, or one whose PVC was deleted (`ceph-rbd` deletes the
image with the PVC).

1. Scale the app to 0: nothing may have the volume mapped.
2. If the PVC is gone, re-apply it from k8s/. ceph-csi creates a new,
   empty image.
3. On joor:

       sudo ceph-restore <namespace> <pvc> [snapshot-id]

   It finds the PVC's image in any RBD pool, refuses if anything still uses
   it, formats it if it has never been mounted, empties it, and restores the
   snapshot (the latest if none is given). Right after a PVC is re-created,
   its old image lingers for a few seconds; the helper waits for it to go. If
   two images still match, it lists them with their PV names: pass
   `--image <pool>/<image>` for the PV in
   `kubectl -n <ns> get pvc <pvc> -o jsonpath='{.spec.volumeName}'`.
4. Scale the app back up and check it.

## 3. A bucket's objects back

The backup's role is read-only, so make a temporary writer in the
bucket owner's account (`"owner"` in `radosgw-admin bucket stats --bucket=<b>`;
the apps' account is `RGW77873637642885287`). On joor:

    A=RGW77873637642885287
    sudo radosgw-admin user create --uid=restore-tmp --display-name=restore-tmp --account-id=$A
    sudo radosgw-admin user policy attach --uid=restore-tmp \
      --policy-arn=arn:aws:iam::aws:policy/AmazonS3FullAccess
    sudo radosgw-admin user info --uid=restore-tmp | grep -E '"(access|secret)_key"'

    sudo ceph-restore-kopia snapshot restore <snapshot-id> /var/tmp/restore/<b>
    export RCLONE_CONFIG_R_TYPE=s3 RCLONE_CONFIG_R_PROVIDER=Ceph \
      RCLONE_CONFIG_R_ENDPOINT=https://rgw.home.garvey.sh \
      RCLONE_CONFIG_R_ACCESS_KEY_ID=<access_key> RCLONE_CONFIG_R_SECRET_ACCESS_KEY=<secret_key>
    sudo -E rclone copy /var/tmp/restore/<b> r:<b>      # creates the bucket if it is gone

    sudo radosgw-admin user policy detach --uid=restore-tmp \
      --policy-arn=arn:aws:iam::aws:policy/AmazonS3FullAccess
    sudo radosgw-admin user rm --uid=restore-tmp
    sudo rm -rf /var/tmp/restore

`rclone copy` only adds and overwrites; use `rclone sync` to also delete
objects that were not in the snapshot. A bucket re-created this way has
default settings: compare with `bucket.instance/<b>:*.json` in the
rgw-metadata snapshot (versioning, policy, lifecycle) and reapply by hand.

## 4. CephFS files back

Mount the filesystem with the admin key and restore into it. On joor:

    sudo mkdir -p /mnt/local-drive
    sudo mount.ceph admin@$(sudo ceph fsid).local-drive=/ /mnt/local-drive -o ms_mode=secure
    sudo ceph-restore-kopia snapshot restore <snapshot-id>[/<path>] /mnt/local-drive[/<path>]
    sudo umount /mnt/local-drive

## 5. The whole Ceph cluster

What a rebuild uses, besides the backups:

| What | Where |
|---|---|
| fsid, mon addresses, daemons, ceph.conf | `modules/services/ceph.nix` |
| bootstrap steps (keys, monmap, mons, mgr/OSD/RGW, prometheus module, `kubernetes` pool, CephFS) | `docs/ceph-bootstrap/01` to `06` |
| RGW STS key (all RGWs share it) | `secrets/rgw-sts.yaml`, wired in `ceph.nix` |
| RGW TLS | issued on each node by ACME (`ceph.nix`) |
| RGW roles | `modules/services/rgw-roles.nix` recreates them on deploy |
| RGW account, its users, OIDC provider | the `rgw-metadata` snapshot |
| ceph-csi key | created by 05; its copy is `k8s/manifests/ceph-csi-rbd/secret.yaml` |
| admin keyring | recreated by 01; recovery copy in `secrets/ceph-admin.yaml` |

1. Rebuild Ceph from `docs/ceph-bootstrap/` (01 to 06), on wiped mon/OSD
   state. 01 reuses the fsid in `modules/services/ceph.nix`, so ceph-csi's
   `clusterID` stays valid. 05 creates a new `client.kubernetes` key: re-encrypt
   it into `k8s/manifests/ceph-csi-rbd/secret.yaml` and apply it. The
   admin key is new too; update `secrets/ceph-admin.yaml`.
2. RGW identities, before any bucket. Restore the newest rgw-metadata snapshot
   to `/var/tmp/rgw-metadata` and put back, in this order (the account keeps
   its id, so role ARNs and apps' `AWS_ROLE_ARN` stay valid):

       cd /var/tmp/rgw-metadata
       for s in account user roles; do
         for f in $s/*.json; do
           k=$(basename "$f" .json); sudo radosgw-admin metadata put "$s:${k//%//}" < "$f"
         done
       done
       for o in oidc/*; do sudo rados -p default.rgw.meta -N oidc put "$(basename "$o")" "$o"; done

   Do not put `bucket` or `bucket.instance`: those need the bucket index the
   objects restore creates. Deploy joor afterwards so rgw-roles reconciles.
3. Buckets: section 3 for each (`snapshot list --all --tags kind:rgw`).
4. CephFS: section 4, whole filesystem.
5. Volumes: apply ceph-csi, then every app's PVCs with the apps scaled to 0;
   `sudo ceph-restore <namespace> <pvc>` for each.
6. Start the apps; databases run their normal crash recovery.

Up to a day of data is lost (daily backups).

## From a machine other than joor

    export KOPIA_PASSWORD=... AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=...   # from sops -d secrets/ceph-backup.yaml
    kopia repository connect s3 --bucket=ceph-backup \
      --endpoint=garage.home.garvey.sh --region=garage \
      --override-hostname=ceph-backup --override-username=root
    kopia snapshot list --all
    kopia snapshot restore <snapshot-id> <target-dir>

To restore into an RBD image by hand: `rbd device map <pool>/<image>`,
mount it, empty it, `kopia snapshot restore` into it, unmount, unmap.

## Off-site copy on Storj

The Storj bucket `ceph-backup` holds a blob-for-blob copy of the repository:
same password, same snapshots. The worker's `SyncStorj` refreshes it after
every run (`kopia repository sync-to rclone --flat --delete`, so pruning is
mirrored too), and `VerifyStorj` then opens the copy, checks it has the newest
snapshot of every source and reads back 1% of their files. Each blob is an
object `<blob-id>.f` at the top of the bucket.

Kopia's own s3 storage cannot read this copy: this Storj project (self-managed
encryption, created after 2025-12-01) lists only prefixes ending in `/`, and
answers kopia's blob-prefix listings with 501 NotImplemented, which kopia
retries forever. Go through rclone, which lists the bucket and filters itself.
Either way works:

- **Storj's S3 gateway** (IPv6 works; what the worker uses), with the S3 keys
  in the SopsSecret `k8s/manifests/ceph-backup/secret.yaml`
  (`ceph-backup-storj`).
- **rclone's native storj backend**, with `storj_access_grant` from
  `secrets/ceph-backup.yaml`. It talks to Storj's storage nodes, whose
  addresses are IPv4 literals NAT64 cannot translate: dual-stack machines only
  (e.g. talos), not the storage nodes.

To read the copy from talos, from the repo root:

    nix shell --inputs-from . nixpkgs#kopia nixpkgs#rclone nixpkgs#sops -c \
      sops exec-env secrets/ceph-backup.yaml 'bash -s' <<'EOF'
    set -eu
    export KOPIA_PASSWORD=$kopia_password
    export RCLONE_CONFIG_STORJ_TYPE=storj RCLONE_CONFIG_STORJ_ACCESS_GRANT=$storj_access_grant
    export KOPIA_CONFIG_PATH=$(mktemp -d)/repository.config
    kopia repository connect rclone --remote-path=storj:ceph-backup \
      --rclone-exe=$(command -v rclone) --flat --readonly \
      --override-hostname=ceph-backup --override-username=root --no-persist-credentials
    kopia snapshot list --all
    EOF

then restore as from Garage. For the gateway instead, set
`RCLONE_CONFIG_STORJ_TYPE=s3 RCLONE_CONFIG_STORJ_PROVIDER=Storj
RCLONE_CONFIG_STORJ_ENDPOINT=https://gateway.storjshare.io` and
`RCLONE_CONFIG_STORJ_ACCESS_KEY_ID` / `RCLONE_CONFIG_STORJ_SECRET_ACCESS_KEY`
from the SopsSecret. Kopia starts rclone for every command, so keep these in
the environment for all of them, not only `connect`.

Checked 2026-10-12: an anki-data snapshot restored from the copy on talos, through
the native backend, matched the live volume file for file.

## Creating the repository (once)

Already done. For a new bucket:

    sudo sh -c 'set -a; . /run/secrets/rendered/ceph-backup.env; set +a;
      KOPIA_CONFIG_PATH=/var/lib/ceph-backup/repository.config \
      kopia repository create s3 --bucket=ceph-backup \
        --endpoint=garage.home.garvey.sh --region=garage \
        --override-hostname=ceph-backup --override-username=root'

## Alerts

The worker exports, from the pod holding the Lease `ceph-backup-metrics`
(scraped by `k8s/manifests/ceph-backup/pod-monitor.yaml`), with labels `kind`
and `name`: `homelab_ceph_backup_item` (what Ceph has now) and
`homelab_ceph_backup_last_success_timestamp_seconds` (the newest snapshot
without errors, from the repository itself); `homelab_ceph_backup_last_run_success`
and `_last_run_timestamp_seconds` (the newest finished run); and
`homelab_ceph_backup_storj_last_sync_timestamp_seconds` (the newest snapshot in
the Storj copy). Rules in `k8s/manifests/prometheus/rules/ceph-backup.yaml`: a
failed run, no run for a day, an item not backed up for a day, an item never
backed up (`CephBackupNotCovered`, which also catches pools the worker does
not know how to read), a Storj copy over a day old, and no metrics at all.
