# Run once, on talos, from the repo root: creates the private root CA (pki/README.md).
# nix shell nixpkgs#openssl nixpkgs#sops -c bash pki/generate-root.sh
#
# Writes pki/root.crt and pki/root-key.yaml (sops: *ngarvey + *paper). The plaintext
# key exists only in a tmpfs directory that is removed on exit.
set -euo pipefail
[ -f .sops.yaml ] && [ -d pki ] || { echo "run from the repo root" >&2; exit 1; }
for f in pki/root.crt pki/root-key.yaml; do
  [ ! -e "$f" ] || { echo "$f exists; refusing to replace the root" >&2; exit 1; }
done

umask 077
d=$(mktemp -d -p "${XDG_RUNTIME_DIR:-/dev/shm}")
trap 'rm -rf "$d"' EXIT

cat > "$d/root.cnf" <<'EOF'
[req]
prompt = no
distinguished_name = dn
x509_extensions = root
[dn]
CN = Garvey Homelab Root CA 2026
[root]
# pathlen 2: root -> pki-cluster -> SPIRE. Name constraints go on the intermediates.
basicConstraints = critical, CA:TRUE, pathlen:2
keyUsage = critical, keyCertSign, cRLSign
subjectKeyIdentifier = hash
EOF

openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "$d/root.key"
openssl req -x509 -new -config "$d/root.cnf" -key "$d/root.key" -sha256 -days 7305 \
  -out "$d/root.crt"

{ echo "root_key: |"; sed 's/^/    /' "$d/root.key"; } > "$d/root-key.yaml"
sops encrypt --filename-override pki/root-key.yaml "$d/root-key.yaml" > "$d/root-key.enc.yaml"

# The encrypted copy must decrypt (with *ngarvey) to the key the certificate holds.
cmp <(sops decrypt --filename-override pki/root-key.yaml --extract '["root_key"]' "$d/root-key.enc.yaml" \
        | openssl pkey -pubout) \
    <(openssl x509 -in "$d/root.crt" -noout -pubkey) \
  || { echo "decrypted key does not match the certificate; nothing written" >&2; exit 1; }

umask 022
cp "$d/root.crt" pki/root.crt
cp "$d/root-key.enc.yaml" pki/root-key.yaml
openssl x509 -in pki/root.crt -noout -subject -dates -fingerprint -sha256 -ext basicConstraints,keyUsage
echo "OK: pki/root.crt and pki/root-key.yaml written"
