# celler

Nix binary cache at `https://celler.home.garvey.sh`, run by
[celler](https://github.com/celler-cache/celler) (an Attic fork). NARs and
chunks are stored in the `celler` bucket on Ceph RGW (`rgw.home.garvey.sh`).
Metadata is kept in the `celler` database on the shared Postgres
(`manifests/postgres`). A Caddy sidecar terminates TLS.

There is one cache, `homelab`. It is public, so any host can pull from it
without credentials. Pushing needs a token. The server signs every path with
the cache's own key, and hosts trust only that key.

Tokens are JWTs signed offline with the RS256 private key in
`secrets/celler-admin.yaml` (ngarvey only). The server holds only the public
key, in `configmap.yaml`.

## Setup (from `k8s/`, once)

`celler` below means `nix run ..#celler --`.

1. RGW role. cellerd has no S3 key: the spiffe-helper sidecar keeps its
   JWT-SVID in a file, and cellerd trades that for temporary credentials of
   the `celler` role with RGW's STS (`AWS_*` in `deployment.yaml`). The role is
   the `celler` entry in `modules/services/rgw-roles.nix`; deploy the host
   that imports it (joor) and check that it exists:
   ```sh
   ssh joor sudo radosgw-admin role get --account-id=RGW77873637642885287 --role-name=celler
   ```
2. Postgres role and database (`manifests/postgres/README.md`, "Adding an
   app"). cellerd logs in with a SPIFFE client certificate
   (`postgres-client-certificate.yaml`), so the role has no password:
   ```sh
   kubectl apply -f manifests/cert-manager/cluster-policies.yaml
   kubectl apply -f manifests/postgres/cluster.yaml -f manifests/postgres/databases.yaml
   kubectl -n postgres get database celler      # APPLIED=true
   kubectl -n postgres exec postgres-1 -c postgres -- psql -U postgres -c \
     'REVOKE CONNECT ON DATABASE celler FROM PUBLIC; GRANT CONNECT ON DATABASE celler TO celler;'
   ```
3. Certificate: `celler` is in `certManagerAcme` (`modules/networking/dns.nix`;
   see `manifests/cert-manager/ONBOARDING.md`), and `certificate.yaml` uses
   `letsencrypt-rfc2136-prod`.
   ```sh
   kubectl apply -f manifests/celler/namespace.yaml -f manifests/celler/certificate.yaml
   ```
4. Image, from a workstation:
   ```sh
   nix build ..#celler-image
   skopeo copy docker-archive:result docker://oci.garvey.sh/celler:$(nix eval --raw ..#celler-image.imageTag)
   ```
5. Bucket. cellerd does not create it, and only the `celler` role may, so a
   one-off pod with cellerd's identity does it:
   ```sh
   kubectl apply -f manifests/celler/service-account.yaml -f manifests/celler/spiffe-helper-configmap.yaml
   kubectl apply -f - <<'EOF'
   apiVersion: v1
   kind: Pod
   metadata: { name: celler-create-bucket, namespace: celler }
   spec:
     serviceAccountName: celler
     restartPolicy: Never
     initContainers:
       - name: spiffe-helper
         image: ghcr.io/spiffe/spiffe-helper:0.11.0
         restartPolicy: Always
         args: ["-config", "/etc/spiffe-helper/helper.conf"]
         volumeMounts:
           - { name: spiffe-workload-api, mountPath: /spiffe-workload-api, readOnly: true }
           - { name: spiffe-helper-config, mountPath: /etc/spiffe-helper, readOnly: true }
           - { name: rgw-token, mountPath: /run/rgw-token }
         startupProbe: { httpGet: { path: /ready, port: 8081 }, periodSeconds: 2, failureThreshold: 60 }
     containers:
       - name: aws
         image: amazon/aws-cli:2.27.50
         args: ["--endpoint-url", "https://rgw.home.garvey.sh", "--region", "default",
                "s3api", "create-bucket", "--bucket", "celler"]
         env:
           - { name: AWS_WEB_IDENTITY_TOKEN_FILE, value: /run/rgw-token/jwt_svid.token }
           - { name: AWS_ROLE_ARN, value: "arn:aws:iam::RGW77873637642885287:role/nix-managed/celler" }
           - { name: AWS_ENDPOINT_URL_STS, value: https://rgw.home.garvey.sh }
         volumeMounts:
           - { name: rgw-token, mountPath: /run/rgw-token, readOnly: true }
     volumes:
       - { name: spiffe-workload-api, csi: { driver: csi.spiffe.io, readOnly: true } }
       - { name: spiffe-helper-config, configMap: { name: celler-spiffe-helper } }
       - { name: rgw-token, emptyDir: { medium: Memory } }
   EOF
   kubectl -n celler wait --for=jsonpath='{.status.phase}'=Succeeded pod/celler-create-bucket
   # RGW answers with an empty body, so check the bucket itself:
   ssh joor sudo radosgw-admin bucket stats --bucket celler | grep owner   # RGW77873637642885287
   kubectl -n celler delete pod celler-create-bucket
   ```
6. Server:
   ```sh
   kubectl apply -f manifests/celler/postgres-client-certificate.yaml
   kubectl -n celler wait --for=condition=Ready certificate/celler-postgres-client
   kubectl apply -f manifests/celler/
   kubectl -n celler rollout status deploy/celler
   kubectl -n celler logs deploy/celler -c cellerd
   ```
7. Admin token and the cache. The admin token lives only in your
   `~/.config/celler/config.toml`:
   ```sh
   TOKEN=$(celler admin make-token \
     --signing-key <(sops -d --extract '["token-signing-key"]' ../secrets/celler-admin.yaml) \
     --sub ngarvey-admin --validity 1y \
     --pull '*' --push '*' --delete '*' --create-cache '*' --configure-cache '*' \
     --configure-cache-retention '*' --destroy-cache '*')
   celler login homelab https://celler.home.garvey.sh/ "$TOKEN"
   celler cache create homelab --public \
     --upstream-cache-key-name cache.nixos.org-1 \
     --upstream-cache-key-name cache.nixos-cuda.org
   celler cache info homelab          # the Public Key line is what hosts trust
   ```
   Paths signed by an upstream key are not uploaded, since every host can
   already fetch them from that upstream.

## Push tokens

Each workstation (talos, dovahkiin) pushes everything it builds with
`celler watch-store` (`modules/desktop/celler-push.nix`), using its own
push-only token from `secrets/celler-push.yaml`. The tokens expire after a
year. To mint them, or renew them, run this, commit the secret and deploy the
workstations:
```sh
manifests/celler/mint-push-tokens.sh            # or: ... <host>
```
Tokens cannot be revoked one by one. To cut off every token at once, rotate
the key pair (`secrets/celler-admin.yaml` and `jwt-public.pem` in
`configmap.yaml`).

## Operations

- Caddy loads the certificate only at startup. After each cert-manager renewal,
  run `kubectl -n celler rollout restart deploy/celler`.
- Garbage collection runs every 12 hours inside cellerd. It deletes paths
  nobody has pulled for 90 days (`default-retention-period` in
  `configmap.yaml`).
