# Prints an OpenBao admin token, logging in with a pinned RSA key (docs/openbao.md).
# pki/openbao/login.sh [key.pem]      (default: ~/.ssh/id_rsa, the talos key)
# Offline key: pki/openbao/login.sh <(sops decrypt --extract '["offline_admin_key"]' secrets/openbao-admin.yaml)
# Needs openssl, curl, jq.
set -euo pipefail
key=${1:-$HOME/.ssh/id_rsa}
addr=${BAO_ADDR:-https://openbao.home.garvey.sh:8200}

b64url() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }
now=$(date +%s)
# Valid for a minute: long enough to log in, short enough to be useless later.
header=$(printf '{"alg":"RS256","typ":"JWT"}' | b64url)
claims=$(printf '{"sub":"ngarvey","aud":"openbao","iat":%d,"nbf":%d,"exp":%d}' \
  "$now" "$((now - 30))" "$((now + 60))" | b64url)
sig=$(printf '%s.%s' "$header" "$claims" | openssl dgst -sha256 -sign "$key" | b64url)

jq -n --arg jwt "$header.$claims.$sig" '{role: "admin", jwt: $jwt}' \
  | curl -fsS --data @- "$addr/v1/auth/jwt/login" | jq -r .auth.client_token
