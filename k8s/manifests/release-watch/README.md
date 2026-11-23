# release-watch

Tells the user when a hand-bumped image falls behind its project's releases,
and whether the newer release fixes a published security advisory. Today it
watches Authelia only (`ghcr.io/authelia/authelia`, `manifests/authelia/`).

The worker (`packages/release-watch`) polls task queue `release-watch` in
Temporal namespace `release-watch` (30 days of retention). The Schedule
`release-watch` runs `ReleaseWatch` daily at 08:00 UTC; the worker creates it
when it is missing, with a first run at once, and leaves an existing one as it
is. For each target, the `Check` activity:

1. lists the target's running pods through the Kubernetes API (`rbac.yaml`)
   and takes the oldest image tag among them, so a bump that is in git but
   not applied still counts as behind;
2. reads `GET /repos/<repo>/releases/latest` and
   `GET /repos/<repo>/security-advisories` from api.github.com,
   unauthenticated (IPv4-only, reached through DNS64/NAT64);
3. reports it behind if the latest release is newer, and lists each published
   advisory that affects the running version and has a patched version newer
   than it and no newer than the latest. An advisory lists one patched version
   per maintained line (`4.38.19,4.39.0`); a running version at or past the
   patched version on its own line counts as patched.

## Alerts

`/metrics` on 9090 (`pod-monitor.yaml`) has, from the newest successful run
(read back from Temporal every 5 minutes, so a restart loses nothing):

- `homelab_release_watch_info{name,repo,running,latest}` (1)
- `homelab_release_watch_behind{name,repo}` (0/1)
- `homelab_release_watch_security_fixes{name,repo}` (advisories fixed by upgrading)
- `homelab_release_watch_last_success_timestamp_seconds`

The rules in `manifests/prometheus/rules/release-watch.yaml` email through
Alertmanager (`manifests/prometheus/alertmanager-config.yaml`):
`ReleaseWatchSecurityFix` (critical, repeats daily), `ReleaseWatchBehind`
(repeats every 5 days), and `ReleaseWatchStale` / `ReleaseWatchMetricsMissing` when
the check stops succeeding.

## Watching another image

Add a `Target` in `packages/release-watch/internal/watch/check.go` (namespace,
pod label selector, container, GitHub repo), and a Role and RoleBinding for
its namespace to `rbac.yaml`. Its tags must be versions (`1.2.3`, `v1.2.3`).

## Checking it

From the Temporal break-glass path ("Ports" in `manifests/temporal/README.md`):

```sh
temporal schedule describe --schedule-id release-watch --namespace release-watch --address 127.0.0.1:7236
temporal schedule trigger --schedule-id release-watch --namespace release-watch --address 127.0.0.1:7236
temporal workflow list --namespace release-watch --address 127.0.0.1:7236 --limit 5
```

## Apply (from `k8s/`)

Temporal first:

```sh
kubectl apply -f manifests/temporal/authz-configmap.yaml
kubectl -n temporal rollout restart deploy/temporal
kubectl -n temporal port-forward deploy/temporal 7236:7236 &
temporal operator namespace create --namespace release-watch --retention 720h --address 127.0.0.1:7236
```

then this directory, the rules and the routes:

```sh
kubectl apply -f manifests/release-watch/namespace.yaml
kubectl apply -f manifests/release-watch/
kubectl apply -f manifests/prometheus/rules/release-watch.yaml
kubectl apply -f manifests/prometheus/alertmanager-config.yaml
```

The image's tag is the store hash of its contents:

```sh
nix build .#release-watch-image
skopeo copy docker-archive:result docker://oci.garvey.sh/release-watch:$(nix eval --raw .#release-watch-image.imageTag)
```
