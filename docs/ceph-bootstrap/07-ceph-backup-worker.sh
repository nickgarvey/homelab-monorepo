# Run once, on any node, as root. The Ceph identities of the backup worker
# (k8s/manifests/ceph-backup): a cephx key and an RGW admin-ops user, both
# limited to what backing up needs. Prints no secrets. Their keys live in the
# SopsSecret k8s/manifests/ceph-backup/secret.yaml (Secrets ceph-backup-ceph and
# ceph-backup-rgw-admin), piped from `ceph auth print-key` and `radosgw-admin
# user info` straight into `sops --encrypt` so they are never shown.
set -euo pipefail

# RBD: list, snapshot, clone, map and remove clones, in each pool with the rbd
# application. `profile rbd` without pool= would reach every pool, RGW's data and
# user records included, so the pools are listed: after adding an RBD pool, run
# this again (until then the worker fails on it and the backup alerts fire).
# CephFS: mount every filesystem and take .snap snapshots (rws; the osd cap
# reaches only pools tagged for CephFS).
# Discovery: read the pool and filesystem maps (allow r).
# RGW's OIDC provider objects, which the admin-ops API cannot export.
# Lines look like: pool 10 'kubernetes' replicated ... application rbd ...
rbd_pools=$(ceph osd pool ls detail | awk '/ application ([a-z,]*,)?rbd(,| |$)/ { gsub("'\''", "", $3); print $3 }')
osd='allow rw tag cephfs data=*, allow r pool=default.rgw.meta namespace=oidc'
mgr=''
for p in $rbd_pools; do
  osd="profile rbd pool=$p, $osd"
  mgr="${mgr:+$mgr, }profile rbd pool=$p"
done
caps=(
  mon 'profile rbd, allow r'
  osd "$osd"
  mgr "$mgr"
  mds 'allow rws'
)
if ceph auth get client.ceph-backup >/dev/null 2>&1; then
  # Prints the whole keyring, key included.
  ceph auth caps client.ceph-backup "${caps[@]}" >/dev/null 2>&1
else
  ceph auth get-or-create client.ceph-backup "${caps[@]}" >/dev/null
fi

# RGW admin-ops API, read only: the metadata export (users, accounts, roles,
# buckets). Not in any account, so it owns no buckets and reads none.
u=ceph-backup-admin-ops
radosgw-admin user info --uid=$u >/dev/null 2>&1 ||
  radosgw-admin user create --uid=$u --display-name="ceph backup worker (admin-ops, read only)" >/dev/null
radosgw-admin caps add --uid=$u \
  --caps="users=read; buckets=read; metadata=read; accounts=read; roles=read; zone=read; info=read" >/dev/null

ceph auth get client.ceph-backup 2>/dev/null | grep caps
radosgw-admin user info --uid=$u | grep -A12 '"caps"' | grep -E '"(type|perm)"'
