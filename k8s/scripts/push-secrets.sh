#!/usr/bin/env bash
# push-secrets.sh [--dry-run] [<file>...]
# Pushes decrypted sops secrets to where the cluster reads them
# (docs/openbao.md, "Pod secrets"):
#   secrets/openbao/<namespace>/<serviceAccount>.yaml -> OpenBao
#     kv/k8s/<namespace>/<serviceAccount>/<name>,
#     written only when a value differs, so KV versions record real changes;
#   secrets/k8s/<namespace>/<name>.yaml -> kubectl apply (Secrets that controllers read).
# With no files, pushes every file under both directories. --dry-run reports what would change.
# Prints paths and actions only, never values. Also reports KV entries under a
# file's namespace/serviceAccount that no file names (they are not deleted).
# Run from k8s/, on the workstation (needs the user's age key and SSH key).
# nix shell nixpkgs#sops nixpkgs#openssl nixpkgs#curl nixpkgs#jq nixpkgs#kubectl -c bash scripts/push-secrets.sh
set -euo pipefail
cd "$(dirname "$0")/.."

dry=0
[ "${1:-}" = --dry-run ] && { dry=1; shift; }
if [ $# -eq 0 ]; then
  dirs=()
  for d in secrets/openbao secrets/k8s; do [ -d "$d" ] && dirs+=("$d"); done
  mapfile -t files < <(find "${dirs[@]}" -name '*.yaml' | sort)
else
  files=("$@")
fi

addr=${BAO_ADDR:-https://openbao.home.garvey.sh:8200}
token=""
bao() { # bao <method> <path> [curl args...]: OpenBao API call, JSON on stdout
  local method=$1 path=$2; shift 2
  if [ -z "$token" ]; then
    token=${BAO_TOKEN:-$(bash ../pki/openbao/login.sh)}
  fi
  curl -sS -X "$method" -H "X-Vault-Token: $token" "$addr/v1/$path" "$@"
}

push_openbao() {
  local file=$1 doc ns sa name want have path
  doc=$(sops decrypt --output-type json "$file")
  ns=$(jq -er .namespace <<<"$doc")
  sa=$(jq -er .serviceAccount <<<"$doc")
  for name in $(jq -r '.secrets | keys[]' <<<"$doc"); do
    path="kv/data/k8s/$ns/$sa/$name"
    want=$(jq -cS --arg n "$name" '.secrets[$n] | map_values(tostring)' <<<"$doc")
    have=$(bao GET "$path" | jq -cS '.data.data // null')
    if [ "$want" = "$have" ]; then
      echo "unchanged  $path"
    elif [ $dry = 1 ]; then
      echo "would push $path"
    else
      jq -n --argjson d "$want" '{data: $d}' \
        | bao POST "$path" --fail-with-body --data @- > /dev/null
      echo "pushed     $path"
    fi
  done
  for name in $(bao LIST "kv/metadata/k8s/$ns/$sa/" | jq -r '.data.keys[]? // empty'); do
    jq -e --arg n "$name" '.secrets | has($n)' <<<"$doc" > /dev/null \
      || echo "not in $file: kv/data/k8s/$ns/$sa/$name (left in place)"
  done
}

# Server-side apply: client-side apply would copy the plaintext values into the
# kubectl.kubernetes.io/last-applied-configuration annotation.
push_k8s() {
  local file=$1 rc=0
  if [ $dry = 1 ]; then
    sops decrypt "$file" | kubectl diff --server-side --field-manager=push-secrets -f - > /dev/null || rc=$?
    case $rc in
      0) echo "unchanged  $file" ;;
      1) echo "would apply $file" ;;
      *) echo "diff failed $file (exit $rc)" >&2; return 1 ;;
    esac
  else
    sops decrypt "$file" | kubectl apply --server-side --field-manager=push-secrets -f - | sed 's/^/applied    /'
  fi
}

for f in "${files[@]}"; do
  case ${f#./} in
    secrets/openbao/*/*.yaml) push_openbao "$f" ;;
    secrets/k8s/*/*.yaml) push_k8s "$f" ;;
    *) echo "skipped    $f: not under secrets/openbao/ or secrets/k8s/" >&2 ;;
  esac
done
