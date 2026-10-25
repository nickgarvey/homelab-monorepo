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
lifecycle rule expires objects after 30 days. RGW credentials (user
`openbao-snapshot`, one bucket max) and the AppRole IDs are in
`secrets/openbao-snapshot.yaml`.

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
