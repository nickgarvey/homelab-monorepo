# habit-helper

Habit Helper's data: the `habit_helper` database on the shared postgres
(`manifests/postgres/`), served by PostgREST at
`http://postgrest.habit-helper.svc.cluster.local:3000` to sandboxes of the
`habit-helper` agent type, through the `habit-data` provider
(`agents/habit-helper/habit-data-profile.yaml` at the repo root). The agent
type and the `habit` CLI that uses it come later (nix-psvkl.5, .7).

## Roles

| Role | Login | What it can do |
|---|---|---|
| `habit_helper` | certificate `CN=habit_helper` | Owns the database and its schemas; only the schema init container uses it. |
| `habit_helper_api` | certificate `CN=habit_helper_api` | PostgREST's login. `NOINHERIT` and granted nothing but `CONNECT` and membership in `habit_agent`, so on its own it can read nothing. |
| `habit_agent` | none | `SELECT`, `INSERT`, `UPDATE` on the tables in schema `habit`. No `DELETE`, `TRUNCATE`, DDL or temporary tables. |

The roles are managed by CloudNativePG (`manifests/postgres/cluster.yaml`),
which also has their `pg_hba` and `pg_ident` lines; both certificates are
from the SPIFFE CA, allowed only in this namespace by the
`spiffe-habit-helper-api` and `spiffe-habit-helper-owner` policies
(`manifests/cert-manager/spiffe-policies.yaml`). The grants are in the
migrations, which also close `CONNECT` on the database to everyone but
`habit_helper_api`, so there is no manual `REVOKE` step.

## Schema

`agents/habit-helper/schema/`, numbered SQL files, applied in order by
[tern](https://github.com/jackc/tern) (`pkgs.tern`) in the `schema` init
container, as `habit_helper`. tern records the version in
`public.schema_version`, runs each file in its own transaction and takes an
advisory lock, so two pods starting at once do not race; on a current schema
it does nothing. A new migration is the next number (`002_<what>.sql`); files
already applied are never edited. tern reads the files as Go templates, so
`{{` has no place in them. Picked over dbmate and golang-migrate (no up/down
markers or dirty state to clear by hand) and over a psql loop (no psql in the
image, and tern already does the locking and bookkeeping).

Schema `habit`:

| Table | Key | Columns |
|---|---|---|
| `habits` | `name` | `cue` ("if <event>, then <behavior>"), `motivation`, `preparation`, `status` (`active`, `paused`, `retired`), `created_at`, `updated_at` |
| `entries` | `habit`, `day` | `outcome` (`done`, `missed`), `why`, `created_at`, `updated_at` |
| `notes` | `id` | `habit`, `kind` (`motivation`, `cue`, `prep`), `body`, `at` |
| `cadence` | `habit` | `interval_days` (1 to 60), `next_check`, `streak`, `misses`, `last_check`, `reason`, `updated_at` |

A habit is referred to by name everywhere; renaming one carries over to the
other tables, and names differing only in case are refused. Adding a habit
also adds its `cadence` row, due today. `day`, `next_check` and `last_check`
are the user's local dates, sent by the client. The tapering rule that sets
`cadence` belongs to the CLI; the agent may override it with a `reason`.
Triggers keep `updated_at`; their functions are in schema `habit_private`,
which PostgREST does not serve.

PostgREST requests the CLI can use (with the injected token; `day` etc. are
examples):

```sh
P=http://postgrest.habit-helper.svc.cluster.local:3000
curl -s "$P/habits?status=eq.active"
curl -s "$P/habits" -H 'Content-Type: application/json' \
  -d '{"name":"meditate","cue":"if I pour my coffee, then I sit for 5 minutes","motivation":"calmer mornings","preparation":"cushion by the window"}'
# Log a day, or log it again: an upsert on (habit, day). Columns left out
# keep their value, so send "why": null to clear an old reason.
curl -s "$P/entries?on_conflict=habit,day" -H 'Content-Type: application/json' \
  -H 'Prefer: resolution=merge-duplicates' \
  -d '{"habit":"meditate","day":"2026-10-13","outcome":"missed","why":"overslept"}'
# Active habits with their entry for a day (an empty list: not logged yet).
curl -s "$P/habits?status=eq.active&select=name,entries(outcome)&entries.day=eq.2026-10-13"
curl -s "$P/entries?habit=eq.meditate&day=gte.2026-09-13&order=day"
curl -s "$P/notes" -H 'Content-Type: application/json' \
  -d '{"habit":"meditate","kind":"motivation","body":"wants to feel less rushed"}'
curl -s "$P/cadence?order=next_check"
curl -s -X PATCH "$P/cadence?habit=eq.meditate" -H 'Content-Type: application/json' \
  -d '{"interval_days":3,"next_check":"2026-10-16","reason":"asked for fewer check-ins"}'
```

`DELETE` is refused by the provider profile and, behind it, by Postgres.

## PostgREST

`deployment.yaml`, from our image `oci.garvey.sh/habit-helper-postgrest`
(`agents/habit-helper/postgrest-image.nix`: `pkgs.postgrest`, tern and the
migrations). Configured by environment only (`PGRST_DB_CONFIG=false`).

- **Authentication.** No anonymous role: a request without a valid JWT is
  answered 401. A JWT must be HS256 with the key in `secret.yaml`
  (`postgrest-jwt`), unexpired, and if it has an `aud`, `habit-data`. PostgREST
  runs the request as the JWT's `role`; `habit_helper_api` can only become
  `habit_agent`, so any other role is refused. The only other holder of the
  key is agent-token (`manifests/agents/agent-token-habit-data-secret.yaml`),
  whose `habit-data` grant gives `spiffe://home.garvey.sh/agent/habit-helper/*`
  a token with `role: habit_agent`, `aud: habit-data`, `sub` = the SPIFFE ID
  and a 5 minute lifetime ("The token service" in `manifests/agents/README.md`).
- **Transport.** Plain HTTP on the ClusterIP Service: the sandbox
  supervisor's proxy verifies upstream TLS against the public roots only, so
  a spiffe-ca certificate here would be refused. The token crosses the pod
  network in cleartext and is good for 5 minutes. HTTPS would need the
  supervisor image to put the spiffe-ca root where the proxy looks
  (`/etc/ssl/certs/ca-certificates.crt`), which would make the proxy trust
  spiffe-ca for every upstream.
- **Network.** No CiliumNetworkPolicy of its own: there is no LoadBalancer,
  and `manifests/cilium/pod-ingress-default-deny.yaml` keeps the LAN out of
  the pod. Inside the cluster, the JWT is the boundary. The admin port (3001:
  `/live`, `/ready`, `/metrics`, `/schema_cache`) holds nothing secret.
- **Postgres.** It logs in as `habit_helper_api` with
  `postgres-client-certificates.yaml` (24h); libpq reads the files for every
  new connection, so renewals need no restart.

### Rotating the JWT key

Write the same new value into both `secret.yaml` here (`jwt-secret`) and
`manifests/agents/agent-token-habit-data-secret.yaml` (`postgrest-jwt-key`)
with `sops`, at least 32 characters, apply both, then
`kubectl -n habit-helper rollout restart deploy/postgrest` (PostgREST reads the
key at start; agent-token reads it for every grant). Tokens signed with the
old key fail until their 5 minutes are up.

## Image

From the repo root:

```sh
nix build .#habit-helper-postgrest-image
skopeo copy docker-archive:result docker://oci.garvey.sh/habit-helper-postgrest:$(nix eval --raw .#habit-helper-postgrest-image.imageTag)
```

The tag is the store hash of the image's contents, so a new migration or a
nixpkgs bump changes it; set it in `deployment.yaml` (the `&image` anchor).

## Apply (from `k8s/`)

`manifests/cert-manager/spiffe-policies.yaml` first, then the roles and the
database, then this namespace:

```sh
kubectl apply -f manifests/cert-manager/spiffe-policies.yaml
kubectl apply -f manifests/postgres/cluster.yaml -f manifests/postgres/databases.yaml
kubectl -n postgres get cluster postgres -o jsonpath='{.status.managedRolesStatus.byStatus.reconciled}'   # lists the three roles
kubectl apply -f manifests/habit-helper/namespace.yaml
kubectl apply -f manifests/habit-helper/secret.yaml
kubectl apply -f manifests/habit-helper/service-account.yaml
kubectl apply -f manifests/habit-helper/postgres-client-certificates.yaml
kubectl -n habit-helper wait --for=condition=Ready certificate --all
kubectl apply -f manifests/habit-helper/deployment.yaml
kubectl apply -f manifests/habit-helper/service.yaml
kubectl -n habit-helper rollout status deploy/postgrest
```

agent-token's side: `manifests/agents/agent-token-habit-data-secret.yaml`,
then the ConfigMap and Deployment, as in "Apply" in `manifests/agents/README.md`.
The profile and provider are published by `nix run .#agents-apply`.
