#!/usr/bin/env bash
# Mints a push-only celler token for each workstation and seals it into
# secrets/celler-push.yaml, keyed by host name (modules/desktop/celler-push.nix).
# Needs the signing key in secrets/celler-admin.yaml (ngarvey only). Tokens
# expire after a year: run this again, commit, and deploy the workstations.
# Usage: mint-push-tokens.sh [host...]   (default: talos dovahkiin)
set -euo pipefail

cd "$(git -C "$(dirname "$0")" rev-parse --show-toplevel)"
hosts=("$@")
[ ${#hosts[@]} -gt 0 ] || hosts=(talos dovahkiin)

celler=$(nix build --no-link --print-out-paths .#celler)/bin/celler
sops() { nix run nixpkgs#sops -- "$@"; }

for host in "${hosts[@]}"; do
  token=$("$celler" admin make-token \
    --signing-key <(sops -d --extract '["token-signing-key"]' secrets/celler-admin.yaml) \
    --sub "$host" --validity 1y --pull homelab --push homelab)
  sops set secrets/celler-push.yaml "[\"$host\"]" "\"$token\""
  unset token
  echo "$host: token sealed"
done

git diff --stat -- secrets/celler-push.yaml
