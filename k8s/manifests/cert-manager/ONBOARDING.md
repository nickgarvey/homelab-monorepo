# Adding a Let's Encrypt name

cert-manager proves control of a name with DNS-01, writing the challenge TXT into
our Knot zone with the `acme-cert-manager` TSIG key (`README.md`). Knot lets that
key write only at the names listed in `certManagerAcme` in
`modules/networking/dns.nix` (repo root), and the `cert-manager-acme-names` flake
check fails until that list matches the Certificates.

## Picking a hostname

| | `foo.home.garvey.sh` (our Knot zone) | `foo.garvey.sh` (Cloudflare) |
|---|---|---|
| List it in `certManagerAcme` | `home` | `garveySh` |
| Extra DNS step | none | Cloudflare CNAME `_acme-challenge.foo.garvey.sh` → `foo.challenge.home.garvey.sh` |
| Credentials | none | a Cloudflare `Zone:DNS:Edit` token |
| Address record | `records` in `dns.nix`; the AAAA is the LB IP for everyone | the public record is Cloudflare's; add a `garveyShOverrides` entry so internal TLS name-matches |
| Examples | `anki`, `grafana` | `oci`, `jellyfin`, `temporal` |

Prefer `home.garvey.sh`: everything is in git. Either way the address is only
resolvable from outside, not reachable; inbound `he-ipv6 → LAN` is default-deny.

## Steps

1. Add the name to `certManagerAcme` (and, for `garvey.sh`, create the Cloudflare
   CNAME). Deploy the router so Knot's ACL includes it:
   `nix develop -c deploy --hosts dragonsreach` from the repo root.
2. Add `manifests/foo/certificate.yaml` with `issuerRef: letsencrypt-rfc2136-staging`;
   once it is Ready, switch to `letsencrypt-rfc2136-prod` and delete the Secret so it
   reissues. Issuance does not need the service to exist. Run
   `nix build .#checks.x86_64-linux.cert-manager-acme-names` before committing.
3. Deploy the service (below), after the Secret exists.
4. Point the name at it in `modules/networking/dns.nix` (`records` for
   `home.garvey.sh`, `garveyShOverrides` for `garvey.sh`) and deploy the router.
5. Verify from the LAN: `curl -sS -o /dev/null -w '%{ssl_verify_result}\n' https://foo...`
   prints 0.

## Serving the certificate

Pick an unused LB IP (`grep -rh lb-ipam-ips manifests/*/service.yaml`).

**If the app can terminate TLS itself** (the zot pattern): it serves TLS on 443 with
the cert and key from the mounted `foo-tls` Secret; HTTPS probes on 443; the Service
pins `io.cilium/lb-ipam-ips` and maps 443 → 443.

**If it can't** (jellyfin, anki, grafana): add a Caddy sidecar, with
`manifests/jellyfin/` as the reference.
- The app stays on its plaintext port.
- `caddy-configmap.yaml`: `{ auto_https off; admin off }` and
  `:443 { tls /certs/tls.crt /certs/tls.key; reverse_proxy http://[::1]:<port> }`.
  Check the loopback address: the cluster is IPv6-only and some apps open no IPv4
  socket (`kubectl -n <ns> exec deploy/<app> -- cat /proc/net/tcp` empty means use
  `[::1]`).
- A `caddy:2-alpine` container in the same pod mounts the Caddyfile at `/etc/caddy`
  and the Secret at `/certs`.

Neither path hot-reloads a renewed certificate: Caddy's static `tls` directive reads
the files at startup only. Restart the workload after each renewal (about every 60
days); no Reloader is installed.
