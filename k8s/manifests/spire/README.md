# spire

SPIRE issues workload identities in the SPIFFE trust domain
`spiffe://home.garvey.sh`: X.509-SVIDs and JWT-SVIDs, delivered to pods through
the Workload API (the agent socket, mounted by the `csi.spiffe.io` CSI driver).
It is rendered from the `spire` chart into `vendor/spire/spire.yaml`, with its
CRDs in `vendor/spire-crds/`; the values and the reason for each are in
`vendor/spire/values.yaml`.

## One CA, two registries

SPIRE's CA is not a second root. Its server requests an intermediate CA from
the `spiffe-ca` ClusterIssuer (UpstreamAuthority `cert_manager`), so every SVID
chains to the same root as the certificates cert-manager issues, and the root's
name constraint (URIs under `home.garvey.sh`) still applies. The request is
approved only by the `spiffe-spire-ca` policy in
`manifests/cert-manager/spiffe-policies.yaml`, which only the `spire-server`
ServiceAccount may use.

Identities are granted in one of two places (see `manifests/cert-manager/README.md`):

- **SPIRE** (this directory): every pod outside `kube-system`, `kube-public`,
  `local-path-storage` and `spire` gets
  `spiffe://home.garvey.sh/ns/<namespace>/sa/<service account>` (the chart's
  default ClusterSPIFFEID). The excluded namespaces are the controller-manager's
  `ignoreNamespaces` and that ClusterSPIFFEID's `namespaceSelector`, both in
  `vendor/spire/spire.yaml`. The agent attests the pod's real ServiceAccount
  through the kubelet. Pods that share a ServiceAccount (often `default`) share
  the identity, so a workload that something grants access to needs its own.
- **cert-manager + approver-policy**: identities whose consumer reads a
  Kubernetes Secret (CloudNativePG's server and replication certificates,
  Temporal's postgres client certificate). SPIRE does not write Secrets.

An identity grants nothing by itself; what each one may do is decided by the
service that trusts it.

A service that trusts the root accepts SVIDs as well as the certificates
approver-policy gates. An SVID's subject carries `O=SPIRE,C=US` and, as its CN,
the entry's first DNS name, so a service that maps a certificate to an account
by CN alone would let a SPIRE entry with that DNS name in. Postgres matches the
whole subject DN instead (`manifests/postgres/cluster.yaml`), which an SVID
never equals. Before giving a ClusterSPIFFEID or ClusterStaticEntry DNS names,
check every service that trusts `spiffe-ca` for a CN-only match.

## Layout

Everything is in the namespace `spire`. The server (one replica, with the
controller-manager beside it) runs on the control-plane nodes fus/ro/dah and
keeps its sqlite datastore on a Longhorn volume, so it keeps working with the
Ceph nodes down. The agent and CSI driver are DaemonSets on every node. While
the server is unavailable, agents keep serving the SVIDs they hold until they
expire (the chart's TTLs: X.509 4h, JWT 1h).

SPIRE's CA is RSA-2048 and also signs JWT-SVIDs; Ceph RGW validates JWKS keys
without an x5c chain only for RSA, so keep it RSA.

## OIDC discovery: `https://spiffe-oidc.home.garvey.sh`

The `iss` of every JWT-SVID. The OIDC discovery provider (a Deployment on
fus/ro/dah) serves `/.well-known/openid-configuration` and the JWKS there, so
services outside the cluster can validate JWT-SVIDs; Ceph RGW's STS is the first.
It terminates TLS itself on the LoadBalancer `2001:470:482f:2::5007:443`, with a
Let's Encrypt certificate (`certificate.yaml`, acme-dns account
`spiffe-oidc.home.garvey.sh`; the AAAA and the `_acme-challenge` CNAME are in
`modules/networking/dns.nix`). It re-reads the certificate files every minute, so
renewals need no restart.

RGW trusts this host through ordinary certificate validation: it fetches the
discovery document and JWKS over HTTPS with the system CAs (`rgw_verify_ssl`,
on by default). The provider is registered in RGW with a thumbprint, the SHA-1
of the last certificate of the served chain (today the `ISRG Root YR`
cross-sign, `ab9d0263244dd0326eb67015705a667e79cfe998`), but RGW does not check
it: thumbprint pinning only runs with `rgw_enable_jwks_url_verification`, which
stays off (`modules/services/ceph.nix`; its connection is IPv4-only), and keys
without an x5c chain, which is all SPIRE publishes, never use thumbprints. A
change in the chain Let's Encrypt hands out therefore changes nothing.

## S3 on Ceph RGW

An app reaches its RGW buckets with its JWT-SVID instead of a static key: RGW's
STS (AssumeRoleWithWebIdentity) trades the token for 1h credentials of the
app's role. The roles live in the RGW account `k8s` (`RGW77873637642885287`),
which trusts this OIDC provider with client ID (audience) `rgw.home.garvey.sh`.

1. Give the app its own ServiceAccount (pods sharing one share the role).
2. Declare it in `modules/services/rgw-roles.nix` (namespace, ServiceAccount,
   bucket prefix) and deploy joor. Its role,
   `arn:aws:iam::RGW77873637642885287:role/nix-managed/<name>`, may use only
   buckets starting with the prefix, and creates them itself.
3. In the pod: keep a JWT-SVID with audience `rgw.home.garvey.sh` in a file
   (spiffe-helper as a native sidecar), and set `AWS_WEB_IDENTITY_TOKEN_FILE`,
   `AWS_ROLE_ARN`, `AWS_ROLE_SESSION_NAME` and
   `AWS_ENDPOINT_URL_STS=https://rgw.home.garvey.sh`, with the S3 endpoint
   `https://rgw.home.garvey.sh`. No `AWS_ACCESS_KEY_ID` may be set: static keys
   win over web identity in the AWS SDKs.

When testing a denial with the AWS CLI, use `aws s3api`: `aws s3` reports some
of RGW's AccessDenied responses as "argument of type 'NoneType' is not iterable".

## Apply (from `k8s/`)

cert-manager, approver-policy and `manifests/cert-manager/` must already be in
place (the `spiffe-spire-ca` policy in particular), and Longhorn. The OIDC
provider cannot start until its certificate Secret exists, which needs the
acme-dns account (`manifests/acme-dns/secret.yaml`) and the router deployed with
the CNAME.

```sh
kubectl apply -f manifests/cert-manager/spiffe-policies.yaml
kubectl apply -f vendor/spire-crds/spire-crds.yaml
kubectl apply -f manifests/spire/certificate.yaml   # after the namespace exists
kubectl -n spire wait --for=condition=Ready certificate/spiffe-oidc-tls --timeout=5m
kubectl apply -f vendor/spire/spire.yaml
kubectl -n spire rollout status statefulset/spire-server
kubectl -n spire rollout status daemonset/spire-agent
kubectl -n spire rollout status deployment/spire-spiffe-oidc-discovery-provider
```

On a bare cluster, `vendor/spire/spire.yaml` creates the namespace: apply it
first, then the certificate; the provider pod waits for its Secret.

The chart's Helm hooks are not rendered. The ones it has only run tests and
relax the controller-manager's webhook during a Helm install; none is needed
here.

## Checks

```sh
kubectl -n spire exec spire-server-0 -c spire-server -- spire-server healthcheck
kubectl -n spire exec spire-server-0 -c spire-server -- spire-server agent list
kubectl -n spire get certificaterequests   # SPIRE's CA request: Approved, Ready
curl -s https://spiffe-oidc.home.garvey.sh/.well-known/openid-configuration
curl -s https://spiffe-oidc.home.garvey.sh/keys      # kty RSA, n/e, no x5c
```
