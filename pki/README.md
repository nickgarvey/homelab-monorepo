# Private root CA

The root of the homelab's private CA, used for service-to-service identities.
Certificates that browsers and outside devices verify come from Let's Encrypt
instead (`docs/tls.md`). The root signs intermediate CAs and nothing else, a few
times a decade.

| File | What it is |
|---|---|
| `root.crt` | The root certificate (ECDSA P-256, `CN=Garvey Homelab Root CA 2026`, pathlen 2, valid 20 years). Public. |
| `root-key.yaml` | The root's private key, sops-encrypted to `*ngarvey` and `*paper` only (`.sops.yaml`). |
| `generate-root.sh` | Created the two files above. Refuses to run if either exists. |
| `sign-intermediate.sh` | Signs an intermediate CA's CSR with the root. |

The root is not installed in any host's trust store. Each service that verifies
private certificates names `root.crt` explicitly, usually as its client CA.

## Recipients

`*paper` is an age key that exists only on paper, kept in two places. It is what
recovers the root if `*ngarvey` is lost. No machine is ever a recipient of
`root-key.yaml`: the key is decrypted only on talos, into a pipe.

To check the paper key still decrypts (type the key; nothing is echoed):

```sh
read -rs SOPS_AGE_KEY && export SOPS_AGE_KEY
nix shell nixpkgs#openssl nixpkgs#sops -c sh -c \
  "sops decrypt --extract '[\"root_key\"]' pki/root-key.yaml | openssl pkey -noout && echo paper key OK"
unset SOPS_AGE_KEY
```

## Creating the root

Once, on talos, from the repo root:

```sh
nix shell nixpkgs#openssl nixpkgs#sops -c bash pki/generate-root.sh
```

The plaintext key lives only in a tmpfs directory that is removed on exit. Before
writing anything, the script checks that the encrypted key decrypts to the key the
certificate holds.

## Signing an intermediate

The intermediate's key stays where it was generated (OpenBao); only its CSR comes
to talos. Write its extensions to a file and sign:

```sh
nix shell nixpkgs#openssl nixpkgs#sops -c bash pki/sign-intermediate.sh <csr> <extfile> <out.crt> [days]
```

Extensions in the CSR are ignored; the extfile decides everything. Every
intermediate needs a pathlen and name constraints. Constraints restrict only the
name types they list, so list every type the intermediate must not issue. For
example:

```
basicConstraints = critical, CA:TRUE, pathlen:0
keyUsage = critical, keyCertSign, cRLSign
subjectKeyIdentifier = hash
authorityKeyIdentifier = keyid:always
nameConstraints = critical, permitted;DNS:home.garvey.sh, excluded;DNS:k8s.home.garvey.sh, excluded;IP:0.0.0.0/0.0.0.0, excluded;IP:::/::, excluded;email:.
```

Intermediates signed so far, both in OpenBao (`docs/openbao.md`): `cluster-ca.crt`
(`cluster-ca.ext`, the cluster CA) and `dns-ca.crt` (`dns-ca.ext`, the hosts CA).

A cross-signature (a new certificate for an existing CA's key) must repeat that CA's
subject byte for byte, string types included: Go links a certificate to its issuer by
comparing raw name bytes, while OpenSSL compares normalized names, so `openssl verify`
cannot catch a mismatch. Set `SUBJECT` to sign with an explicit subject (OpenSSL writes
UTF8String), and compare with `openssl x509 -noout -subject -nameopt RFC2253,show_type`.

OpenSSL writes IP constraints as `address/mask`, with the mask in address form
(`::/::` for all of IPv6). Libraries differ in how they enforce constraints: test a
new intermediate against each verifier that will use it before relying on it.
