# metrics-collector

One Vector instance in the cluster that polls HTTP APIs and serves the results
as Prometheus metrics. Use it for metrics about things outside the homelab (a
GitHub repo, a web API) that have no exporter of their own.

Metrics about a NixOS host belong on that host instead, through
`homelab.metrics` (`modules/services/vector-agent.nix`). Both use the same
Vector idiom: a source, a VRL `remap`, and a `log_to_metric` transform.

| File | What it is |
|---|---|
| `configmap.yaml` | The Vector config; every metric is defined here. |
| `deployment.yaml` | `timberio/vector`, the version the hosts run. `--watch-config` reloads the config when the ConfigMap changes. |
| `pod-monitor.yaml` | Prometheus scrapes `:9598` every 60s. |

## Adding a metric

In `configmap.yaml`:

1. An `http_client` source: `endpoint`, `headers`, `decoding.codec: json`.
   - Set `scrape_interval_secs` below an hour. The sink's `flush_period_secs` (3600) drops any metric not seen for that long.
   - Mind the target's rate limits. For example, unauthenticated GitHub API calls allow 60 per hour, shared by the whole LAN.
2. A `remap` transform that reduces the response to the fields you need.
   - Set `drop_on_abort: true` and `abort` when the response does not have them.
3. A `log_to_metric` transform **named `<something>_metric`**. The sink's `"*_metric"` input picks it up, so the sink needs no change.
4. A rule in `manifests/prometheus/rules/`, including an `absent()` alert, so a broken poll does not go unnoticed.

Before applying, check the config locally:

```sh
nix run nixpkgs#yq-go -- '.data."vector.yaml"' manifests/metrics-collector/configmap.yaml \
  | sed "s#/var/lib/vector#$(mktemp -d)#" > /tmp/vector.yaml
nix run nixpkgs#vector -- validate --no-environment /tmp/vector.yaml
nix run nixpkgs#vector -- --config /tmp/vector.yaml &   # then: curl -s 'http://[::1]:9598/metrics'
```

### APIs that need a credential

Put the credential in a SOPS-encrypted Secret in this namespace, mount it as a
directory, and read it through Vector's `directory` secret backend, the way
`vector-agent.nix` reads the remote-write password:

```yaml
secret:
  credentials:
    type: directory
    path: /run/secrets/metrics-collector
    remove_trailing_whitespace: true
# then, in a source:  auth: { strategy: bearer, token: "SECRET[credentials.github-token]" }
```

Vector does not interpolate environment variables, so use the secret backend, not `env`.

## IPv4-only APIs

The cluster is IPv6-only. For a target without an AAAA record, such as api.github.com, the router answers with a synthesized `64:ff9b::/96` address (blocky DNS64, `modules/router/blocky-dns.nix`). Jool translates that address to IPv4 (`modules/router/nat64.nix`). Nothing needs configuring here, but these targets depend on the router's NAT64.

## Apply

From `k8s/`:

```sh
kubectl apply -f manifests/metrics-collector/namespace.yaml
kubectl apply -f manifests/metrics-collector/
```

A ConfigMap change reaches the pod in about a minute, and Vector reloads it. If the new config is invalid, Vector logs the error and keeps running the old one, so check `kubectl -n metrics-collector logs deploy/metrics-collector` after a change.
