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

- `cluster-issuer-acmedns-staging.yaml` / `-prod.yaml` — acme-dns DNS-01 issuers. These are
  the only issuers; every Certificate uses `letsencrypt-acmedns-prod`.
- The acme-dns account secret lives at `manifests/acme-dns/secret.yaml` (also namespaced to
  `cert-manager`). Onboarding a new name: `manifests/acme-dns/ONBOARDING.md`.

## SPIFFE trust domain

`spiffe-ca-issuer.yaml` is the ClusterIssuer `spiffe-ca`, the one CA for the trust domain
`spiffe://home.garvey.sh`. Its key is in `secret.yaml` (SOPS); the certificate carries a
name constraint, so it can only certify URIs under `home.garvey.sh`, and path length 1, so
it can sign exactly one level of CA below it: SPIRE's (`manifests/spire/`). Services trust
this single root and decide which identities they accept (postgres: `manifests/postgres/`).
Copies of the root certificate that are not read from a Secret, to replace with it if the
CA is ever replaced: `manifests/postgres/client-ca.yaml`,
`manifests/litellm/postgres-ca.yaml` and the repo root's
`packages/openshell/spiffe-ca.crt` (baked into the OpenShell supervisor image).

Identities come from one of two registries, both chaining to this root:

- **Here, approver-policy**, when the consumer needs a Kubernetes Secret (a `Certificate`
  in the workload's namespace), e.g. CloudNativePG's server and replication certificates.
- **SPIRE**, when the workload fetches its identity through the Workload API, and for
  every JWT-SVID. Every pod outside the system namespaces already has one; see
  `manifests/spire/README.md`.

Requests to it are approved by approver-policy, not cert-manager: `values.yaml` lists the
signers cert-manager still approves itself (every other issuer), and
`spiffe-policies.yaml` holds one `CertificateRequestPolicy` per identity, plus one for
SPIRE's intermediate CA. A request that no policy matches stays unapproved. Add an
identity by adding a policy (and its name to the ClusterRole there).

Apply order: `vendor/cert-manager/cert-manager.yaml`, then
`kubectl apply -f vendor/cert-manager-approver-policy/cert-manager-approver-policy.yaml`,
then `kubectl apply -f manifests/cert-manager/` (the CA secret needs the
sops-secrets-operator).
