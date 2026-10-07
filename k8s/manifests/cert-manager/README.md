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
