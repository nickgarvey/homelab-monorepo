# matrix

The household's Matrix homeserver, Synapse, at `https://matrix.garvey.sh`.
The server name is `matrix.garvey.sh`, so IDs look like
`@ngarvey:matrix.garvey.sh`; it was picked so the server can federate later,
and it can never change without starting over.

- **LAN only.** The Service's address is reachable from the LAN and nowhere
  else: inbound IPv6 is default-deny at the router and no port is forwarded.
  `matrix.garvey.sh` resolves to it only inside, through `garveyShOverrides`
  in `modules/networking/dns.nix`; there is no public record.
- **No federation.** Synapse serves only the client API (no federation
  listener), allows no remote servers and asks no key servers.
- **TLS on 443 by a Caddy in Synapse's pod** (`caddy-configmap.yaml`), with a
  Let's Encrypt certificate (`certificate.yaml`, DNS-01 through Knot), so clients
  trust it with no setup. Synapse listens on loopback only.
- **State:** everything but media is in the `synapse` database on the shared
  postgres (`manifests/postgres`), which Synapse logs in to with a SPIFFE
  client certificate (`postgres-client-certificate.yaml`). Media is on the
  `synapse-media` volume (`pvc.yaml`, ceph-rbd).
- **Logins** go through the Matrix Authentication Service (MAS) at
  `https://matrix-auth.home.garvey.sh`, which sends the browser on to Authelia
  (its client `matrix` lets in only `ngarvey`). The Authelia username is the
  Matrix localpart: `ngarvey` signs in as `@ngarvey:matrix.garvey.sh`, and
  the first login makes the account. There are no Matrix passwords and no
  registration. See "Logins" below.
- **Push notifications** (once clients are signed in): Synapse calls the
  client's push gateway (Element's) outbound, so they work without anything
  reaching in.

The images are nixpkgs' Synapse with Postgres support only
(`packages/matrix-synapse`, `nix build .#matrix-synapse-image`) and nixpkgs'
MAS (`packages/matrix-authentication-service`), both tagged
`<version>-<store hash>`.

## Logins

MAS is its own site, `matrix-auth.home.garvey.sh` (`mas-*.yaml`), serving TLS
on 443 itself. It is in the `home.garvey.sh` zone, so its address and
certificate delegation are in `modules/networking/dns.nix` with no Cloudflare
record.

- **Clients with OAuth 2.0 login** (Element Web, Element Desktop, Element X)
  find MAS through Synapse's `/.well-known/matrix/client` and
  `/_matrix/client/v1/auth_metadata`, and open it in a browser.
- **Older clients** use `/_matrix/client/*/login`, `/logout` and `/refresh`.
  Synapse has none of these once MAS handles auth; the Caddy in its pod sends
  them to MAS's compatibility layer, which offers SSO login (through Authelia).
- **Synapse and MAS** call each other at their public names with the secret
  in `mas-secret.yaml`: Synapse asks MAS about each access token, and MAS
  makes and changes accounts through Synapse's admin API.
- **Appservices** (the agents bridge) are unaffected: Synapse checks their
  `as_token` itself, before asking MAS. MAS has no
  `m.login.application_service` login, which only encrypted bridges need.

## Clients

- **Desktop:** Element Desktop (`element-desktop`, installed on the
  workstations by `modules/desktop/common-workstation.nix`). Sign in with
  homeserver `matrix.garvey.sh`; it opens MAS in the browser, then Authelia.
- **Android:** Element X. Same homeserver; the login opens in the phone's
  browser.
- **What a device needs.** `matrix.garvey.sh` has no public DNS record and
  only an IPv6 address on the LAN. A device must use the router's DNS
  (Android's Private DNS and browsers' secure DNS go around it) and have an
  IPv6 route to the LAN; otherwise the app spins at "checking homeserver"
  without a request ever reaching Synapse. `matrix-auth.home.garvey.sh` does
  resolve publicly, so a half-working login points at the first name. Off
  the LAN, nothing works until the phone's route to the LAN is up.
- **Push notifications** (Element X): the app registers a pusher with
  Synapse for Element's push gateway (`matrix.org`'s Sygnal), which reaches
  the phone through Firebase. Synapse sends only room and event IDs
  (`push.include_content: false`); the phone then fetches the message from
  Synapse, so it shows only once the phone can reach the LAN. The pushers
  Synapse holds:
  `kubectl -n postgres exec postgres-1 -c postgres -- psql -U postgres -d synapse -c 'select user_name, app_id, app_display_name, device_display_name, data from pushers'`.

## Setup (from `k8s/`, once)

1. Postgres roles and databases. Synapse and MAS log in with client
   certificates, so the roles have no password:
   ```sh
   kubectl apply -f manifests/cert-manager/cluster-policies.yaml
   kubectl apply -f manifests/postgres/cluster.yaml -f manifests/postgres/databases.yaml
   kubectl -n postgres get database synapse mas      # APPLIED=true
   for db in synapse mas; do
     kubectl -n postgres exec postgres-1 -c postgres -- psql -U postgres -c \
       "REVOKE CONNECT ON DATABASE $db FROM PUBLIC; GRANT CONNECT ON DATABASE $db TO $db;"
   done
   kubectl -n postgres exec postgres-1 -c postgres -- psql -U postgres -tAc \
     "select datcollate, datctype from pg_database where datname = 'synapse'"   # C|C
   ```
2. Certificates (`manifests/cert-manager/ONBOARDING.md`): both names are in
   `certManagerAcme` (`modules/networking/dns.nix`). `matrix.garvey.sh` is in
   the Cloudflare zone, whose `_acme-challenge.matrix` CNAME points at
   `matrix.challenge.home.garvey.sh`; `matrix-auth.home.garvey.sh` is in ours.
   Both use `letsencrypt-rfc2136-prod`.
   ```sh
   kubectl apply -f manifests/matrix/namespace.yaml \
     -f manifests/matrix/certificate.yaml -f manifests/matrix/mas-certificate.yaml
   ```
3. Images, from a workstation:
   ```sh
   for img in matrix-synapse matrix-authentication-service; do
     nix build ..#$img-image
     skopeo copy docker-archive:result docker://oci.garvey.sh/$img:$(nix eval --raw ..#$img-image.imageTag)
   done
   ```
4. Authelia's client for MAS: `kubectl apply -f manifests/authelia/configmap.yaml`
   and `kubectl -n authelia rollout restart deploy/authelia`.
5. The rest. MAS first: Synapse checks every token with it.
   ```sh
   kubectl apply -f manifests/matrix/
   kubectl -n matrix rollout status deploy/mas
   kubectl -n matrix rollout status deploy/synapse
   ```
6. DNS: deploy the router (dragonsreach) for the `matrix.garvey.sh` override
   and the `matrix-auth` record.

## Checking it

From a LAN client:

```sh
dig +short matrix.garvey.sh AAAA                       # 2001:470:482f:2::5010
dig +short matrix-auth.home.garvey.sh AAAA             # 2001:470:482f:2::5011
curl -sS https://matrix.garvey.sh/_matrix/client/versions | head -c 200
curl -sS https://matrix.garvey.sh/.well-known/matrix/client
# MAS as the issuer, from Synapse.
curl -sS https://matrix.garvey.sh/_matrix/client/v1/auth_metadata | head -c 200
# Compatibility login, through Caddy to MAS: m.login.sso only.
curl -sS https://matrix.garvey.sh/_matrix/client/v3/login
# No federation API: 404.
curl -sS -o /dev/null -w '%{http_code}\n' https://matrix.garvey.sh/_matrix/federation/v1/version
curl -sS -o /dev/null -w '%{http_code}\n' https://matrix.garvey.sh/_matrix/key/v2/server
```

## Operating it

- **Certificate renewal:** Caddy (in Synapse's pod) and MAS read their
  certificates only at start. After cert-manager renews them (about every 60
  days), restart: `kubectl -n matrix rollout restart deploy/synapse deploy/mas`.
  The postgres client certificates renew daily and need nothing: libpq and
  MAS read them for each new connection.
- **MAS admin:** `mas-cli` in MAS's pod, which finds its config through
  `MAS_CONFIG`, e.g.
  `kubectl -n matrix exec deploy/mas -- mas-cli manage --help` (lock or
  unlock users, end sessions, set admin).
- **Synapse admin API:** `/_synapse/admin` needs a token MAS gives only to
  users in `policy.data.admin_users` (`mas-configmap.yaml`). For a token to
  use with curl:
  `kubectl -n matrix exec deploy/mas -- mas-cli manage issue-compatibility-token ngarvey --yes-i-want-to-grant-synapse-admin-privileges`.
  It is a full admin login; end it afterwards from the account page at
  `https://matrix-auth.home.garvey.sh/account/`.
- **Adding a person:** add them to Authelia (its `users_database.yml`) and to
  the `matrix` authorization policy in `manifests/authelia/configmap.yaml`.
  Their first login makes the account.
- **Backups:** both databases are in the shared postgres's backups
  (`manifests/postgres/README.md`, "Backups"). Synapse's signing key is in
  `secret.yaml` and MAS's keys in `mas-secret.yaml`; MAS's `encryption`
  secret must never change, or what it encrypted in its database is lost.
  Media is on the ceph-rbd volume.
- **Logs:** `kubectl -n matrix logs deploy/synapse -c synapse`,
  `-c caddy`, and `kubectl -n matrix logs deploy/mas`.
