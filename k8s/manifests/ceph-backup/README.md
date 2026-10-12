# ceph-backup

The Ceph backup worker (`packages/ceph-backup-worker`, whose README describes
the workflow, the metrics and the flags): a DaemonSet on the storage nodes
joor, zah and frul, polling task queue `ceph-backup` in Temporal namespace
`ceph-backup` (365 days of retention). Each item runs on whichever pod takes
it; if a node goes down mid-activity, Temporal retries the activity on
another. Recovery does not use any of this: `docs/ceph-backup.md`.

The worker creates the Schedule `ceph-backup` (daily 10:00 UTC) **paused** and
never unpauses it. To run a backup now, from the Temporal break-glass path
("Ports" in `manifests/temporal/README.md`):

```sh
temporal schedule trigger --schedule-id ceph-backup --namespace ceph-backup --address 127.0.0.1:7236
temporal schedule toggle --unpause --reason "<why>" --schedule-id ceph-backup --namespace ceph-backup --address 127.0.0.1:7236
```

## Why the pods are privileged

Mapping RBD images and mounting CephFS and rclone (FUSE) is kernel work, done
the way ceph-csi's node plugin does it (`daemonset.yaml`):

- `privileged`, with the host's `/dev` (for the `/dev/rbdN` devices mapped
  after the pod starts), `/sys` and `/run/udev` (read-only; `rbd device map`
  waits for udev).
- `hostNetwork`: the kernel clients connect from the network namespace that
  maps or mounts. So Cilium policies do not apply to these pods, and the
  metrics port 9090 and spiffe-helper's health port 8081 are bound on the
  node.
- Mounts stay in the pod's mount namespace. RBD mappings belong to the node:
  the worker unmaps leftover `kopia-backup-*` ones at start.
- No `/lib/modules`: NixOS has none to mount. `rbd` is loaded at boot
  (`modules/k3s/k3s-common.nix`), and the kernel loads `ceph` itself on the
  first CephFS mount.

Kopia's caches are a hostPath, `/var/cache/ceph-backup-worker`, so a restarted
pod does not fetch the indexes again. Its repository configs, which hold the
storage keys, are on a memory-backed emptyDir.

## Credentials

Each piece reaches only what backing up needs:

| What | Credential |
|---|---|
| Temporal | X.509-SVID `spiffe://home.garvey.sh/ns/ceph-backup/sa/ceph-backup-worker`, `write` in namespace `ceph-backup` (`manifests/temporal/authz-configmap.yaml`) |
| RBD, CephFS, pool and filesystem maps, RGW OIDC objects | cephx `client.ceph-backup` (`docs/ceph-bootstrap/07-ceph-backup-worker.sh` sets its caps), Secret `ceph-backup-ceph` |
| RGW bucket contents | STS role `ceph-backup` (`modules/services/rgw-roles.nix`): read and list on every bucket of the `k8s` account, for the JWT-SVID (audience `rgw.home.garvey.sh`) |
| RGW metadata | admin-ops user `ceph-backup-admin-ops`, read caps only, Secret `ceph-backup-rgw-admin` |
| Kopia repository | Garage key `ceph-backup-worker-key` (bucket `ceph-backup`) and the repository password, Secret `ceph-backup-kopia` |
| Off-site copy | Storj hosted S3 gateway keys for bucket `ceph-backup`, Secret `ceph-backup-storj` |

The four Secrets come from the SopsSecret in `secret.yaml`. The Kubernetes
token is mounted only for the Lease `ceph-backup-metrics` (`rbac.yaml`), which
picks the pod that computes the backup metrics.

Checked 2026-10-12 from the zah pod: STS gives the role, which lists `celler`
and is refused CreateBucket and PutObject (403).

## Apply (from `k8s/`)

The cephx key and admin-ops user (`docs/ceph-bootstrap/07-ceph-backup-worker.sh`)
and the RGW role (deploy joor) first. Then Temporal:

```sh
kubectl apply -f manifests/temporal/authz-configmap.yaml
kubectl -n temporal rollout restart deploy/temporal
kubectl -n temporal port-forward deploy/temporal 7236:7236 &
temporal operator namespace create --namespace ceph-backup --retention 8760h --address 127.0.0.1:7236
```

and this directory:

```sh
kubectl apply -f manifests/ceph-backup/namespace.yaml
kubectl apply -f manifests/ceph-backup/service-account.yaml
kubectl apply -f manifests/ceph-backup/rbac.yaml
kubectl apply -f manifests/ceph-backup/secret.yaml
kubectl apply -f manifests/ceph-backup/ceph-configmap.yaml
kubectl apply -f manifests/ceph-backup/spiffe-helper-configmap.yaml
kubectl apply -f manifests/ceph-backup/daemonset.yaml
kubectl apply -f manifests/ceph-backup/pod-monitor.yaml
```

The image's tag is the store hash of its contents, so changing the worker,
kopia or ceph changes the tag in `daemonset.yaml`:

```sh
nix build .#ceph-backup-worker-image
skopeo copy docker-archive:result docker://oci.garvey.sh/ceph-backup-worker:$(nix eval --raw .#ceph-backup-worker-image.imageTag)
```
