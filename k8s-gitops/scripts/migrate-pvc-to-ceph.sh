#!/usr/bin/env bash
# migrate-pvc-to-ceph.sh <namespace> <pvc> <owner, e.g. deploy/acme-dns or sts/couchdb>
# Moves one Longhorn PVC onto ceph-rbd under the same name. Leaves the owner
# scaled to 0; the caller switches the manifest to ceph-rbd and scales up.
# The old volume stays as <pvc>-longhorn-old (PV reclaimPolicy Retain).
#   --copy-only: resume at step 5 (the claims are already swapped).
# Run as root on a k3s server node (fus/ro/dah): it uses `k3s kubectl`.
set -euo pipefail
copy_only=0
[ "${1:-}" = --copy-only ] && { copy_only=1; shift; }
ns=$1; pvc=$2; owner=$3
k() { k3s kubectl "$@"; }
log() { echo "[$(date +%T)] $*"; }

if [ $copy_only = 0 ]; then

pv=$(k -n "$ns" get pvc "$pvc" -o jsonpath='{.spec.volumeName}')
size=$(k -n "$ns" get pvc "$pvc" -o jsonpath='{.spec.resources.requests.storage}')
oldsc=$(k -n "$ns" get pvc "$pvc" -o jsonpath='{.spec.storageClassName}')
[ "$oldsc" != ceph-rbd ] || { echo "$ns/$pvc is already on ceph-rbd" >&2; exit 1; }
log "$ns/$pvc: PV $pv, $size, class $oldsc, owner $owner"

# 1. Fresh Longhorn backup while the app still runs (Longhorn cannot back up a
#    detached volume).
tag="mig-$(date +%Y%m%d%H%M%S)"
cat <<EOF | k apply -f - >/dev/null
apiVersion: longhorn.io/v1beta2
kind: Snapshot
metadata: {name: $tag-$pv, namespace: longhorn-system}
spec: {volume: $pv, createSnapshot: true}
---
apiVersion: longhorn.io/v1beta2
kind: Backup
metadata: {name: $tag-$pv, namespace: longhorn-system}
spec: {snapshotName: $tag-$pv, labels: {migration: ceph}}
EOF
for _ in $(seq 180); do
  st=$(k -n longhorn-system get backups.longhorn.io "$tag-$pv" -o jsonpath='{.status.state}' 2>/dev/null || true)
  [ "$st" = Completed ] && break
  [ "$st" = Error ] && { echo "Longhorn backup failed" >&2; exit 1; }
  sleep 5
done
[ "$st" = Completed ] || { echo "Longhorn backup did not complete (state: $st)" >&2; exit 1; }
log "Longhorn backup $tag-$pv Completed"

# 2. Stop the app.
replicas=$(k -n "$ns" get "$owner" -o jsonpath='{.spec.replicas}')
log "scaling $owner from $replicas to 0"
k -n "$ns" scale "$owner" --replicas=0 >/dev/null
for _ in $(seq 60); do
  users=$(k -n "$ns" get pods -o jsonpath='{range .items[*]}{.spec.volumes[*].persistentVolumeClaim.claimName}{"\n"}{end}' | tr " " "\n" | grep -cx "$pvc" || true)
  [ "$users" = 0 ] && break
  sleep 5
done
[ "$users" = 0 ] || { echo "pods still use $pvc" >&2; exit 1; }

# 3. Keep the old volume under a new claim name.
k patch pv "$pv" -p '{"spec":{"persistentVolumeReclaimPolicy":"Retain"}}' >/dev/null
k -n "$ns" delete pvc "$pvc" --wait=true >/dev/null
k patch pv "$pv" --type=json -p '[{"op":"remove","path":"/spec/claimRef"}]' >/dev/null
cat <<EOF | k apply -f - >/dev/null
apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: $pvc-longhorn-old, namespace: $ns, labels: {migration: longhorn-old}}
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: $oldsc
  volumeName: $pv
  resources: {requests: {storage: $size}}
EOF
# 4. The new volume under the original name.
cat <<EOF | k apply -f - >/dev/null
apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: $pvc, namespace: $ns, labels: {app.kubernetes.io/managed-by: ngarvey-homelab}}
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: ceph-rbd
  resources: {requests: {storage: $size}}
EOF
k -n "$ns" wait "pvc/$pvc-longhorn-old" --for=jsonpath='{.status.phase}'=Bound --timeout=120s >/dev/null
k -n "$ns" wait "pvc/$pvc" --for=jsonpath='{.status.phase}'=Bound --timeout=120s >/dev/null
log "old volume kept as $pvc-longhorn-old; new $pvc bound on ceph-rbd"
fi
replicas=${replicas:-"(see earlier run)"}

# 5. Copy, then a checksum pass that must find nothing to change.
job="migrate-$pvc"
job=${job:0:63}
k -n "$ns" delete job "$job" --ignore-not-found --wait=true >/dev/null
cat <<EOF | k apply -f - >/dev/null
apiVersion: batch/v1
kind: Job
metadata: {name: $job, namespace: $ns}
spec:
  backoffLimit: 0
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: rsync
          image: nixery.dev/shell/rsync/gnugrep
          command:
            - bash
            - -c
            - |
              set -euo pipefail
              rsync -aHAX --numeric-ids --delete --stats /old/ /new/ | grep -E "Number of files|Total file size"
              diffs=\$(rsync -aHAXc --numeric-ids --delete --dry-run --itemize-changes /old/ /new/ | grep -v '^\.d' || true)
              if [ -n "\$diffs" ]; then echo "VERIFY FAILED:"; echo "\$diffs" | head -20; exit 1; fi
              echo "VERIFY OK: checksums match"
          volumeMounts:
            - {name: old, mountPath: /old, readOnly: true}
            - {name: new, mountPath: /new}
      volumes:
        - {name: old, persistentVolumeClaim: {claimName: $pvc-longhorn-old, readOnly: true}}
        - {name: new, persistentVolumeClaim: {claimName: $pvc}}
EOF
for _ in $(seq 720); do
  ok=$(k -n "$ns" get job "$job" -o jsonpath='{.status.succeeded}')
  failed=$(k -n "$ns" get job "$job" -o jsonpath='{.status.failed}')
  [ "$ok" = 1 ] || [ -n "$failed" ] && break
  sleep 5
done
k -n "$ns" logs "job/$job" | tail -5
k -n "$ns" delete job "$job" --wait=true >/dev/null
[ "$ok" = 1 ] || { echo "copy/verify FAILED; app left at 0, old volume intact as $pvc-longhorn-old" >&2; exit 1; }
log "copied and verified. Owner $owner left at 0 (was $replicas): switch its manifest to ceph-rbd, then scale up."
