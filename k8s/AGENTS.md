# Agent Guidelines

Paths in this file, and in the docs and comments under `k8s/`
(`manifests/...`, `vendor/...`), are relative to `k8s/`, and the commands
shown here run from `k8s/`.

## Destructive Actions

Deleting things is allowed when it is part of the task. What still needs the
user's explicit OK is destroying **data that cannot be recreated**.

### Allowed without asking

- Deleting files: repo files (git has the history), and temporary files,
  including ones the agent created.
- Deleting or recreating stateless Kubernetes resources as part of a change
  the user asked for: pods, Deployments, StatefulSets (the objects, not their
  volumes), Services, ConfigMaps and the like.

### Ask first

- Persistent data: PVCs, PVs, Longhorn/Ceph volumes, and namespaces that
  hold them.
- Backups and object storage: Longhorn backups, Kopia snapshots, S3 buckets
  or their objects.
- Secrets whose value exists nowhere else (not already in sops).
- Cluster resets, etcd operations, and rebooting nodes.

When unsure whether something holds unrecoverable data, ask.

---

# Vendored components

`vendor/` holds upstream components; charts are rendered there by
`scripts/render-chart.sh` from a checked-in `values.yaml`, and static files are
downloaded by `scripts/fetch-vendor.sh`. Never hand-edit either; local changes go
in `values.yaml` or a kustomization. `vendor/README.md` lists each component,
its source and version, and its apply command.

---

# Network Configuration

The cluster is **IPv6-only** (`ipv4.enabled: false` in `vendor/cilium/values.yaml`).
Pods, Services and LoadBalancers have IPv6 addresses only; IPv4 exists on the LAN and for
the nodes' host addresses, not inside the cluster.

Those addresses are a routed Hurricane Electric `/48`, so they are **globally routable
GUAs, not ULAs**. Inbound `he-ipv6 → LAN` is default-deny at the router, so exposure is
opt-in per address — but an address being "internal" does not make it private.

## Where the network facts live

The NixOS tree at the repo root (`modules/`, `hosts/`) is the source of truth. Read them there rather than copying them here,
where they drift:

| What | File |
|------|------|
| Subnet plan for `2001:470:482f::/48`, node IPs/MACs, pod CIDRs | `modules/networking/lan-hosts.nix` |
| LAN CIDR, router interfaces | `modules/router/default.nix` |
| DHCP pool and reservations | `modules/router/dhcp.nix` |
| Cluster/Service CIDR, API TLS SANs, registry mirror | `modules/k3s/k3s-common.nix` |
| Firewall, WAN port forwards | `modules/router/nftables.nix` |
| Authoritative DNS, resolver, public DNS front door | `modules/containers/knot-auth.nix`, `modules/router/knot-resolver.nix`, `modules/containers/public-dns-proxy.nix` |

## LoadBalancers — Cilium LB-IPAM, not MetalLB

There is no MetalLB. Cilium allocates from the IPv6 pool in
`manifests/cilium/l2-config.yaml` and announces via L2. Pin an address with the
`io.cilium/lb-ipam-ips` annotation plus `ipFamilies: [IPv6]` /
`ipFamilyPolicy: SingleStack`.

Before picking one, list what is already taken:

```bash
grep -rh lb-ipam-ips manifests/*/*.yaml | sed 's/.*: //' | tr -d '"' | sort
```

---

# DNS

Names come from four places. Pick by what the service needs, not by what is least
work — the first two cost nothing but cannot carry a publicly-trusted cert.

| Scheme | Who serves it | Cert | Use it when |
|---|---|---|---|
| `<svc>.<ns>.k8s.home.garvey.sh` | `k8s_gateway` (`manifests/k8s-gateway/`), auto-generated from every LoadBalancer Service | none | You need *a* name and don't care which. Free — it appears the moment the Service exists. |
| `<name>.home.garvey.sh` | our own Knot zone, from `modules/networking/dns.nix` | Let's Encrypt via acme-dns | **Preferred for a real cert.** Delegation lives in git, no Cloudflare credentials, no split-horizon override. `anki`. |
| `<name>.garvey.sh` | Cloudflare | Let's Encrypt via acme-dns | The name genuinely belongs on the apex domain. Needs a CF token and a `garveyShOverrides` entry. `oci`, `jellyfin`, `temporal`. |

Both cert-bearing schemes are covered by `manifests/acme-dns/ONBOARDING.md`, which
forks per zone at step 2.

An app that cannot terminate TLS itself gets a Caddy sidecar — see `manifests/jellyfin/`
and `manifests/anki/`.

## ⚠️ LoadBalancer Services are publicly enumerable

`home.garvey.sh` is **publicly delegated** from Cloudflare to a self-hosted Knot server on
the router, which in turn delegates `k8s.home.garvey.sh` to `k8s_gateway`. The full chain
resolves from the open internet:

```
. → sh → garvey.sh (Cloudflare) → home.garvey.sh (Knot) → k8s.home.garvey.sh (k8s_gateway)
```

So **creating a LoadBalancer Service publishes its name and GUA to anyone who asks.**
Resolvable is not the same as reachable — inbound IPv6 is still default-deny at the
router — but do not treat an address as secret because it is "internal".

---

# Container Registry (zot)

Registry: `oci.garvey.sh` (TLS, Let's Encrypt prod cert)

Pulls are anonymous. Push and delete need the `ngarvey` account: zot holds its
bcrypt hash (`manifests/zot/secret.yaml`), and the workstations (talos,
dovahkiin) get the credential as `~/.config/containers/auth.json` from
`modules/desktop/zot-push.nix`, so skopeo and podman push there
with no login step. docker and crane don't read that file.

## Quick Commands

List all repositories:
```bash
curl -s https://oci.garvey.sh/v2/_catalog | jq
```

List tags (requires skopeo from nix environment):
```bash
nix develop --command skopeo list-tags docker://oci.garvey.sh/<repo>
```

Pull with podman:
```bash
podman pull oci.garvey.sh/<repo>:<tag>
```

Push a nix-built image (from a workstation):
```bash
skopeo copy docker-archive:result docker://oci.garvey.sh/<repo>:<tag>
```

