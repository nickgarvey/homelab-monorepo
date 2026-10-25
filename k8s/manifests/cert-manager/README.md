# cert-manager

cert-manager is rendered from its Helm chart into `vendor/cert-manager/cert-manager.yaml`;
`vendor/cert-manager/values.yaml` holds the two DNS-01 self-check flags and why the
IPv6-only cluster needs them. See `vendor/README.md` for rendering and upgrades.

## Install / upgrade order

```sh
kubectl apply -f vendor/cert-manager/cert-manager.yaml
kubectl apply -f manifests/cert-manager/
```

## Issuers & secrets (this dir)

- `cluster-issuer-rfc2136-staging.yaml` / `-prod.yaml` — Let's Encrypt issuers that write
  DNS-01 challenges straight into Knot (RFC2136) with the `acme-cert-manager` TSIG key in
  `secret.yaml`. Every Let's Encrypt Certificate uses `letsencrypt-rfc2136-prod`. Knot lets
  the key write TXT only at the names in `certManagerAcme` (`modules/networking/dns.nix`);
  the `cert-manager-acme-names` flake check keeps that list equal to the Certificates.
- Adding a name: `ONBOARDING.md`.

## SPIFFE trust domain

`cluster-ca-issuer.yaml` is the ClusterIssuer `cluster-ca`, the one CA for the trust domain
`spiffe://home.garvey.sh`. Its key is in OpenBao's `pki-cluster` mount, outside the
cluster (`docs/openbao.md`, "Cluster CA"); the issuer logs in with the `openbao-issuer`
ServiceAccount (`openbao-issuer.yaml`) and signs through `sign-verbatim`, which keeps each
request's subject and SANs. Its certificate (`pki/cluster-ca.crt`) is signed by the root, `pki/root.crt`, with name
constraints (SPIFFE IDs in `home.garvey.sh`, `*.svc`, `*.cluster.local`,
`*.k8s.home.garvey.sh`) and path length 1, so it can sign exactly one level of CA below it:
SPIRE's (`manifests/spire/`), which SPIRE requests from OpenBao directly. Each Secret's
`ca.crt` is the root and `tls.crt` carries the cluster CA's certificate after the leaf;
services trust the root and decide which identities they accept (postgres:
`manifests/postgres/`). Copies of the root not read from a Secret are
`manifests/postgres/client-ca.yaml` and `manifests/litellm/postgres-ca.yaml`; the
`pki-root-copies` flake check keeps them equal to `pki/root.crt`.

Identities come from one of two registries, both chaining to this root:

- **Here, approver-policy**, when the consumer needs a Kubernetes Secret (a `Certificate`
  in the workload's namespace), e.g. CloudNativePG's server and replication certificates.
- **SPIRE**, when the workload fetches its identity through the Workload API, and for
  every JWT-SVID. Every pod outside the system namespaces already has one; see
  `manifests/spire/README.md`.

Requests to it are approved by approver-policy, not cert-manager: `values.yaml` lists the
signers cert-manager still approves itself (every other issuer), and
`cluster-policies.yaml` holds one `CertificateRequestPolicy` per identity. A request that no policy matches stays unapproved. Add an
identity by adding a policy (and its name to the ClusterRole there).

Apply order: `vendor/cert-manager/cert-manager.yaml`, then
`kubectl apply -f vendor/cert-manager-approver-policy/cert-manager-approver-policy.yaml`,
then `kubectl apply -f manifests/cert-manager/` (the CA secret needs the
sops-secrets-operator).
