# jellyfin

Jellyfin at `https://jellyfin.garvey.sh`, with a Caddy sidecar terminating TLS
and Intel iGPU transcoding (comments in `deployment.yaml`).

## Single sign-on

People sign in through Authelia with the SSO plugin
([MaxRink/jellyfin-plugin-sso](https://github.com/MaxRink/jellyfin-plugin-sso),
a fork of the archived 9p4 plugin that supports Jellyfin 12). Local password
accounts keep working beside it.

- The `install-sso-plugin` init container installs the pinned release into
  `/config/plugins/`.
- The login page shows a "Sign in with Authelia" button (`branding-configmap.yaml`,
  seeded once).
- Authelia has a `jellyfin` client and authorization policy
  (`manifests/authelia/configmap.yaml`): members of `jellyfin-users` or
  `admins` may sign in, with two factors. The plugin checks the same groups and
  makes `admins` Jellyfin administrators, syncing admin status at each login.
- The first SSO login of an Authelia user takes over the local Jellyfin account
  with the same username (case-insensitive), or creates one.
- The login portal `auth.home.garvey.sh` is reachable only from the LAN, so
  SSO logins work only from home. A device that is signed in stays signed in
  elsewhere.

### Adding a person

Add them to `users_database.yml` in `secrets/openbao/authelia/authelia.yaml`
with the group `jellyfin-users`, push it
(`bash scripts/push-secrets.sh secrets/openbao/authelia/authelia.yaml`) and
restart Authelia.

### Devices without a browser login (TVs, Jellyfin Media Player)

Pick Quick Connect on the device, then on a phone open
`https://jellyfin.garvey.sh/sso/OID/start/authelia?qc=<CODE>`, sign in, and
confirm. The phone apps (Android, iOS) can use the SSO button directly.

### Provider settings

The plugin keeps its settings, including the links between Authelia users and
Jellyfin accounts, in `/config/plugins/configurations/SSO-Auth.xml` on the
config PVC, so they are set once through its API rather than seeded at every
boot. `OID/Add` replaces the whole provider entry, links included; after a
re-run, each person's next login re-links them by username.

With a Jellyfin API key (Dashboard > API Keys) in `$JF_API_KEY`, from `k8s/`:

```bash
sops decrypt --extract '["client-secret"]' secrets/manual/jellyfin/sso.yaml \
  | jq -Rn '{
      oidEndpoint: "https://auth.home.garvey.sh",
      oidClientId: "jellyfin",
      oidSecret: input,
      useClientSecretBasic: true,
      disablePushedAuthorization: true,
      enabled: true,
      enableAuthorization: true,
      enableAllFolders: true,
      enabledFolders: [],
      roles: ["jellyfin-users", "admins"],
      adminRoles: ["admins"],
      roleClaim: "groups",
      oidScopes: ["groups"],
      enableFolderRoles: false,
      folderRoleMapping: [],
      schemeOverride: "https",
      newPath: true,
      logoutUrl: "https://auth.home.garvey.sh/logout"
    }' \
  | curl -sSf -X POST -H "Authorization: MediaBrowser Token=\"$JF_API_KEY\"" \
      -H 'Content-Type: application/json' --data-binary @- \
      https://jellyfin.garvey.sh/sso/OID/Add/authelia
```

`schemeOverride` is needed because Jellyfin sees plain HTTP from Caddy and
would otherwise build an `http://` redirect URI, which Authelia rejects. To
limit someone to some libraries, set `enableAllFolders: false` and map groups
to library IDs (`GET /Library/MediaFolders`) in `folderRoleMapping`.

### Upgrading the plugin

Change `VERSION` and `SHA256` on `install-sso-plugin` in `deployment.yaml` (the
release publishes a `.sha256` file) and check that the release's `targetAbi`
matches the Jellyfin image.
