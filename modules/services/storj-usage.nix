# Hourly measurement of how much is stored on Storj, per bucket, pushed as metrics for the usage alert.
#
# Lists every bucket through the S3 gateway (modules/containers/storj-gateway.nix,
# which this module needs on the same host for its S3 keys) and sums object
# sizes with `rclone size`. That approximates Storj's billed storage: it leaves
# out in-flight multipart uploads and the small encryption overhead, and it
# sees only the buckets the gateway's access grant can see.
# Alerts: k8s/manifests/prometheus/rules/storj.yaml.
{ config, lib, pkgs, ... }:

let
  stateDir = "/var/lib/storj-usage";

  measure = pkgs.writeShellApplication {
    name = "storj-usage";
    runtimeInputs = [ pkgs.rclone pkgs.jq pkgs.coreutils ];
    text = ''
      # Results replace the published files only once every bucket has been
      # measured, so a failed run leaves the last good numbers and the
      # last-success timestamp ages until the stale alert fires.
      tmp=$(mktemp -p ${stateDir})
      trap 'rm -f "$tmp"' EXIT

      # Not a process substitution: its failure would not stop the script.
      list=$(rclone lsjson --dirs-only storj:)
      mapfile -t buckets < <(jq -r '.[].Name' <<< "$list")
      for b in "''${buckets[@]}"; do
        rclone size --json "storj:$b" |
          jq -r --arg b "$b" '"\($b) \(.bytes) \(.count)"' >> "$tmp"
      done

      chmod 0644 "$tmp"
      mv "$tmp" ${stateDir}/buckets
      date +%s > ${stateDir}/last-success.new
      mv ${stateDir}/last-success.new ${stateDir}/last-success
    '';
  };
in
{
  users.users.storj-usage = {
    isSystemUser = true;
    group = "storj-usage";
  };
  users.groups.storj-usage = { };

  sops.templates."storj-usage.env".content = ''
    RCLONE_CONFIG_STORJ_TYPE=s3
    RCLONE_CONFIG_STORJ_PROVIDER=Other
    RCLONE_CONFIG_STORJ_ENDPOINT=https://${config.nspawn.storj-gateway.tls.domain}:7777
    RCLONE_CONFIG_STORJ_ACCESS_KEY_ID=${config.sops.placeholder.storj-s3-access-key}
    RCLONE_CONFIG_STORJ_SECRET_ACCESS_KEY=${config.sops.placeholder.storj-s3-secret-key}
  '';

  systemd.services.storj-usage = {
    description = "Measure bytes stored per Storj bucket";
    after = [ "network-online.target" "container@storj-gateway.service" ];
    wants = [ "network-online.target" ];
    serviceConfig = {
      Type = "oneshot";
      ExecStart = lib.getExe measure;
      # A static user, not DynamicUser: that would put the state directory
      # under the root-only /var/lib/private, out of vector's reach.
      User = "storj-usage";
      Group = "storj-usage";
      # Read by PID 1 before the switch to the service user.
      EnvironmentFile = config.sops.templates."storj-usage.env".path;
      StateDirectory = "storj-usage";
      # vector (below) reads the results.
      StateDirectoryMode = "0755";
      CacheDirectory = "storj-usage";
      Environment = [ "RCLONE_CACHE_DIR=%C/storj-usage" "RCLONE_CONFIG=/dev/null" ];
      TimeoutStartSec = "30min";
    };
  };

  systemd.timers.storj-usage = {
    wantedBy = [ "timers.target" ];
    timerConfig = {
      OnCalendar = "hourly";
      Persistent = true;
      RandomizedDelaySec = "5m";
    };
  };

  # Pushed rather than scraped (modules/services/vector-agent.nix), every 180s
  # so the series stay inside Prometheus's 5-minute staleness window.
  homelab.metrics.sources.storj_usage_buckets = {
    type = "exec";
    mode = "scheduled";
    scheduled.exec_interval_secs = 180;
    command = [ "${pkgs.bash}/bin/bash" "-c" "cat ${stateDir}/buckets 2>/dev/null || true" ];
  };
  homelab.metrics.transforms.storj_usage_buckets_fields = {
    type = "remap";
    inputs = [ "storj_usage_buckets" ];
    source = ''
      parts = split(strip_whitespace!(to_string!(.message)), " ")
      .bucket = parts[0]
      .bytes = to_int!(parts[1])
      .objects = to_int!(parts[2])
    '';
  };
  homelab.metrics.transforms.storj_usage_buckets_metric = {
    type = "log_to_metric";
    inputs = [ "storj_usage_buckets_fields" ];
    metrics = [
      {
        type = "gauge";
        field = "bytes";
        name = "homelab_storj_bucket_bytes";
        tags = {
          bucket = "{{ bucket }}";
          hostname = config.networking.hostName;
        };
      }
      {
        type = "gauge";
        field = "objects";
        name = "homelab_storj_bucket_objects";
        tags = {
          bucket = "{{ bucket }}";
          hostname = config.networking.hostName;
        };
      }
    ];
  };

  homelab.metrics.sources.storj_usage_run = {
    type = "exec";
    mode = "scheduled";
    scheduled.exec_interval_secs = 180;
    command = [ "${pkgs.bash}/bin/bash" "-c" "cat ${stateDir}/last-success 2>/dev/null || true" ];
  };
  homelab.metrics.transforms.storj_usage_run_fields = {
    type = "remap";
    inputs = [ "storj_usage_run" ];
    source = ''
      .at = to_int!(strip_whitespace!(to_string!(.message)))
    '';
  };
  homelab.metrics.transforms.storj_usage_run_metric = {
    type = "log_to_metric";
    inputs = [ "storj_usage_run_fields" ];
    metrics = [{
      type = "gauge";
      field = "at";
      name = "homelab_storj_usage_last_success_timestamp_seconds";
      tags.hostname = config.networking.hostName;
    }];
  };
}
