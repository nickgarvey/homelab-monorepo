# OpenBao

OpenBao is the issuing tier of the private CA (`pki/README.md` has the root). It
runs as a plain systemd service on fus, ro and dah, a 3-node Raft cluster, so it
keeps working when k3s is down. Two of three nodes must be up for anything to
work: issuance, logins, configuration.

| | |
|---|---|
| Module | `modules/services/openbao.nix` |
| Configuration (mounts, auth, policies) | `pki/openbao/` (OpenTofu) |
| Clients | `https://openbao.home.garvey.sh:8200` (AAAA for all three nodes; standbys forward to the active node) |
| Per node | `https://openbao-<node>.home.garvey.sh:8200` (Raft peers' API addresses), cluster port 8201 |
| Loopback, per node | `http://127.0.0.1:8202`: the node's own tooling, and recovery |
| Secrets | `secrets/openbao*.yaml` (`.sops.yaml` lists who can read each) |
| Alerts | `k8s/manifests/prometheus/rules/openbao.yaml` |

## How it starts

- **Unseal:** the static seal reads a 32-byte hex key from `secrets/openbao.yaml`
  (via `LoadCredential`), so a node unseals itself at every start. The key file
  must not end in a newline: the seal does not trim it.
- **Listener certificate:** each node gets a Let's Encrypt certificate for
  `openbao.home.garvey.sh` plus its own `openbao-<node>` name, by DNS-01 against
  Knot with the `acme-openbao` TSIG key (`docs/tls.md`). Renewal reloads OpenBao
  (SIGHUP), which re-reads the certificate. Until the first issuance the listener
  serves lego's self-signed placeholder and the nodes cannot join each other.
  The three nodes validate the shared name at once, so one can lose the race
  (Let's Encrypt sees another node's TXT value) and fail; a failed first order
  sits in a long restart back-off, so start it by hand:
  `systemctl restart acme-order-renew-openbao.home.garvey.sh`. At renewal the
  loser retries on its next run, well inside the 30-day window.
- **Audit log:** every request goes to the journal of the node that served it
  (`journalctl -u openbao`). OpenBao 2.x takes audit devices from its config file
  only, so it is in the module, not OpenTofu.
- `restartIfChanged = false` (upstream module): a deploy that changes OpenBao's
  configuration does not restart it. Restart the nodes one at a time and wait for
  each to be unsealed again:

  ```sh
  ssh <node> sudo systemctl restart openbao
  ssh <node> curl -s http://127.0.0.1:8202/v1/sys/health   # "sealed":false
  ```

## Deploys

The deploy tool's Raft gate (`packages/deploy/openbao.go`) refuses to deploy one of
fus/ro/dah unless the other two are initialised and unsealed, and afterwards waits
for the deployed node to be unsealed. `--force` skips it: needed for the first
rollout (nothing is initialised) and when two nodes are already broken and the
deploy is the fix.

## Admin login

```sh
nix shell nixpkgs#openssl nixpkgs#curl nixpkgs#jq -c bash pki/openbao/login.sh > /dev/null  # prints a token
export BAO_ADDR=https://openbao.home.garvey.sh:8200 BAO_TOKEN=$(bash pki/openbao/login.sh)
```

`login.sh` signs a one-minute JWT (RS256) with `~/.ssh/id_rsa`, the talos SSH key,
and exchanges it at `auth/jwt/login` for a one-hour `admin` token. The JWT auth
method pins two public keys (`pki/openbao/*.pub.pem`): the talos key, and an
offline key kept in `secrets/openbao-admin.yaml` (recipients: the user and the
paper key). Without talos:

```sh
bash pki/openbao/login.sh <(sops decrypt --extract '["offline_admin_key"]' secrets/openbao-admin.yaml)
```

## Configuration changes

```sh
nix shell nixpkgs#opentofu nixpkgs#sops nixpkgs#openssl nixpkgs#curl nixpkgs#jq \
  -c bash pki/openbao/tofu.sh plan     # or apply
```

`tofu.sh` decrypts the state passphrase from `secrets/openbao-admin.yaml`, logs in
with `login.sh` unless `VAULT_TOKEN` is set, and runs OpenTofu. The state
(`pki/openbao/terraform.tfstate`) is committed, encrypted. Run `tofu init` once in
a fresh checkout. OpenBao stores PEM keys without their trailing newline, hence the
`trimspace` in `main.tf`.

## Recovery without talos

1. **Offline key:** log in as above with the offline admin key.
2. **Recovery key:** if no login works, generate a root token on any node's
   loopback listener (the only listener with the unauthenticated generate-root
   endpoints), using the recovery key from `secrets/openbao-recovery.yaml`:

   ```sh
   L="curl -fsS http://127.0.0.1:8202/v1/sys/generate-root"
   ssh <node> "$L/attempt -X PUT --data {}"          # note .nonce and .otp
   jq -n --arg k "$(sops decrypt --extract '["recovery_key"]' secrets/openbao-recovery.yaml)" \
     --arg n <nonce> '{key: $k, nonce: $n}' | ssh <node> "$L/update -X PUT --data @-"
   bash pki/openbao/decode-root-token.sh <encoded_token> <otp>   # needs perl
   ```

   The `bao operator generate-root` CLI is no use here: its commands read the
   attempt's status from an authenticated endpoint, even on the loopback listener.
   Use the root token to re-pin an admin key (`pki/openbao/main.tf`), then revoke
   it. A run that is abandoned midway leaves a root token nobody can decode; revoke
   it by accessor (`auth/token/accessors`, `auth/token/revoke-accessor`).

The static seal key cannot generate a root token; only the recovery key can. Both
paths were tested on 2026-10-25.

## Snapshots and restore

The `openbao-snapshot` timer runs daily on all three nodes; only the active node
(sys/health 200) saves a Raft snapshot and uploads it to the `openbao-snapshots`
bucket on Ceph RGW (`rgw.home.garvey.sh`, on joor/zah/frul). It logs in with the
`snapshot` AppRole, whose secret ID and tokens are bound to 127.0.0.1. The bucket's
lifecycle rule expires objects after 30 days. RGW credentials and the AppRole IDs are
in `secrets/openbao-snapshot.yaml`. The RGW user `openbao-snapshot` is in the `k8s`
account (`RGW77873637642885287`), which owns the bucket, so the Ceph backup copies the
snapshots off Ceph with the account's other buckets (`docs/ceph-backup.md`). Its only
permission is an inline IAM policy, `openbao-snapshots-bucket`, allowing `s3:*` on that
bucket; `radosgw-admin` cannot set inline user policies, so it was put with `aws iam
put-user-policy` as a temporary account-root user (`--account-root`), removed afterwards.

A snapshot is encrypted by the seal: restoring needs the same static seal key.
To restore onto a fresh single node and then re-grow the cluster: start the node
with the seal key, `bao operator init` it (a throwaway init; its root token is only
for the restore), then

```sh
bao operator raft snapshot restore -force <file>.snap
```

After the restore the node holds the snapshot's data, including its auth methods,
so `login.sh` works against it. Tested on 2026-10-25 with a throwaway node on talos.
Re-grow by starting the other nodes empty: `retry_join` brings them in.

## SSH certificates

The `ssh-user` mount is an Ed25519 SSH CA (`pki/openbao/main.tf`); every NixOS host
trusts it (`TrustedUserCAKeys` in `modules/core/nixos-common.nix`, public key in
`pki/openbao/ssh-user-ca.pub`). A certificate logs in as the account named in its
principals; the `ngarvey` role signs for `ngarvey` only, for 8 hours.

On talos, `modules/desktop/openbao-ssh-cert.nix` keeps
`~/.local/state/openbao-ssh/deploy` (a key in no `authorized_keys`) signed: an hourly
user timer renews once fewer than 2 hours remain, logging in with `login.sh`. The
system ssh config offers that key first and `~/.ssh/id_rsa` second, so deploys and
logins use the certificate, and fall back to `id_rsa` (the break-glass key, still in
`authorized_keys`) when the certificate has expired and OpenBao is unreachable.
Logging in with a certificate never contacts OpenBao.

The key is kept out of `~/.ssh` because gcr-ssh-agent loads every `~/.ssh` key that
has a `.pub` beside it, certificate included, and would keep each expired
certificate as another identity to offer first until sshd's `MaxAuthTries` ran out.

```sh
systemctl --user start openbao-ssh-cert     # renew now
ssh-keygen -L -f ~/.local/state/openbao-ssh/deploy-cert.pub
```

sshd logs a certificate login as `ED25519-CERT ... ID ngarvey-jwt-ngarvey`.
Replacing the CA means updating `ssh-user-ca.pub` and deploying every host before
the old CA's certificates stop being issued.

### Build helpers

talos's nix-daemon logs in to lydia and wabbajack as `nix-ssh` with
`/var/lib/nix-builder-ssh/id_ed25519` and an 8-hour certificate from the `nix-ssh`
role, renewed hourly by `openbao-builder-cert.service` (talos-builder AppRole,
`secrets/openbao-talos-builder.yaml`). The helpers have no authorized key for
`nix-ssh`: the certificate is the only way in. If OpenBao is down long enough for
it to lapse, talos builds locally until the next renewal.

The helpers present host certificates from the separate `ssh-host` CA
(`pki/openbao/ssh-host-ca.pub`, role `builder-host`, 30 days, renewed with 10 left
by `openbao-host-cert.service`, `secrets/openbao-builder-host.yaml`). talos trusts
them through an `@cert-authority` line in `/etc/ssh/ssh_known_hosts` instead of
pinned host keys. A new helper needs its name added to the `builder-host` role's
`allowed_domains` and to that known_hosts entry.

## Cluster CA

The `pki-cluster` mount holds the cluster CA's key, `cluster-ca-2026` (EC P-256),
generated by OpenTofu (`pki/openbao/main.tf`); it has never left OpenBao. The root signed
its CSR on talos: `pki/cluster-ca.crt`, "Garvey Homelab Cluster CA 2026", extensions in
`pki/cluster-ca.ext` (pathlen 1; SPIFFE IDs in `home.garvey.sh`, `*.svc`,
`*.cluster.local` and `*.k8s.home.garvey.sh`; no IPs or emails). It is the default
issuer; `root-2026` (`pki/root.crt`, certificate only) completes its chain. cert-manager
writes `ca.crt` = the root and `tls.crt` = leaf + `cluster-ca.crt`; under the root, a
name outside the constraints fails.

Replacing the key: generate a new key and CSR the same way, have the root sign it, import
it, make it the default, rotate SPIRE (`spire-server localauthority x509 prepare` and
`activate`) and renew every `cluster-ca` Certificate. Verifiers trust only the root, so
nothing else changes. Test a new chain with Go's `x509.Verify` as well as OpenSSL: Go
matches a certificate to its issuer by raw name bytes.

Two clients sign here, each logging in through the `k8s` JWT mount with a k3s
service account token (audience `openbao`), checked against
`pki/openbao/k3s-sa.pub.pem` without calling the API server:

- cert-manager (service account `cert-manager/openbao-issuer`, role `cert-manager`)
  signs through `pki-cluster/sign-verbatim/cluster-leaf`, which keeps the CSR's
  subject, SANs and key usages. approver-policy decides what may be requested.
- SPIRE (`spire/spire-server`, role `spire-server`) signs its 24-hour CA through
  `pki-cluster/root/sign-intermediate`. Its policy allows exactly the parameters
  SPIRE sends, so a longer TTL, a path length or extra names are refused.

If the k3s service account signing key changes, logins fail until
`k3s-sa.pub.pem` is refreshed from `kubectl get --raw /openid/v1/jwks` and applied.

## Hosts CA

The `pki-dns` mount issues `<name>.home.garvey.sh` by ACME, 7-day certificates usable as
both server and client certificates. Its key was generated in OpenBao and never left it;
the root signed its CSR (`pki/dns-ca.crt`, extensions in `pki/dns-ca.ext`): names under
`home.garvey.sh` except `k8s.home.garvey.sh`, no IPs, emails or URIs, pathlen 0.

| | |
|---|---|
| ACME directory | `https://openbao.home.garvey.sh:8200/v1/pki-dns/acme/directory` |
| Role | `host` (every order): subdomains of `home.garvey.sh`, `serverAuth` + `clientAuth` |
| Proof | DNS-01 in Knot, checked by OpenBao against Knot through NAT64 |
| Host module | `modules/services/pki-dns-cert.nix` (lego via `security.acme`, renews with 3 days left) |
| Alerts | `PkiDnsCertExpiring`, `PkiDnsCertMissing` in `k8s/manifests/prometheus/rules/openbao.yaml` |

Who may get a name is decided by who can write its DNS-01 challenge: each host has a Knot
TSIG key scoped to its own `_acme-challenge` name (`pkiDnsHosts` in
`modules/containers/knot-auth.nix`, secret `secrets/pki-dns-<host>.yaml`). The role itself
accepts any subdomain, including under `k8s.home.garvey.sh`; such a certificate fails
verification under the root because of the constraint.

A new host: add it to `pkiDnsHosts`, create `secrets/pki-dns-<host>.yaml` (a 32-byte
base64 `acme_tsig_secret`, with a `.sops.yaml` rule for ngarvey, dragonsreach and the
host), import the module, and deploy dragonsreach before the host.

**Logging in with it.** The listener keeps its Let's Encrypt certificate and asks every
client for a certificate. The `cert` auth role `hosts` trusts only `pki/root.crt` and
accepts the SANs in its `allowed_dns_sans`, so a host can authenticate to OpenBao without a
stored secret:

```sh
sudo curl --cert /var/lib/acme/lydia.home.garvey.sh/fullchain.pem \
  --key /var/lib/acme/lydia.home.garvey.sh/key.pem \
  -X POST -d '{"name":"hosts"}' https://openbao.home.garvey.sh:8200/v1/auth/cert/login
```
