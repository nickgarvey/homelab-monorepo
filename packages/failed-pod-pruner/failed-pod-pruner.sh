# Deletes pods that have been in the Failed phase for at least MIN_AGE_HOURS,
# in every namespace. A Failed pod is terminal: its controller has already
# replaced it, and nothing else removes it (Kubernetes only collects
# terminated pods once there are 12,500 of them).
#
# The usual source here is a pod that asks for a device-plugin resource
# (Jellyfin's gpu.intel.com/xe): when its node boots, the kubelet re-admits it
# before the plugin has registered and rejects it with
# UnexpectedAdmissionError.
#
# Pods owned by a Job are left alone: the CronJobs' failedJobsHistoryLimit
# already decides how long those are kept.
#
# DRY_RUN=1 prints what would be deleted without deleting it.

min_age_hours="${MIN_AGE_HOURS:-48}"
cutoff=$(date -u -d "@$(( $(date +%s) - min_age_hours * 3600 ))" +%Y-%m-%dT%H:%M:%SZ)

# namespace, name, when it failed, reason. "When it failed" is the newest
# condition transition (the kubelet sets Ready=False/PodFailed at that moment),
# or the creation time for a pod with no conditions.
pods=$(kubectl get pods --all-namespaces --field-selector=status.phase=Failed -o json \
  | jq -r '.items[]
      | select(.metadata.deletionTimestamp == null)
      | select([.metadata.ownerReferences[]?.kind] | index("Job") | not)
      | [ .metadata.namespace,
          .metadata.name,
          (([.status.conditions[]?.lastTransitionTime] | max) // .metadata.creationTimestamp),
          (.status.reason // "none") ]
      | @tsv')

echo "deleting pods that have been Failed since before $cutoff"
deleted=0
kept=0
while IFS=$'\t' read -r ns name failed_at reason; do
  [ -n "$name" ] || continue
  if [[ ! "$failed_at" < "$cutoff" ]]; then
    echo "keep   $ns/$name (failed $failed_at, $reason)"
    kept=$((kept + 1))
    continue
  fi
  echo "delete $ns/$name (failed $failed_at, $reason)"
  if [ "${DRY_RUN:-0}" != 1 ]; then
    kubectl -n "$ns" delete pod "$name" --wait=false
  fi
  deleted=$((deleted + 1))
done <<<"$pods"
if [ "${DRY_RUN:-0}" = 1 ]; then
  echo "done (dry run): $deleted pod(s) would be deleted, $kept kept"
else
  echo "done: $deleted pod(s) deleted, $kept kept"
fi
