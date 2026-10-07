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
| openshell | chart, `openshell/chart.env` (OCI), plus `kustomization.yaml` (IPv6 listeners); see `manifests/openshell/README.md` | 0.1.2 chart; images pinned to NVIDIA/OpenShell#3702 in `values.yaml` | `kubectl apply -k vendor/openshell` |
| prometheus-operator | static, `prometheus-operator/source.env`, plus `kustomization.yaml` (own namespace, control-plane pinning) | v0.90.1 | `kubectl apply --server-side -k vendor/prometheus-operator` |
| csi-smb | static, `deploy/<v>/` of kubernetes-csi/csi-driver-smb (rbac, driver, controller, node) | v1.16.0 | `kubectl apply -f vendor/csi-smb/install.yaml` |
| intel-gpu-plugin | static, `deployments/gpu_plugin/base/intel-gpu-plugin.yaml` of intel/intel-device-plugins-for-kubernetes | 0.31.0 | `kubectl apply -f vendor/intel-gpu-plugin/daemonset.yaml` |
| agent-sandbox | static, `agent-sandbox/source.env` (core `sandbox.yaml` of kubernetes-sigs/agent-sandbox, no extensions) | v1.0.3 | `kubectl apply -f vendor/agent-sandbox/sandbox.yaml` |
| cert-manager-approver-policy | chart, `cert-manager-approver-policy/chart.env`; installs into cert-manager's namespace | v0.28.0 | `kubectl apply -f vendor/cert-manager-approver-policy/cert-manager-approver-policy.yaml` |
| cloudnative-pg | chart, `cloudnative-pg/chart.env` | 0.29.1 (operator 1.30.1) | `kubectl apply --server-side -f vendor/cloudnative-pg/cloudnative-pg.yaml` |
| plugin-barman-cloud | static, `plugin-barman-cloud/source.env` (`manifest.yaml` of cloudnative-pg/plugin-barman-cloud) | v0.15.1 | `kubectl apply --server-side -f vendor/plugin-barman-cloud/manifest.yaml` |
| spire-crds | chart, `spire-crds/chart.env`; the CRDs of spire, apply first | 0.6.1 | `kubectl apply -f vendor/spire-crds/spire-crds.yaml` |
| spire | chart, `spire/chart.env`; see `manifests/spire/README.md` | 0.30.3 (SPIRE 1.15.3) | `kubectl apply -f vendor/spire/spire.yaml` |

Notes on the apply commands:

- **sops-secrets-operator** needs `-n`: the chart leaves `metadata.namespace` off
  its ServiceAccount and Deployment, and without `-n` they land in `default`.
- **prometheus-operator** and **cloudnative-pg** need `--server-side`: their CRDs
  are too big for the `last-applied-configuration` annotation that client-side
  apply writes.
- **plugin-barman-cloud** goes after cloudnative-pg and cert-manager (it creates
  cert-manager Certificates in `cnpg-system`).
- **openshell** goes through its `kustomization.yaml`, which sets the namespace
  the chart leaves off and moves the gateway's listeners from `0.0.0.0` to
  `[::]` with env vars (the chart has no value for the bind address).
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
For prometheus-operator and agent-sandbox, `source.env` records the URL and
version, and `scripts/fetch-vendor.sh` re-downloads the file (`--check` fails
if the committed file differs). prometheus-operator's local changes live in
`kustomization.yaml`, which moves the operator out of `default` into
`prometheus-operator` and pins it to the control-plane nodes.

### Upgrading prometheus-operator

1. Bump `VERSION` in `prometheus-operator/source.env`.
2. `scripts/fetch-vendor.sh prometheus-operator` and read `git diff`.
3. `kubectl diff --server-side -k vendor/prometheus-operator`, then apply as in
   the table.
