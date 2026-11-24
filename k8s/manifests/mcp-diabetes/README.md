# mcp-diabetes

The diabetes MCP server (`packages/diabetes-mcp`): read-only tools over the
user's Nightscout for claude.ai, at `https://mcp.home.garvey.sh/diabetes/mcp`
behind the gateway in `manifests/mcp` (Authelia client `claude-diabetes`).
Design: ait epic nix-INSvB.

| File | What it holds |
|---|---|
| `deployment.yaml` | The server; config by env (Nightscout URL, home time zone, CGM lifetime) |
| `service.yaml` | ClusterIP :8000, reached only by the gateway |
| `secret-provider-class.yaml` | ServiceAccount `mcp-diabetes` and its OpenBao mount of the Nightscout token |
| `network-policy.yaml` | Ingress from the gateway only; egress to Nightscout and DNS |

The token belongs to the Nightscout subject `mcp-diabetes` (Admin Tools →
Subjects), which has only the `readable` role. It is in
`secrets/openbao/mcp-diabetes/mcp-diabetes.yaml`, pushed with
`scripts/push-secrets.sh`; the server rereads the file on every request.

Tools: `current_status`, `device_lifetimes`, `glucose_history`, `treatments`,
`therapy_settings`. Each takes an optional `timezone` (IANA name). Without one,
local times are in Loop's pump time zone (`pump.secondsFromGMT`), which lags
behind the user after travel until the pump time is synced in Loop.

## Deploy

```sh
nix build .#diabetes-mcp-image
skopeo copy docker-archive:result docker://oci.garvey.sh/diabetes-mcp:<tag>   # tag in deployment.yaml
kubectl apply -f manifests/mcp-diabetes/
```
