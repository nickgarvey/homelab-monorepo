# Nightscout

Nightscout (`nightscout/cgm-remote-monitor`) at https://nightscout.home.garvey.sh,
reachable on the LAN and over Tailscale only. Loop on the phone uploads to it
with `API_SECRET`; everything else needs a token (`AUTH_DEFAULT_ROLES=denied`).

| File | What |
|---|---|
| `mongo-statefulset.yaml`, `mongo-service.yaml`, `mongo-init-configmap.yaml`, `pvc.yaml` | MongoDB 6.0, one replica, volume `mongo-data` on ceph-rbd |
| `deployment.yaml`, `caddy-configmap.yaml`, `service.yaml`, `certificate.yaml`, `network-policy.yaml` | Nightscout behind a Caddy sidecar on `2001:470:482f:2::5012` |
| `backup-cronjob.yaml` | Nightly `mongodump` to Garage |
| `secret.yaml` | SopsSecret: `mongo-credentials`, `nightscout-env` (`MONGODB_URI`, `API_SECRET`), `nightscout-backup-s3` |

## Apply

`certificate.yaml` must be Ready before the Deployment starts, since the pod
mounts the `nightscout-tls` secret. From `k8s/`:

```bash
kubectl apply -f manifests/nightscout/namespace.yaml
kubectl apply -f manifests/nightscout/secret.yaml -f manifests/nightscout/certificate.yaml
kubectl -n nightscout wait certificate/nightscout-tls --for=condition=Ready --timeout=300s
kubectl apply -f manifests/nightscout/
```

Caddy does not reload a renewed certificate: after each renewal,
`kubectl -n nightscout rollout restart deploy/nightscout`.

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
   way as in `backup-cronjob.yaml`; the key is the `nightscout-backup-s3`
   Secret (or any Garage key with read on the bucket):

   ```bash
   export RCLONE_CONFIG_GARAGE_TYPE=s3 RCLONE_CONFIG_GARAGE_PROVIDER=Other \
     RCLONE_CONFIG_GARAGE_ENDPOINT=https://garage.home.garvey.sh \
     RCLONE_CONFIG_GARAGE_REGION=garage RCLONE_CONFIG_GARAGE_FORCE_PATH_STYLE=true
   eval "$(kubectl -n nightscout get secret nightscout-backup-s3 -o go-template='{{range $k, $v := .data}}export {{$k}}={{$v | base64decode}}{{"\n"}}{{end}}')"
   nix run nixpkgs#rclone -- lsl garage:nightscout-backup/daily
   nix run nixpkgs#rclone -- copyto garage:nightscout-backup/daily/<file> ./nightscout.archive.gz
   ```

2. Copy it into the mongo pod and restore over the existing database:

   ```bash
   kubectl -n nightscout cp ./nightscout.archive.gz mongo-0:/tmp/nightscout.archive.gz
   kubectl -n nightscout exec -it mongo-0 -- sh -c \
     'mongorestore --uri="mongodb://$MONGO_INITDB_ROOT_USERNAME:$MONGO_INITDB_ROOT_PASSWORD@localhost:27017/?authSource=admin" \
        --gzip --archive=/tmp/nightscout.archive.gz --drop --nsInclude="nightscout.*"'
   ```

   `--drop` replaces each collection; leave it off to merge. To check a dump
   without touching live data, restore into another database instead with
   `--nsFrom='nightscout.*' --nsTo='restoretest.*'`, then drop that database.

3. `kubectl -n nightscout rollout restart deploy/nightscout`.

On a fresh, empty `mongo-data` volume, apply the manifests first: the image
creates the root and `nightscout` users on first start, then restore as above.
