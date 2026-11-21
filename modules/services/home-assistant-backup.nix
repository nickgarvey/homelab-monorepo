# Daily off-host backup of Home Assistant's state (/fast/homeassistant) to the homeassistant-backup bucket on Ceph RGW.
#
# Each run takes a read-only btrfs snapshot of the @homeassistant subvolume, so
# HA's config and database and the Z-Wave JS, Matter server and OTBR data are
# captured at one instant, as after a power cut (SQLite in WAL mode recovers
# from that). The snapshot is written as <UTC timestamp>.tar.zst, checked,
# uploaded, and its size compared with what RGW stored.
#
# RGW runs on joor/zah/frul, a separate failure domain from lydia. The bucket
# is owned by the k8s account, so the Ceph backup worker copies it into Kopia
# on Garage and on to Storj (docs/ceph-backup.md). The bucket's lifecycle rule
# expires objects after 30 days. The timer runs before the worker's 10:00 UTC
# schedule, so each day's archive reaches Storj the same day.
#
# The upload holds no key: the job fetches its JWT-SVID,
# spiffe://home.garvey.sh/host/lydia/homeassistant-backup, from the host's
# SPIRE agent (modules/services/spire-agent.nix; the entry in
# k8s/manifests/spire/host-entries.yaml selects this unit), and the AWS CLI
# trades it with RGW's STS for 1h credentials of the homeassistant-backup role
# (modules/services/rgw-roles.nix), which may use only this bucket.
#
# Each success is pushed to Prometheus as a timestamp and a size;
# k8s/manifests/prometheus/rules/homeassistant-backup.yaml alerts when the
# backup goes stale or shrinks. Restore steps: docs/home-assistant.md.
{ config, lib, pkgs, ... }:

let
  source = "/fast/homeassistant";
  # On the same filesystem as the source (a snapshot cannot cross filesystems),
  # outside btrbk's .snapshots so its retention never sees it.
  snapshot = "/mnt/fast-root/.homeassistant-backup";
  bucket = "homeassistant-backup";
  endpoint = "https://rgw.home.garvey.sh";
  stateDir = "/var/lib/homeassistant-backup";
  role = "arn:aws:iam::RGW77873637642885287:role/nix-managed/homeassistant-backup";

  backup = pkgs.writeShellApplication {
    name = "homeassistant-backup";
    runtimeInputs = with pkgs; [ btrfs-progs gnutar zstd awscli2 coreutils jq config.services.spire.agent.package ];
    text = ''
      stamp=$(date -u +%Y-%m-%dT%H%M%SZ)
      out="$CACHE_DIRECTORY/$stamp.tar.zst"
      key="$stamp.tar.zst"

      # A run killed midway leaves its snapshot behind.
      if [ -e ${snapshot} ]; then btrfs subvolume delete ${snapshot} >/dev/null; fi
      cleanup() {
        if [ -e ${snapshot} ]; then btrfs subvolume delete ${snapshot} >/dev/null; fi
        rm -f "$out"
      }
      trap cleanup EXIT

      btrfs subvolume snapshot -r ${source} ${snapshot} >/dev/null
      tar -C ${snapshot} --numeric-owner --xattrs --acls -c . | zstd -q -T0 -o "$out"
      zstd -tq "$out"
      size=$(stat -c %s "$out")

      # Fetched after the archive is built, so the 1h credentials cover only
      # the upload. -j: the AWS CLI sends the file's bytes as the token, and
      # RGW refuses one with a trailing newline.
      spire-agent api fetch jwt -audience rgw.home.garvey.sh \
        -socketPath ${config.services.spire.agent.settings.agent.socket_path} -timeout 30s -output json \
        | jq -jer 'first(.[].svids? // empty | .[0].svid)' > "$RUNTIME_DIRECTORY/jwt"
      export AWS_WEB_IDENTITY_TOKEN_FILE="$RUNTIME_DIRECTORY/jwt"
      export AWS_ROLE_ARN=${role}
      export AWS_ROLE_SESSION_NAME=homeassistant-backup
      export AWS_ENDPOINT_URL_STS=${endpoint}
      aws s3 cp --only-show-errors --endpoint-url ${endpoint} "$out" "s3://${bucket}/$key"
      stored=$(aws s3api head-object --endpoint-url ${endpoint} --bucket ${bucket} --key "$key" \
        --query ContentLength --output text)
      if [ "$stored" != "$size" ]; then
        echo "uploaded $key is $stored bytes, expected $size" >&2
        exit 1
      fi

      echo "$size" > ${stateDir}/last-size
      date +%s > ${stateDir}/last-success
    '';
  };

  probe = pkgs.writeShellApplication {
    name = "homeassistant-backup-probe";
    text = ''
      t=0; [ -f ${stateDir}/last-success ] && t=$(cat ${stateDir}/last-success)
      s=0; [ -f ${stateDir}/last-size ] && s=$(cat ${stateDir}/last-size)
      echo "{\"last_success\":$t,\"last_size\":$s}"
    '';
  };
in
{
  assertions = [{
    assertion = config.services.spire.agent.enable;
    message = "home-assistant-backup.nix needs modules/services/spire-agent.nix.";
  }];

  systemd.services.homeassistant-backup = {
    description = "Back up Home Assistant's state to Ceph RGW";
    wants = [ "network-online.target" "spire-agent.service" ];
    after = [ "network-online.target" "spire-agent.service" ];
    unitConfig.RequiresMountsFor = [ source "/mnt/fast-root" ];
    serviceConfig = {
      Type = "oneshot";
      ExecStart = lib.getExe backup;
      Nice = 10;
      IOSchedulingClass = "idle";
      StateDirectory = "homeassistant-backup";
      CacheDirectory = "homeassistant-backup";
      # Holds the JWT-SVID; removed when the run ends.
      RuntimeDirectory = "homeassistant-backup";
      RuntimeDirectoryMode = "0700";
    };
  };

  systemd.timers.homeassistant-backup = {
    wantedBy = [ "timers.target" ];
    timerConfig = {
      OnCalendar = "*-*-* 08:00:00 UTC";
      RandomizedDelaySec = "30m";
      Persistent = true;
    };
  };

  homelab.metrics.sources.homeassistant_backup = {
    type = "exec";
    mode = "scheduled";
    scheduled.exec_interval_secs = 180;
    command = [ (lib.getExe probe) ];
  };
  homelab.metrics.transforms.homeassistant_backup_fields = {
    type = "remap";
    inputs = [ "homeassistant_backup" ];
    source = ". = parse_json!(strip_whitespace!(to_string!(.message)))";
  };
  homelab.metrics.transforms.homeassistant_backup_metric = {
    type = "log_to_metric";
    inputs = [ "homeassistant_backup_fields" ];
    metrics = map (m: {
      type = "gauge";
      inherit (m) field name;
      tags.hostname = config.networking.hostName;
    }) [
      { field = "last_success"; name = "homeassistant_backup_last_success_timestamp_seconds"; }
      { field = "last_size"; name = "homeassistant_backup_last_size_bytes"; }
    ];
  };
}
