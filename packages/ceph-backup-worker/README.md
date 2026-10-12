# ceph-backup-worker

A Temporal worker that backs up everything stored in Ceph to the Kopia
repository on Garage and copies that repository to Storj. It is a port of the
bash job in `modules/services/ceph-backup.nix`, item for item: same mount
paths, Kopia sources, tags and `root@ceph-backup` identity, so snapshot
history continues. Recovery is unchanged: `docs/ceph-backup.md`.

Built here:

- `nix build .#ceph-backup-worker`: the binary. `go test ./...` runs in the
  build (also the `ceph-backup-worker-tests` check); the tests fake every
  command and Temporal, so they need no Ceph or network.
- `nix build .#ceph-backup-worker-image`: the image, tagged with the store
  hash of its contents, for `oci.garvey.sh/ceph-backup-worker`. It holds the
  worker, `pkgs.ceph` (the storage nodes' package: `rbd`, `ceph`, `rados`,
  `mount.ceph`), kopia, rclone, fuse3, util-linux, e2fsprogs, kmod and
  cacert, and runs as root.

## What it runs

Temporal namespace and task queue `ceph-backup`. The worker creates the
Schedule `ceph-backup` (daily 10:00 UTC, overlap skip) **paused** if it is
missing, and keeps an existing one's spec without touching its paused state.

Workflow `CephBackup` (`internal/backup/workflow.go`):

1. `Discover`: removes `kopia-backup-*` leftovers (mounts in this pod,
   mappings on this node, clones and snapshots in every RBD pool), connects to
   the repository, sets the global policy, and lists the items: `rbd` (every
   image in every pool with the rbd application, ceph-csi ones named by PVC),
   `rgw-metadata`, `rgw` (every bucket, from the admin-ops API), `cephfs`
   (every filesystem), and `pool` items it cannot back up (unknown
   applications, RBD namespaces, pools the cephx key has no caps on).
2. For each item, one at a time: `BackupRBD` / `BackupRGWMetadata` /
   `BackupBucket` / `BackupCephFS`, then `Verify` of the new snapshot: no
   errors, not empty or under half the size or file count (of the whole
   tree) of the previous non-empty snapshot of the source, and `kopia
   snapshot verify` of 10% of its files (100% for rgw-metadata).
3. `Maintenance`: quick daily; full on Sundays (UTC), which also verifies
   every file of the newest snapshot of every source.
4. `SyncStorj`: `kopia repository sync-to rclone --flat --delete` to bucket
   `ceph-backup`, through rclone's S3 backend and Storj's hosted gateway
   `gateway.storjshare.io`. Not kopia's s3 storage: this Storj project lists
   only prefixes ending in `/` (501 NotImplemented otherwise), and kopia lists
   by blob-ID prefix; rclone lists the bucket and filters. Blobs are stored as
   `<id>.f`, which rclone's native storj backend reads too. `VerifyStorj`:
   connects to the copy the same way, read-only, checks it has the newest
   snapshot of every source, and verifies 1% of their files.

A failed item does not stop the others; the run fails at the end with every
failure listed. Item activities retry 3 times (Verify's check failures are
not retried) and heartbeat while Kopia runs.

How each kind is read:

| Kind | How | Kopia source |
|---|---|---|
| rbd | snapshot, clone, `rbd device map`, mount the clone; an unformatted ceph-csi volume snapshots its empty mount point | `/run/ceph-backup/<ns>/<pvc>`, other images `/run/ceph-backup-rbd/<pool>/<image>` |
| rgw | `rclone mount` through S3 with STS credentials of the read-only role (web identity from the environment) | `/run/ceph-backup-rgw/<bucket>` |
| rgw-metadata | admin-ops API `/admin/metadata` (every section, every key), SigV4 as the admin-ops user; OIDC providers with `rados -N oidc get`. The zone and zonegroup are not exported: the admin API does not serve them | `/run/ceph-backup-rgw-metadata` |
| cephfs | `mount.ceph` as `client.ceph-backup`, `.snap/kopia-backup-<time>`, bind-mounted read-only | `/run/ceph-backup-cephfs/<fs>` |

## Metrics

`--metrics-listen` (`:9090`) serves `/metrics`: the Temporal SDK's metrics
on every pod, and, on the pod holding the Lease `ceph-backup-metrics` in
namespace `ceph-backup`, recomputed every `--metrics-interval`:

- `homelab_ceph_backup_item{kind,name}`: what Ceph has now.
- `homelab_ceph_backup_last_success_timestamp_seconds{kind,name}`: end time
  of the newest snapshot without errors of each current item, from the
  repository.
- `homelab_ceph_backup_last_run_success`, `_last_run_timestamp_seconds`: the
  newest finished `CephBackup`, from Temporal's visibility store.
- `homelab_ceph_backup_storj_last_sync_timestamp_seconds`: end time of the
  newest snapshot in the Storj copy.

The last-run and Storj series are absent until there is a run or a copy,
not 0. `pod-monitor.yaml` in k8s/manifests/ceph-backup scrapes them; the
alerts are in k8s/manifests/prometheus/rules/ceph-backup.yaml.

## Flags and files

Credentials are files, read when used. Required: `--temporal-cert`,
`--temporal-key`, `--temporal-ca`, `--ceph-conf`, `--ceph-keyring`,
`--rgw-endpoint`, `--rgw-admin-dir` (`access_key`, `secret_key`),
`--kopia-dir` (`garage_key_id`, `garage_secret_key`, `kopia_password`),
`--storj-dir` (`access_key`, `secret_key`), `--node-name`. STS comes from
`AWS_WEB_IDENTITY_TOKEN_FILE`, `AWS_ROLE_ARN`, `AWS_ROLE_SESSION_NAME` and
`AWS_ENDPOINT_URL_STS`. Kopia's caches are in `--state-dir`
(`/var/cache/ceph-backup-worker`, a hostPath); its repository configs, which
hold the storage keys, are in the pod-local `--run-dir`. `--help` lists the
rest with their defaults.
