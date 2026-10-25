# How TLS works in this homelab

Certificates come in three kinds, and each has one job:

| Kind | Verified by | Issued by |
|---|---|---|
| Public | anyone: browsers, phones, devices, other hosts | Let's Encrypt, DNS-01 written into our Knot zone |
| Private, service to service | only services we configure to trust `pki/root.crt` | OpenBao's intermediates under our one root ([below](#private-certificates-service-to-service)) |
| Kubernetes internal (apiserver, kubelet, etcd, Cilium) | k3s components | k3s itself; nothing here touches it, and it never appears on the LAN |

A browser never sees a private certificate: anything a person opens uses a public one.

Configuration is split across two trees, so paths below are prefixed when ambiguous:
the NixOS tree (`modules/`, `hosts/`) owns the router, DNS, and host-level pieces; `k8s/`
owns cert-manager and the services themselves.

---

## Public certificates: the two ways one gets issued

| Path | Names | Issued by | Config |
|---|---|---|---|
| cert-manager | `<name>.home.garvey.sh` and `<name>.garvey.sh` for cluster services | Let's Encrypt, DNS-01 via RFC2136 | `k8s/manifests/cert-manager/cluster-issuer-rfc2136-{prod,staging}.yaml`; names in `certManagerAcme`, `modules/networking/dns.nix` |
| On-box ACME | `homeassistant.home.garvey.sh`, `garage.home.garvey.sh`, `storj-gateway.home.garvey.sh`, `rgw.home.garvey.sh`, `openbao.home.garvey.sh` (+ `openbao-<node>`) | Let's Encrypt, DNS-01 via RFC2136 | HAOS itself / `security.acme` on lydia + wabbajack / `security.acme` on dragonsreach / `security.acme` on joor + zah + frul (Ceph RGW) / `security.acme` on fus + ro + dah (OpenBao, `docs/openbao.md`); TSIG ACLs in `modules/containers/knot-auth.nix` |

Both write the challenge TXT straight into Knot with a TSIG key that may write only TXT at
its own challenge names.

### 1. cert-manager — the default for cluster services

cert-manager's `letsencrypt-rfc2136-prod` issuer holds the `acme-cert-manager` TSIG key
(`k8s/manifests/cert-manager/secret.yaml`) and reaches Knot through NAT64. Knot allows it
TXT at `_acme-challenge.<name>.home.garvey.sh` for each `home` name in `certManagerAcme`,
and at `<name>.challenge.home.garvey.sh` for each `garveySh` name. A `garvey.sh` name has
a one-time Cloudflare CNAME from `_acme-challenge.<name>.garvey.sh` to that second form,
which cert-manager follows (`cnameStrategy: Follow`); no Cloudflare credential exists in
the cluster, and renewals edit nothing at Cloudflare. The `cert-manager-acme-names` flake
check keeps the list equal to the Certificates.

**The cert is decoupled from where the service points.** DNS-01 proves control of the
name in DNS, not via `<host>`'s address. You can issue a cert before the service exists,
and changing a service's IP never affects issuance.

cert-manager checks propagation through cluster CoreDNS, then blocky, kresd and Knot
(`k8s/vendor/cert-manager/values.yaml` explains why not the published NS). Its zone
discovery queries the challenge name before the TXT exists, and that NXDOMAIN is cached
for at most Knot's 300-second negative TTL, so a check can wait up to five minutes.

Onboarding a name: `k8s/manifests/cert-manager/ONBOARDING.md`.

### 2. On-box ACME against our own Knot

For the things cert-manager cannot deliver a cert to — Home Assistant, which runs HAOS
rather than NixOS, and the two S3 endpoints, which run in nspawn containers (garage on
lydia and wabbajack, the Storj gateway on dragonsreach) — the box issues its own cert with
DNS-01 by **RFC2136 dynamic update against our own Knot**.

The interesting part is the ACLs in `modules/containers/knot-auth.nix`. Every TSIG key
handed out is scoped to a single owner name and a single record type:

```
update-owner-name = [ "_acme-challenge.homeassistant.home.garvey.sh." ]
update-type       = [ "TXT" ]
```

So compromising a box that holds one cannot repoint any real record in the zone — it can
only write TXT at that one challenge name. That scoping is what makes it acceptable to take
dynamic updates into the real zone at all. It is also why the `acme-garage`
key can be shared by *every* garage node: the ACL is scoped to the one name they all write,
not to a host, so adding a node is a new recipient on `secrets/garage-acme.yaml` and
nothing else.

Knot's zone is loaded from the Nix store and never written back
(`zonefile-sync = -1`), so the zone stays reproducible in git while still accepting these
dynamic TXT updates — `zonefile-load = "difference"` plus `journal-content = "changes"` is
what keeps an in-flight challenge from being wiped by an unrelated config reload.

**Garage's shape is worth knowing**, because the cert is issued on one machine and used on
another. Garage speaks no TLS itself, so nginx inside the container terminates it and
proxies to garage on the loopback; the container has no route to the internet
(`modules/containers/common.nix` gives it no IPv6 default route and no IPv4 at all), so
lego runs on the *host* and `/var/lib/acme/garage.home.garvey.sh` is bind-mounted in.
nginx reads its cert at startup, so the renewal hook is a `systemctl -M garage restart
nginx.service` from the host's acme unit.

Two consequences: **each node issues its own copy** of the same name, so a cluster-wide
first issuance spends one of Let's Encrypt's 5-per-week duplicate certificates per node
(renewals stagger themselves and come nowhere near it); and **HTTPS is the only way in**:
with `tls.enable`, the plaintext API on 3900 binds the container's loopback, for nginx
alone. A certificate that fails to renew therefore takes that node's S3 endpoint down when
it expires, rather than degrading to plaintext; the certificate-expiry alerts are what
catch it first. Every client uses `https://garage.home.garvey.sh`.

**The Storj gateway's shape is different, and the difference is instructive.** gateway-st
embeds minio, which reads its own certificate from `<config-dir>/certs/{public.crt,
private.key}` — `main.go` passes `--config-dir` and no `--certs-dir`, so the certs
directory falls out of the config directory. There is no proxy: an import unit inside the
container copies the host-issued pair into `/var/lib/storj-gateway/minio/certs` under the
names minio expects, and the renewal hook restarts the gateway.

**The degradation is not symmetric, and that bit us.** minio serves plain HTTP when it
finds *no* certificate, but a certificate it cannot load is fatal — it aborts at startup.
`security.acme` always plants a minica self-signed placeholder before the first successful
order, and minica issues P-384, which minio rejects outright (Go has no constant-time
P-384; `storj.io/minio` `cmd/config/certs.go:102`). So a first order that fails does not
leave the gateway on HTTP — it leaves it crash-looping. The import unit therefore checks
the key's curve and declines to install anything minio would refuse, which is what makes
the "falls back to HTTP" claim actually true.

The cost is that minio has exactly one listener, so **port 7777 converts** rather than
gaining a sibling: there is no plaintext port left to migrate consumers off gradually. That
was affordable here because Mimir is the only consumer. Prefer garage's shape when a
service has several.

One more local quirk: dragonsreach runs both lego *and* the Knot it updates, so a rebuild
restarts them together. An order that starts before knot-auth is listening dies with a
socket-level refusal — `could not find the start of authority ... connection refused` —
and spends a Let's Encrypt failed-validation slot. The order unit waits for the zone's SOA
to be answered before running. The garage nodes need no such wait: they query Knot across
the LAN, where it is already up.

**`dnsPropagationCheck = false` is load-bearing, not a shortcut.** lego's default
propagation check ignores `dnsResolver`: it looks up the zone's NS (`ns1` → our WAN
address) and queries *that*. From the LAN the `:53` DNAT does not apply — it matches on the
WAN interface — so the query lands on blocky rather than dnsdist, and blocky's 1m negative
cache (`modules/router/blocky-dns.nix`) outlives lego's 60s propagation timeout. It fails
*intermittently*: one node can win the race against the
cache entry while the other times out with

```
propagation: time limit exceeded: last error: authoritative nameservers:
NS ns1.home.garvey.sh.:53 returned REFUSED for _acme-challenge.garage.home.garvey.sh.
```

Nothing is lost by disabling it. Knot is the only authoritative server and its DDNS update
is committed before lego proceeds, so there is no propagation to await; Let's Encrypt
validates from the public internet, where the DNAT does apply. Any future on-box ACME
client on this network needs the same setting.

---

## Private certificates: service to service

One root, `pki/root.crt` ("Garvey Homelab Root CA 2026"; its key is sops-encrypted in
`pki/root-key.yaml`, `pki/README.md`), signs intermediates that live in OpenBao on fus, ro
and dah (`docs/openbao.md`), so issuance keeps working when k3s is down. Each intermediate
carries name constraints, so a stolen intermediate key can only name what its tier owns:

| Intermediate | Names it may certify | Who gets certificates |
|---|---|---|
| `pki-cluster` (`pki/cluster-ca.crt`) | SPIFFE IDs in `home.garvey.sh`, `*.svc`, `*.cluster.local`, `*.k8s.home.garvey.sh` | cluster workloads: cert-manager's `cluster-ca` ClusterIssuer and SPIRE's 24-hour CA (`k8s/manifests/cert-manager/`, `k8s/manifests/spire/`) |
| `pki-dns` (`pki/dns-ca.crt`) | `*.home.garvey.sh` except `k8s.home.garvey.sh` | hosts, through ACME, 7 days (`modules/services/pki-dns-cert.nix`; docs/openbao.md, "Hosts CA") |

**The root goes only where private certificates are verified.** It is not in any host's
system trust store; a service that checks private certificates gets it as a file (a Secret's
`ca.crt`, a pinned copy, SPIRE's bundle). Because every private certificate chains to the same
root, a workload in the cluster and a service on a host can authenticate each other.

**Server certificate and client CA are independent.** A service can keep its Let's Encrypt
server certificate, which every client already trusts, and still require a private client
certificate by trusting the root only as its client CA, then authorize by the client's SAN.
Peers send their intermediate with the leaf, so trusting the root alone is enough.

**There is no revocation; lifetimes are short instead.** Cluster leaves last a day (a few
long-lived server certificates a year), SPIRE SVIDs four hours, SPIRE's own CA a day, and
hosts' certificates seven days. A compromised workload stops being able to renew once its
access is removed; a compromised intermediate is bounded by its name constraints and is
replaced by signing a new one from the root, which stays offline.

---

## How TLS is terminated

**Services that serve TLS terminate it themselves, on port 443 directly** — not on a high
port behind a LoadBalancer port map. `zot` and `jellyfin` both do this: the cert-manager
Secret is mounted into the pod and the app reads it.

```yaml
# k8s/manifests/zot/configmap.yaml
"port": "443",
"tls": { "cert": "/etc/zot/tls/tls.crt", "key": "/etc/zot/tls/tls.key" }
```

Keeping the listener on 443 means the public URL needs no port suffix and the LB mapping
stays a straight 443→443.

## Split-horizon DNS keeps the cert name honest

A public name like `jellyfin.garvey.sh` resolves publicly to the WAN address. From inside
the LAN, hairpinning out and back would be wasteful — but simply pointing clients at the
internal IP would break TLS, because the cert is for the public name.

The fix is in `modules/networking/dns.nix`:

```nix
garveyShOverrides = {
  "oci.garvey.sh"      = "2001:470:482f:2::5000";
  "jellyfin.garvey.sh" = "2001:470:482f:2::5001";
};
```

blocky serves these from `customDNS`, so LAN clients reach the LoadBalancer directly **and
still verify against the public name**. Add any future public-named service here.

---

## The fragile part: WAN IP → DNS-01 → renewals

This is the failure mode most worth understanding, because it is silent.

Every DNS-01 renewal requires Let's Encrypt to reach our authoritative DNS from the public
internet — `ns1.home.garvey.sh` for the Knot zone, which `garvey.sh` challenges also reach
through their CNAMEs. It publishes our **DHCP-assigned WAN address**, hardcoded in
Cloudflare glue and in `modules/networking/zone.nix`, with **no DDNS automation**.

When that address drifts, renewals begin failing immediately, and nothing else notices for
weeks — until certificates approach expiry. So there are two layers of alerting:

- **Early warning:** `modules/router/wan-dns-drift.nix` resolves our published records
  through a public resolver and compares them to the WAN interface's actual address.
  `PublishedWanIPDrift` fires within 15 minutes
  (`k8s/manifests/prometheus/rules/wan-dns.yaml`).
- **Backstop:** the cert-manager rules in
  `k8s/manifests/prometheus/rules/certificates.yaml` — not-Ready for an hour,
  under 21 days remaining (cert-manager renews at 30, so this means a renewal already
  failed), and under 7 days as critical. `CertManagerMetricsMissing` covers the case where
  cert-manager itself is down, which would otherwise look identical to "no problems".

If drift happens, fix it in two places: Cloudflare glue for `home.garvey.sh`, and `ns1` in
`modules/networking/zone.nix`.

### Home Assistant needs its own probe

cert-manager has no visibility into HA's on-box cert, and expiry alone would not catch the
real failure mode: **HA only loads its certificate at startup**, so a renewal that isn't
followed by a restart leaves it serving the old one indefinitely.

`modules/services/ha-cert-probe.nix` (runs on fus) therefore checks what HA actually serves
on the wire, asserting hostname match *and* chain validity against the system trust store —
not just remaining lifetime:

```
openssl s_client -connect homeassistant.home.garvey.sh:8123 \
  -verify_hostname homeassistant.home.garvey.sh -verify_return_error
```

This is also the canary for trust-store changes on NixOS hosts: it is the one check that
would notice if something still depended on a CA we removed.

---

## Retired: the Garvey Home CA

A private CA (`Garvey Home Root CA` → `Garvey Home Intermediate CA`, hand-issued via XCA in
March 2024) used to sign internal services. It was **fully retired in August 2026** once
Home Assistant — its last real consumer — migrated to on-box Let's Encrypt.

Removed at that time:

- `public_certs/` and the `security.pki.certificateFiles` entry in
  `modules/core/nixos-common.nix` that installed it into every NixOS host's trust store
- The `trmnl-display-ca` ConfigMap and its volume mount

Verified before removal: no endpoint on the LAN serves a cert chaining to it (probed all
hosts in `modules/networking/lan-hosts.nix` across the common TLS ports), and no
cert-manager Issuer is a `ca` or `selfSigned` issuer.

**If you ever need it back**, both certs are recoverable from git history, and the private
key lives in XCA outside these repos.

### `k3s-api.home.arpa` is still load-bearing

The name comes from the retired `home.arpa` zone but is still in use, so do not drop it
without migrating every consumer to another name first:

- `modules/k3s/k3s-common.nix`: `serverAddr` for joining nodes, and `--tls-san` on the
  API server cert
- `k8s/vendor/cilium/values.yaml`: `k8sServiceHost` for the Cilium agents and
  operator
- `modules/router/blocky-dns.nix`: keeps the record resolving (from `k3s-api` in
  `modules/networking/dns.nix`)

---

## Adding a new public service: the short version

This is the in-cluster path. A service on a NixOS host instead takes path 2 above: add a
TSIG identity to `acmeKeys` in `modules/containers/knot-auth.nix`, a sops file holding the
key encrypted to both dragonsreach and the host, and `security.acme` with
`dnsProvider = "rfc2136"` — see `modules/containers/garage.nix` for a worked example.

1. Add the name to `certManagerAcme` in `modules/networking/dns.nix` (for a `garvey.sh`
   name, also the Cloudflare CNAME) and deploy dragonsreach — details in
   `k8s/manifests/cert-manager/ONBOARDING.md`.
2. Add a `Certificate` with `issuerRef: letsencrypt-rfc2136-prod`. Validate against
   `letsencrypt-rfc2136-staging` first if the name is at all unusual; Let's Encrypt's prod
   rate limits are unforgiving.
3. Mount the resulting Secret in the pod and terminate TLS on **443**.
4. Add the address record (`records` for `home.garvey.sh`, `garveyShOverrides` for
   `garvey.sh`) in `modules/networking/dns.nix` so LAN clients get the internal address
   without breaking verification.
5. Confirm `certmanager_certificate_ready_status` goes to 1 — the alerts above will
   otherwise tell you about it an hour later.

**Note that a LoadBalancer Service publishes its name and address publicly.** The
`home.garvey.sh` zone is delegated from Cloudflare and resolves from the open internet, so
`<svc>.<ns>.k8s.home.garvey.sh` is enumerable by anyone. Resolvable is not reachable —
inbound IPv6 is default-deny at the router — but don't treat an internal address as secret.
