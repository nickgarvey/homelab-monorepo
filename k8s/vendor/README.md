# vendor/

Upstream components, as the YAML that gets applied. Helm is a build-time tool
here: charts are rendered into files in git, and nothing in the cluster knows
about Helm (no releases, no hooks, no k3s HelmChart). Our own resources that sit
on top of a component (issuers, StorageClasses, policies) live in
`manifests/<component>/`.

## What is here

| Component | Source | Version | Apply (from `k8s/`) |
|---|---|---|---|
| cilium | chart, `cilium/chart.env` | 1.19.8 | `kubectl apply -f vendor/cilium/cilium.yaml` |
| cert-manager | chart, `cert-manager/chart.env` | v1.20.1 | `kubectl apply -f vendor/cert-manager/cert-manager.yaml` |
| sops-secrets-operator | chart, `sops-secrets-operator/chart.env` | 0.23.1 (operator 0.17.1) | `kubectl apply -n sops-secrets-operator -f vendor/sops-secrets-operator/sops-secrets-operator.yaml` |
| longhorn | chart, `longhorn/chart.env` | 1.11.1 | `kubectl apply -f vendor/longhorn/longhorn.yaml` |
| ceph-csi-rbd | chart, `ceph-csi-rbd/chart.env` | 3.17.1 | `kubectl apply -f vendor/ceph-csi-rbd/ceph-csi-rbd.yaml` |
| prometheus-operator | static, `prometheus-operator/source.env`, plus `kustomization.yaml` (own namespace, control-plane pinning) | v0.90.1 | `kubectl apply --server-side -k vendor/prometheus-operator` |
| csi-smb | static, `deploy/<v>/` of kubernetes-csi/csi-driver-smb (rbac, driver, controller, node) | v1.16.0 | `kubectl apply -f vendor/csi-smb/install.yaml` |
| intel-gpu-plugin | static, `deployments/gpu_plugin/base/intel-gpu-plugin.yaml` of intel/intel-device-plugins-for-kubernetes | 0.31.0 | `kubectl apply -f vendor/intel-gpu-plugin/daemonset.yaml` |

Notes on the apply commands:

- **sops-secrets-operator** needs `-n`: the chart leaves `metadata.namespace` off
  its ServiceAccount and Deployment, and without `-n` they land in `default`.
- **prometheus-operator** needs `--server-side`: its CRDs are too big for the
  `last-applied-configuration` annotation that client-side apply writes.
- On a bare cluster, apply cilium first (it is the CNI), and each component
  before the `manifests/` resources that use its CRDs.

## Charts

Each chart directory holds `chart.env` (repo, chart, version, release name,
namespace), `values.yaml` (only the values we change, each with the reason)
and the render. Re-render with:

```sh
scripts/render-chart.sh <name>     # or --all
scripts/render-chart.sh --check --all   # fails if a render is stale
```

Never edit a render by hand. Change `values.yaml` and re-render. Helm hooks are
left out (`--no-hooks`) because nothing runs them.

### Upgrading a chart

1. Bump `VERSION` in `chart.env`. Read the chart's release notes for renamed
   or removed values.
2. `scripts/render-chart.sh <name>` and read `git diff`.
3. `kubectl diff -f vendor/<name>/<name>.yaml`, then apply as in the table.
4. `kubectl apply` does not prune. Delete by hand any object the render diff
   removed.

## Static manifests

These are upstream's files, downloaded as-is and never edited.
prometheus-operator's `source.env` records its URL and version, and
`scripts/fetch-vendor.sh` re-downloads it (`--check` fails if the committed
file differs). Its local changes live in `kustomization.yaml`, which moves the
operator out of `default` into `prometheus-operator` and pins it to the
control-plane nodes.

### Upgrading prometheus-operator

1. Bump `VERSION` in `prometheus-operator/source.env`.
2. `scripts/fetch-vendor.sh prometheus-operator` and read `git diff`.
3. `kubectl diff --server-side -k vendor/prometheus-operator`, then apply as in
   the table.
