# Deletes the Longhorn backup volumes of volumes that no longer exist, once
# their last backup is older than MIN_AGE_DAYS. Deleting a BackupVolume makes
# Longhorn remove its backups from the backup target. The recurring backup
# job's retain count only prunes volumes that still exist, so without this
# a deleted volume's backups are kept forever.
#
# DRY_RUN=1 prints what would be deleted without deleting it.

ns=longhorn-system
min_age_days="${MIN_AGE_DAYS:-30}"
cutoff=$(date -u -d "@$(( $(date +%s) - min_age_days * 86400 ))" +%Y-%m-%dT%H:%M:%SZ)

volumes=$(kubectl -n "$ns" get volumes.longhorn.io -o json | jq -r '.items[].metadata.name')
backup_volumes=$(kubectl -n "$ns" get backupvolumes.longhorn.io -o json \
  | jq -r '.items[] | [.metadata.name, .spec.volumeName, (.status.lastBackupAt // "")] | @tsv')

# An empty volume list next to existing backups is far more likely a broken
# API answer than a cluster with no volumes; stop rather than delete them all.
if [ -z "$volumes" ] && [ -n "$backup_volumes" ]; then
  echo "no Longhorn volumes listed but backup volumes exist; refusing to prune" >&2
  exit 1
fi

echo "pruning backup volumes of deleted volumes with no backup since $cutoff"
deleted=0
while IFS=$'\t' read -r name volume last; do
  [ -n "$name" ] || continue
  if grep -qxF "$volume" <<<"$volumes"; then
    continue
  fi
  # No backup time yet (still syncing): leave it for a later run.
  if [ -z "$last" ] || [[ ! "$last" < "$cutoff" ]]; then
    echo "keep   $name (volume $volume deleted, last backup ${last:-unknown})"
    continue
  fi
  echo "delete $name (volume $volume deleted, last backup $last)"
  if [ "${DRY_RUN:-0}" != 1 ]; then
    kubectl -n "$ns" delete backupvolumes.longhorn.io "$name" --wait=false
  fi
  deleted=$((deleted + 1))
done <<<"$backup_volumes"
if [ "${DRY_RUN:-0}" = 1 ]; then
  echo "done (dry run): $deleted backup volume(s) would be deleted"
else
  echo "done: $deleted backup volume(s) deleted"
fi
