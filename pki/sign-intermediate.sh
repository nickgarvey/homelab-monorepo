# Signs an intermediate CA's CSR with the root, on talos, from the repo root (pki/README.md).
# nix shell nixpkgs#openssl nixpkgs#sops -c bash pki/sign-intermediate.sh <csr> <extfile> <out.crt> [days]
#
# <extfile> holds the intermediate's extensions (basicConstraints with pathlen, keyUsage,
# nameConstraints); extensions requested in the CSR are ignored. The root key is
# decrypted into a pipe and never written to disk.
#
# SUBJECT=<openssl -subj string> replaces the CSR's subject; the CSR still supplies the
# key. A cross-signature of an existing CA needs it: the new certificate's subject must
# be byte-identical to the existing one's (same string types), because Go matches an
# issuer to its parent's subject by raw bytes. OpenSSL writes -subj as UTF8String.
set -euo pipefail
[ $# -ge 3 ] || { sed -n 2p "$0" >&2; exit 1; }
csr=$1 ext=$2 out=$3 days=${4:-3650}
[ -f .sops.yaml ] && [ -f pki/root.crt ] || { echo "run from the repo root" >&2; exit 1; }
[ ! -e "$out" ] || { echo "$out exists" >&2; exit 1; }

openssl req -in "$csr" -noout -verify
trap '[ -s "$out" ] || rm -f "$out"' EXIT
opts=(-set_serial "0x$(openssl rand -hex 16)" -sha256 -days "$days" -extfile "$ext" -out "$out")
if [ -n "${SUBJECT:-}" ]; then
  openssl x509 -new -force_pubkey <(openssl req -in "$csr" -noout -pubkey) -subj "$SUBJECT" "${opts[@]}" \
    -CA pki/root.crt -CAkey <(sops decrypt --extract '["root_key"]' pki/root-key.yaml)
else
  openssl x509 -req -in "$csr" -copy_extensions none "${opts[@]}" \
    -CA pki/root.crt -CAkey <(sops decrypt --extract '["root_key"]' pki/root-key.yaml)
fi

openssl verify -CAfile pki/root.crt "$out"
openssl x509 -in "$out" -noout -subject -issuer -dates -nameopt RFC2253,show_type \
  -ext basicConstraints,keyUsage,nameConstraints
