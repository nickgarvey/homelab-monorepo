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
- **TLS on 443 by Synapse itself,** with a Let's Encrypt certificate through
  acme-dns (`certificate.yaml`), so clients trust it with no setup.
- **State:** everything but media is in the `synapse` database on the shared
  postgres (`manifests/postgres`), which Synapse logs in to with a SPIFFE
  client certificate (`postgres-client-certificate.yaml`). Media is on the
  `synapse-media` volume (`pvc.yaml`, ceph-rbd).
- **Logins** go through the Matrix Authentication Service with Authelia
  (nix-X35bb.2). Until then nobody can log in; registration is off.
- **Push notifications** (once clients are signed in): Synapse calls the
  client's push gateway (Element's) outbound, so they work without anything
  reaching in.

The image is nixpkgs' Synapse with Postgres support only
(`packages/matrix-synapse`, `nix build .#matrix-synapse-image`), tagged
`<version>-<store hash>`.

## Setup (from `k8s/`, once)

1. Postgres role and database. Synapse logs in with a client certificate, so
   the role has no password:
   ```sh
   kubectl apply -f manifests/cert-manager/spiffe-policies.yaml
   kubectl apply -f manifests/postgres/cluster.yaml -f manifests/postgres/databases.yaml
   kubectl -n postgres get database synapse      # APPLIED=true
   kubectl -n postgres exec postgres-1 -c postgres -- psql -U postgres -c \
     'REVOKE CONNECT ON DATABASE synapse FROM PUBLIC; GRANT CONNECT ON DATABASE synapse TO synapse;'
   kubectl -n postgres exec postgres-1 -c postgres -- psql -U postgres -tAc \
     "select datcollate, datctype from pg_database where datname = 'synapse'"   # C|C
   ```
2. Certificate (`manifests/acme-dns/ONBOARDING.md`, steps 1–3 with 2a: the
   name is in the Cloudflare zone). Register an acme-dns account for
   `matrix.garvey.sh`, add the `_acme-challenge.matrix.garvey.sh` CNAME in
   Cloudflare, and merge the account into `manifests/acme-dns/secret.yaml`.
   `certificate.yaml` starts on the staging issuer; switch it to
   `letsencrypt-acmedns-prod` once staging is Ready.
   ```sh
   kubectl apply -f manifests/matrix/namespace.yaml -f manifests/matrix/certificate.yaml
   ```
3. Image, from a workstation:
   ```sh
   nix build ..#matrix-synapse-image
   skopeo copy docker-archive:result docker://oci.garvey.sh/matrix-synapse:$(nix eval --raw ..#matrix-synapse-image.imageTag)
   ```
4. The rest:
   ```sh
   kubectl apply -f manifests/matrix/
   kubectl -n matrix rollout status deploy/synapse
   ```
5. DNS: deploy the router (dragonsreach) for the `matrix.garvey.sh` override.

## Checking it

From a LAN client:

```sh
dig +short matrix.garvey.sh AAAA                       # 2001:470:482f:2::5010
curl -sS https://matrix.garvey.sh/_matrix/client/versions | head -c 200
curl -sS https://matrix.garvey.sh/.well-known/matrix/client
# No federation API: 404.
curl -sS -o /dev/null -w '%{http_code}\n' https://matrix.garvey.sh/_matrix/federation/v1/version
curl -sS -o /dev/null -w '%{http_code}\n' https://matrix.garvey.sh/_matrix/key/v2/server
```

## Operating it

- **Certificate renewal:** Synapse reads the certificate only at start. After
  cert-manager renews it (about every 60 days), restart:
  `kubectl -n matrix rollout restart deploy/synapse`. The postgres client
  certificate renews daily and needs nothing: libpq reads it for each new
  connection.
- **Admin API:** `/_synapse/admin` is on the same listener and needs an
  admin's access token, which comes through MAS once it is set up.
- **Backups:** the database is in the shared postgres's backups
  (`manifests/postgres/README.md`, "Backups"). The signing key is in
  `secret.yaml`. Media is on the ceph-rbd volume.
- **Logs:** `kubectl -n matrix logs deploy/synapse`.
