# mcp

The MCP gateway: Caddy with the caddy-jwt plugin (`packages/mcp-gateway`), on the
LoadBalancer `2001:470:482f:2::5013`. It is the only way in to the MCP modules
that claude.ai connectors use. Design: ait epic nix-xcFoC.

It serves one name, with the certificate `mcp-gateway-tls`:

- `mcp.home.garvey.sh/<module>/mcp`: each module's MCP endpoint. Caddy serves the
  module's RFC 9728 protected resource metadata at
  `/.well-known/oauth-protected-resource/<module>/mcp`, answers a missing or bad
  token with `401` and `WWW-Authenticate: Bearer resource_metadata="…"`, and
  forwards only requests carrying an Authelia JWT access token whose audience is
  that module's URL. Modules: `notes` (namespace `mcp-notes`), `anki`
  (namespace `mcp-anki`), `pages` (namespace `mcp-pages`) and `diabetes`
  (namespace `mcp-diabetes`).

Anthropic's servers reach Authelia (`auth.home.garvey.sh`) for discovery and
tokens through the HAProxy SNI router (`manifests/ipv4-gateway`), which passes
that name from any source, not through this gateway.

## Adding a module

1. One `import module <name> <service>.<namespace>.svc.cluster.local:<port>`
   line in `caddy-configmap.yaml`; the upstream serves MCP at `/mcp`, and it
   sees Host `mcp.home.garvey.sh`.
2. An Authelia client whose audience is `https://mcp.home.garvey.sh/<name>/mcp`
   (`manifests/authelia/configmap.yaml`).
3. The module's network policy admits only this namespace's `app: mcp-gateway`
   pods. The cluster-wide `pod-ingress-default-deny` admits every pod in the
   cluster, so the module's namespace must be excluded from it, with a
   default-deny policy of its own.

## Reloads

Caddy reads the Caddyfile and the certificate only when it loads a config.
The `reload` container polls `/certs/tls.crt`, `/certs/tls.key` and the
Caddyfile every 60 seconds and, when one changes, forces a reload through
Caddy's admin API on `127.0.0.1:2019` (loopback, so only this pod reaches it).
A renewed certificate or an applied ConfigMap change is live within a few
minutes, once the kubelet has updated the volume; `kubectl -n mcp logs
deploy/mcp-gateway -c reload` shows each reload. To restart by hand, bump the
`mcp.home.garvey.sh/restarted` annotation in `deployment.yaml` and apply.

## Testing

`./smoke-test.sh` from the LAN checks the metadata, the 401 handshake, that
forged and query-string tokens are refused.
With `NOTES_TOKEN` / `ANKI_TOKEN` / `PAGES_TOKEN` / `DIABETES_TOKEN` set to real access tokens it also checks
pass-through and that each token is refused by the other module.
