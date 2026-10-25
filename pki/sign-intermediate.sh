# Signs an intermediate CA's CSR with the root, on talos, from the repo root (pki/README.md).
# nix shell nixpkgs#openssl nixpkgs#sops -c bash pki/sign-intermediate.sh <csr> <extfile> <out.crt> [days]
#
# <extfile> holds the intermediate's extensions (basicConstraints with pathlen, keyUsage,
# nameConstraints); extensions requested in the CSR are ignored. The root key is
# decrypted into a pipe and never written to disk.
set -euo pipefail
[ $# -ge 3 ] || { sed -n 2p "$0" >&2; exit 1; }
csr=$1 ext=$2 out=$3 days=${4:-3650}
[ -f .sops.yaml ] && [ -f pki/root.crt ] || { echo "run from the repo root" >&2; exit 1; }
[ ! -e "$out" ] || { echo "$out exists" >&2; exit 1; }

openssl req -in "$csr" -noout -verify
openssl x509 -req -in "$csr" -copy_extensions none -extfile "$ext" \
  -CA pki/root.crt -CAkey <(sops decrypt --extract '["root_key"]' pki/root-key.yaml) \
  -set_serial "0x$(openssl rand -hex 16)" -sha256 -days "$days" -out "$out"

openssl verify -CAfile pki/root.crt "$out"
openssl x509 -in "$out" -noout -subject -issuer -dates \
  -ext basicConstraints,keyUsage,nameConstraints
