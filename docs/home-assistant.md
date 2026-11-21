# Home Assistant

Home Assistant's state lives on lydia in the btrfs subvolume `@homeassistant`
on the 980 PRO (`/fast/homeassistant`): HA's config and database, plus the
Z-Wave JS, Matter server and OTBR data. Keeping it in one subvolume means a
snapshot captures all of it at one instant.

## Backups

| Copy | Where | How often | Kept | Made by |
|---|---|---|---|---|
| Local snapshot | `/mnt/fast-root/.snapshots/@homeassistant.<ts>` (same disk) | hourly | every one for 2 days, then daily for 14 days | btrbk (`hosts/lydia/configuration.nix`) |
| Second local disk | `/mnt/slow-root/.received/@homeassistant.<ts>` (SATA mirror) | hourly | every one for 2 days, daily for 14 days, weekly for 8 weeks | btrbk send/receive |
| Off-host | RGW bucket `homeassistant-backup`, `<UTC ts>.tar.zst` | daily, 08:00 UTC | 30 days (bucket lifecycle rule) | `homeassistant-backup` timer (`modules/services/home-assistant-backup.nix`) |
| Off-site | the bucket inside the Ceph backup's Kopia repository on Garage, and its Storj copy | daily, 10:00 UTC | 14 daily snapshots | ceph-backup worker (`docs/ceph-backup.md`) |
| HA's own | Nabu Casa cloud | daily | 7 | HA's backup settings (config and history only) |

Each archive is a read-only snapshot of the subvolume, written with
`--numeric-owner --xattrs --acls` and checked with `zstd -t` before upload.
After upload, the stored size is compared with the local one.

**Alerts** (`k8s/manifests/prometheus/rules/homeassistant-backup.yaml`), all
critical:

- `HomeAssistantBackupStale`: no upload for 26 hours.
- `HomeAssistantBackupMetricsMissing`: lydia stopped pushing the metrics.
- `HomeAssistantBackupShrunk`: the last archive is under half the largest of the
  past week. An unmounted or emptied `/fast/homeassistant` still uploads without
  error; this alert is what catches it.

The Ceph backup's own alerts (`CephBackupNotCovered`, `CephBackupStale`,
`CephBackupStorjStale`) cover the bucket's off-site copy.

## Restore

Stop everything that writes the state first, so nothing overwrites the
restored files.

### From a local snapshot (bad upgrade, bad edit)

On lydia:

    sudo ls /mnt/fast-root/.snapshots/ | grep @homeassistant      # pick <ts>
    sudo ls /mnt/slow-root/.received/                              # if the 980 PRO is gone
    sudo rsync -aHAX --delete --numeric-ids \
      /mnt/fast-root/.snapshots/@homeassistant.<ts>/ /fast/homeassistant/

For a single file, copy it out of the snapshot instead.

### From the RGW bucket (lydia's disks gone, or a rebuild)

The backup job has no standing key (it uses lydia's SPIFFE identity), so make a
temporary key for the RGW user `homeassistant-backup` on a Ceph node, and
remove it when done:

    # on joor, zah or frul
    sudo radosgw-admin key create --uid=homeassistant-backup --key-type=s3 --gen-access-key --gen-secret
    # anywhere, with the access_key/secret_key it printed
    export AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=...
    aws s3 ls --endpoint-url https://rgw.home.garvey.sh s3://homeassistant-backup/
    aws s3 cp --endpoint-url https://rgw.home.garvey.sh \
      s3://homeassistant-backup/<ts>.tar.zst /var/tmp/
    zstd -dc /var/tmp/<ts>.tar.zst | sudo tar -C /fast/homeassistant \
      --numeric-owner --xattrs --acls -x
    # back on the Ceph node
    sudo radosgw-admin key rm --uid=homeassistant-backup --key-type=s3 --access-key=<access_key>

### From the Ceph backup (RGW or the bucket gone)

The bucket's objects are in the Kopia repository, tagged `bucket:homeassistant-backup`.
On joor:

    sudo ceph-restore-kopia snapshot list --all --tags bucket:homeassistant-backup
    sudo ceph-restore-kopia snapshot restore <snapshot-id>/<ts>.tar.zst /var/tmp/<ts>.tar.zst

Then unpack as in the previous section. Without joor, or without Garage, see
"From a machine other than joor" and "Off-site copy on Storj" in
`docs/ceph-backup.md`.

### Checking a backup without touching the live state

Unpack into a scratch directory and compare with the live tree. Files HA wrote
since the backup will differ:

    mkdir /var/tmp/ha-restore-test
    zstd -dc <ts>.tar.zst | sudo tar -C /var/tmp/ha-restore-test --numeric-owner --xattrs --acls -x
    sudo diff -rq /var/tmp/ha-restore-test /fast/homeassistant

## One-time setup

These steps were done by hand and are recorded here so they can be redone.

1. The subvolume, before the first deploy that mounts it:

       sudo btrfs subvolume create /mnt/fast-root/@homeassistant

2. The RGW user `homeassistant-backup` in the `k8s` account, kept for restores
   (it holds no key between them). Set it up the same way as
   `openbao-snapshot` (`docs/openbao.md`, "Snapshots and restore"):
   - Its only permission is an inline IAM policy allowing `s3:*` on
     `arn:aws:s3:::homeassistant-backup` and `arn:aws:s3:::homeassistant-backup/*`.
     Set it with `aws iam put-user-policy` as a temporary account-root user, then
     remove that user.
   The backup job itself uses the STS role `homeassistant-backup`
   (`modules/services/rgw-roles.nix`, deployed on joor) with lydia's SPIFFE
   identity, whose entry is in `k8s/manifests/spire/host-entries.yaml`.
3. The bucket and its 30-day lifecycle rule, created with a temporary key of
   that user (as in the restore above):

       aws s3 mb --endpoint-url https://rgw.home.garvey.sh s3://homeassistant-backup
       aws s3api put-bucket-lifecycle-configuration --endpoint-url https://rgw.home.garvey.sh \
         --bucket homeassistant-backup --lifecycle-configuration \
         '{"Rules":[{"ID":"expire-30d","Status":"Enabled","Filter":{"Prefix":""},"Expiration":{"Days":30}}]}'

4. Apply the alert rules:

       kubectl apply -f k8s/manifests/prometheus/rules/homeassistant-backup.yaml
