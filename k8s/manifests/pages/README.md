# pages

Static pages at `https://pages.home.garvey.sh/<path>`, served from the RGW
bucket `pages` (`rgw.home.garvey.sh`) behind an Authelia login. Only
`ngarvey` may see them (the `pages.home.garvey.sh` rule in
`manifests/authelia/configmap.yaml`). The name is under `home.garvey.sh` so
Authelia's session cookie covers it.

- **Serving:** one Caddy (`packages/pages-server`, Caddy with the
  `caddy-fs-s3` plugin) terminates TLS on 443, asks Authelia about every
  request (`forward_auth` to its ext-authz endpoint), then serves the object. A directory serves its
  `index.html`; nothing is ever listed, and a missing object is a 404.
  Content-Type comes from the file extension. `caddy-configmap.yaml` has the
  config.
- **Credentials:** none stored. The spiffe-helper sidecar keeps the pod's
  JWT-SVID in a file, and Caddy's S3 client trades it with RGW's STS for
  temporary keys of the read-only role `pages` (`modules/services/rgw-roles.nix`,
  deployed on joor).
- **The bucket is not public.** An anonymous request to RGW for it gets 403,
  so the only way to read a page is through this Caddy and Authelia.
- **Reachable from the internet** over IPv4: WAN :443 goes through the HAProxy
  SNI router (`manifests/ipv4-gateway`), which lets this name and the Authelia
  portal through from any source. On the LAN, the name resolves to the IPv4
  gateway and the LoadBalancer AAAA (`garveyShOverrides` in
  `modules/networking/dns.nix`).

## Uploading

Uploads use the RGW user `pages-upload` in the `k8s` account
(`RGW77873637642885287`). Its only permission is an inline IAM policy,
`pages-bucket`, allowing `s3:*` on `pages` and `pages/*`. Its key is in
`~/.aws/credentials` on talos as profile `pages`, and nowhere else.

```sh
export AWS_PROFILE=pages AWS_DEFAULT_REGION=default AWS_REQUEST_CHECKSUM_CALCULATION=when_required
aws --endpoint-url https://rgw.home.garvey.sh s3 sync ./site/ s3://pages/site/   # -> /site/
aws --endpoint-url https://rgw.home.garvey.sh s3 cp report.html s3://pages/report.html
aws --endpoint-url https://rgw.home.garvey.sh s3 rm s3://pages/report.html
```

A changed object is served on the next request; nothing is cached.

A new key, if the old one is lost (on joor, joor/zah/frul all work):

```sh
sudo radosgw-admin key create --uid=pages-upload --key-type=s3 --gen-access-key --gen-secret
sudo radosgw-admin key rm --uid=pages-upload --key-type=s3 --access-key=<old access key>
```

The user and its policy were made the way `docs/openbao.md` describes for
`openbao-snapshot` ("Snapshots and restore"): `radosgw-admin` cannot set inline
user policies, so `aws iam put-user-policy` ran as a temporary account-root
user, removed afterwards.

## Apply (from `k8s/`)

The image is `oci.garvey.sh/pages-server`:

```sh
nix build ..#pages-server-image
skopeo copy docker-archive:result docker://oci.garvey.sh/pages-server:$(nix eval --raw ..#pages-server-image.imageTag)
kubectl apply -f manifests/pages/namespace.yaml
kubectl apply -f manifests/pages/
```

## Checking it

```sh
curl -sI https://pages.home.garvey.sh/site/ | head -3   # 302 to auth.home.garvey.sh without a session
curl -s -o /dev/null -w '%{http_code}\n' https://rgw.home.garvey.sh/pages/site/index.html   # 403
```
