#!/usr/bin/env bash
# Smoke test for the MCP gateway (k8s/manifests/mcp): discovery metadata, the
# 401 handshake, and rejection of forged and cross-module tokens. Run from the
# LAN:
#
#   k8s/manifests/mcp/smoke-test.sh                  # the deployed gateway
#   GATEWAY=[::1] PORT=8443 INSECURE=1 smoke-test.sh # a local caddy run
#
# With real Authelia access tokens it also checks pass-through and that a
# token is refused by every module but its own:
#
#   NOTES_TOKEN=<jwt> ANKI_TOKEN=<jwt> PAGES_TOKEN=<jwt> DIABETES_TOKEN=<jwt> k8s/manifests/mcp/smoke-test.sh
#
# Needs curl, jq and openssl.
set -u

GATEWAY="${GATEWAY:-[2001:470:482f:2::5013]}"
PORT="${PORT:-443}"
MODULES=(notes anki pages diabetes)

curl_opts=(-sS --max-time 15
  --resolve "mcp.home.garvey.sh:${PORT}:${GATEWAY}")
[ -n "${INSECURE:-}" ] && curl_opts+=(-k)

mcp="https://mcp.home.garvey.sh:${PORT}"
[ "$PORT" = 443 ] && mcp="https://mcp.home.garvey.sh"

failures=0
pass() { echo "ok   $1"; }
fail() { echo "FAIL $1"; failures=$((failures + 1)); }
check() { if [ "$2" = "$3" ]; then pass "$1"; else fail "$1 (got '$2', want '$3')"; fi; }

# status <url> [curl args...]: the HTTP status code of a request.
status() { local url="$1"; shift; curl "${curl_opts[@]}" -o /dev/null -w '%{http_code}' "$@" "$url"; }

initialize='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke-test","version":"0"}}}'
mcp_post() { # mcp_post <module> [curl args...]
  local m="$1"; shift
  status "$mcp/$m/mcp" -X POST -H 'Content-Type: application/json' \
    -H 'Accept: application/json, text/event-stream' --data "$initialize" "$@"
}

b64url() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }

# A JWT with the right issuer, audience and key ID, signed by a throwaway key.
forged_jwt() {
  local aud="$1" key header payload sig now
  key=$(mktemp); trap 'rm -f "$key"' RETURN
  openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "$key" 2>/dev/null
  now=$(date +%s)
  header=$(printf '{"alg":"RS256","typ":"at+jwt","kid":"main"}' | b64url)
  payload=$(printf '{"iss":"https://auth.home.garvey.sh","aud":["%s"],"sub":"forged","iat":%d,"nbf":%d,"exp":%d}' \
    "$aud" "$now" "$now" "$((now + 600))" | b64url)
  sig=$(printf '%s.%s' "$header" "$payload" | openssl dgst -sha256 -sign "$key" -binary | b64url)
  printf '%s.%s.%s' "$header" "$payload" "$sig"
}

for m in "${MODULES[@]}"; do
  url="https://mcp.home.garvey.sh/$m/mcp"
  prm_path="/.well-known/oauth-protected-resource/$m/mcp"
  prm_url="https://mcp.home.garvey.sh$prm_path"

  prm=$(curl "${curl_opts[@]}" "$mcp$prm_path")
  check "$m: metadata resource" "$(jq -r .resource <<<"$prm" 2>/dev/null)" "$url"
  check "$m: metadata authorization server" \
    "$(jq -r '.authorization_servers[0]' <<<"$prm" 2>/dev/null)" "https://auth.home.garvey.sh"

  check "$m: no token -> 401" "$(mcp_post "$m")" 401
  www=$(curl "${curl_opts[@]}" -o /dev/null -D - -X POST --data "$initialize" "$mcp/$m/mcp" \
    | tr -d '\r' | sed -n 's/^[Ww][Ww][Ww]-[Aa]uthenticate: //p')
  check "$m: WWW-Authenticate" "$www" "Bearer resource_metadata=\"$prm_url\""

  check "$m: garbage token -> 401" "$(mcp_post "$m" -H 'Authorization: Bearer not-a-jwt')" 401
  check "$m: forged token -> 401" \
    "$(mcp_post "$m" -H "Authorization: Bearer $(forged_jwt "$url")")" 401
  check "$m: token in query string -> 401" \
    "$(status "$mcp/$m/mcp?access_token=$(forged_jwt "$url")" -X POST --data "$initialize")" 401
done

check "mcp: unknown path -> 404" "$(status "$mcp/")" 404
check "mcp: unknown module -> 404" "$(status "$mcp/nope/mcp")" 404

# Real tokens, from ticket nix-xcFoC.5's Authelia clients.
for m in "${MODULES[@]}"; do
  var="${m^^}_TOKEN"
  token="${!var:-}"
  [ -z "$token" ] && { echo "skip $m: pass-through ($var unset)"; continue; }
  s=$(mcp_post "$m" -H "Authorization: Bearer $token")
  if [ "$s" != 401 ] && [ "$s" != 403 ]; then pass "$m: valid token passes ($s)"; else fail "$m: valid token (got $s)"; fi
  for other in "${MODULES[@]}"; do
    [ "$other" = "$m" ] && continue
    check "$m token on $other -> 401" "$(mcp_post "$other" -H "Authorization: Bearer $token")" 401
  done
done

echo
if [ "$failures" -eq 0 ]; then echo "all checks passed"; else echo "$failures check(s) failed"; exit 1; fi
