# Nightscout

Nightscout (`nightscout/cgm-remote-monitor`) at https://nightscout.home.garvey.sh,
reachable on the LAN and over Tailscale only. Loop on the phone uploads to it
with `API_SECRET`; everything else needs a token (`AUTH_DEFAULT_ROLES=denied`).

| File | What |
|---|---|
| `mongo-statefulset.yaml`, `mongo-service.yaml`, `mongo-init-configmap.yaml`, `pvc.yaml` | MongoDB 6.0, one replica, volume `mongo-data` on ceph-rbd |
| `deployment.yaml`, `caddy-configmap.yaml`, `service.yaml`, `certificate.yaml`, `network-policy.yaml` | Nightscout behind a Caddy sidecar on `2001:470:482f:2::5012` |
| `backup-cronjob.yaml` | Nightly `mongodump` to Garage |
| `secret-provider-class.yaml` | ServiceAccounts `nightscout`, `mongo`, `nightscout-backup` and the SecretProviderClasses that mount each one's secrets from OpenBao (docs/openbao.md, "Pod secrets") |
| `secrets/openbao/nightscout/nightscout.yaml` | sops: Nightscout's `MONGODB_URI` and `API_SECRET` |
| `secrets/openbao/nightscout/mongo.yaml` | sops: `MONGO_INITDB_ROOT_*` and `NIGHTSCOUT_DB_*` |
| `secrets/openbao/nightscout/nightscout-backup.yaml` | sops: the backup's `MONGODB_URI` and Garage key (`RCLONE_CONFIG_GARAGE_*`) |

## Apply

The secrets must be in OpenBao before the pods start (they fail to mount
otherwise), and the three roles must exist (`pki/openbao/tofu.sh apply`).
`certificate.yaml` must be Ready before the Deployment starts, since the pod
mounts the `nightscout-tls` secret. From `k8s/`:

```bash
bash scripts/push-secrets.sh secrets/openbao/nightscout/nightscout.yaml \
  secrets/openbao/nightscout/mongo.yaml secrets/openbao/nightscout/nightscout-backup.yaml
kubectl apply -f manifests/nightscout/namespace.yaml
kubectl apply -f manifests/nightscout/secret-provider-class.yaml -f manifests/nightscout/certificate.yaml
kubectl -n nightscout wait certificate/nightscout-tls --for=condition=Ready --timeout=300s
kubectl apply -f manifests/nightscout/
```

Caddy does not reload a renewed certificate: after each renewal,
`kubectl -n nightscout rollout restart deploy/nightscout`.

## Metrics

`manifests/metrics-collector` polls the v1 API every minute and serves the
newest glucose reading, Loop status (IOB, COB, forecast), pump and phone state,
and the time of the last pod change and sensor start as `nightscout_*` metrics
in the main Prometheus. Its token belongs to the subject `metrics-collector`
(Admin Tools → Subjects), which has only the `readable` role; the token is in
`secrets/openbao/metrics-collector/metrics-collector.yaml`.

## Backups

Two independent copies:

- The ceph-backup worker snapshots the `mongo-data` RBD image daily with the
  rest of Ceph (`manifests/ceph-backup/README.md`). That snapshot is taken
  under a running mongod, so it is crash-consistent.
- `backup-cronjob.yaml` runs `mongodump` of the `nightscout` database at
  04:00 UTC and uploads it to `s3://nightscout-backup/daily/` on Garage, keeping
  30 days.

Run a backup now:

```bash
kubectl -n nightscout create job --from=cronjob/nightscout-backup nightscout-backup-manual-$(date +%s)
```

## Restore from a dump

1. Download the archive. rclone is configured from the environment the same
   way as in `backup-cronjob.yaml`; the key is `secrets.s3` in
   `secrets/openbao/nightscout/nightscout-backup.yaml` (or any Garage key with read on the bucket):

   ```bash
   export RCLONE_CONFIG_GARAGE_TYPE=s3 RCLONE_CONFIG_GARAGE_PROVIDER=Other \
     RCLONE_CONFIG_GARAGE_ENDPOINT=https://garage.home.garvey.sh \
     RCLONE_CONFIG_GARAGE_REGION=garage RCLONE_CONFIG_GARAGE_FORCE_PATH_STYLE=true
   eval "$(nix run nixpkgs#sops -- decrypt --output-type json secrets/openbao/nightscout/nightscout-backup.yaml \
     | nix run nixpkgs#jq -- -r '.secrets.s3 | to_entries[] | "export \(.key)=\(.value | @sh)"')"
   nix run nixpkgs#rclone -- lsl garage:nightscout-backup/daily
   nix run nixpkgs#rclone -- copyto garage:nightscout-backup/daily/<file> ./nightscout.archive.gz
   ```

2. Copy it into the mongo pod and restore over the existing database:

   ```bash
   kubectl -n nightscout cp ./nightscout.archive.gz mongo-0:/tmp/nightscout.archive.gz
   kubectl -n nightscout exec -it mongo-0 -- sh -c \
     'u=$(cat /run/secrets/env/MONGO_INITDB_ROOT_USERNAME) p=$(cat /run/secrets/env/MONGO_INITDB_ROOT_PASSWORD)
      mongorestore --uri="mongodb://$u:$p@localhost:27017/?authSource=admin" \
        --gzip --archive=/tmp/nightscout.archive.gz --drop --nsInclude="nightscout.*"'
   ```

   `--drop` replaces each collection; leave it off to merge. To check a dump
   without touching live data, restore into another database instead with
   `--nsFrom='nightscout.*' --nsTo='restoretest.*'`, then drop that database.

3. `kubectl -n nightscout rollout restart deploy/nightscout`.

On a fresh, empty `mongo-data` volume, apply the manifests first: the image
creates the root and `nightscout` users on first start, then restore as above.
