# mcp-pages

The pages MCP server (`packages/pages-mcp`) at
`https://mcp.home.garvey.sh/pages/mcp`, behind the MCP gateway
(`manifests/mcp`). Its tools work on the RGW bucket `pages` that
`https://pages.home.garvey.sh` serves (`manifests/pages`):

- `list_pages(folder, limit)`: files under a folder, newest first.
- `read_page(path)`: one file's text (truncated at 1 MB).
- `create_page(path, content)`: a new text file (`.html`, `.css`, `.js`,
  `.svg`, `.json`, `.md`, …, up to 5 MB). Paths may contain folders.

Nothing it creates can be changed or deleted through it. Its RGW role
`pages-mcp` (`modules/services/rgw-roles.nix`, access `create`) has no delete
permission, and the server refuses a path that already exists. Deleting or
replacing a page is done by hand with the `pages` upload profile
(`manifests/pages/README.md`).

S3 credentials come from the pod's JWT-SVID, as in `manifests/pages`; no key
is stored.

## Connect

In claude.ai, add a custom connector with the URL
`https://mcp.home.garvey.sh/pages/mcp`, then sign in with Authelia (Authelia
client `duet-pages`).

## Apply (from `k8s/`)

Deploy joor first (the role), then:

```sh
kubectl apply -f manifests/mcp-pages/namespace.yaml
kubectl apply -f manifests/mcp-pages/
kubectl apply -f manifests/cilium/pod-ingress-default-deny.yaml
kubectl apply -f manifests/authelia/configmap.yaml   # then restart Authelia
kubectl apply -f manifests/mcp/caddy-configmap.yaml  # the reload container picks it up
```
