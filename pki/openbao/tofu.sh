# Runs OpenTofu on pki/openbao with its encrypted state and an admin token (docs/openbao.md).
# nix shell nixpkgs#opentofu nixpkgs#sops nixpkgs#openssl nixpkgs#curl nixpkgs#jq -c bash pki/openbao/tofu.sh plan
#
# The state is committed, encrypted with a passphrase from secrets/openbao-admin.yaml.
# VAULT_TOKEN, if set, is used as is (the root token during bootstrap); otherwise
# login.sh logs in with the talos key.
set -euo pipefail
cd "$(dirname "$0")"
pass=$(sops decrypt --extract '["tofu_state_passphrase"]' ../../secrets/openbao-admin.yaml)
TF_ENCRYPTION=$(cat <<EOF
key_provider "pbkdf2" "state" {
  passphrase = "$pass"
}
method "aes_gcm" "state" {
  keys = key_provider.pbkdf2.state
}
state {
  method   = method.aes_gcm.state
  enforced = true
}
plan {
  method   = method.aes_gcm.state
  enforced = true
}
EOF
)
export TF_ENCRYPTION
if [ -z "${VAULT_TOKEN:-}" ]; then
  VAULT_TOKEN=$(bash ./login.sh)
fi
export VAULT_TOKEN
exec tofu "$@"
