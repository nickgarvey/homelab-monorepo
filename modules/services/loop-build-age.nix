# Hourly lookup of when Loop was last built and uploaded to TestFlight, pushed as a metric for the expiry alert.
#
# TestFlight builds expire 90 days after upload. Builds come from the
# nickgarvey/LoopWorkspace fork's GitHub Actions (`nix run .#loop-build`, and
# the fork's weekly "4. Build Loop Auto"). A successful run of either build
# workflow means a build was uploaded: the auto workflow cancels its own run
# when there is nothing to build. The repo is public, so the GitHub API needs
# no token.
# Alerts: k8s/manifests/prometheus/rules/loop-build.yaml.
{ config, lib, pkgs, ... }:
let
  stateDir = "/var/lib/loop-build-age";
  repo = "nickgarvey/LoopWorkspace";
  lookup = pkgs.writeShellApplication {
    name = "loop-build-age";
    runtimeInputs = [ pkgs.curl pkgs.jq pkgs.coreutils ];
    text = ''
      # A failed lookup leaves the last published time in place, so the build
      # age keeps growing and the alert still fires if lookups stay broken.
      latest=0
      for wf in build_loop.yml build_loop_auto.yml; do
        runs=$(curl -fsS --retry 3 -H 'Accept: application/vnd.github+json' \
          "https://api.github.com/repos/${repo}/actions/workflows/$wf/runs?status=success&per_page=1")
        t=$(jq -r '.workflow_runs[0].updated_at // empty | fromdateiso8601' <<< "$runs")
        if [ -n "$t" ] && [ "$t" -gt "$latest" ]; then latest=$t; fi
      done
      [ "$latest" -gt 0 ] || { echo "no successful Loop build found" >&2; exit 1; }
      echo "$latest" > ${stateDir}/last-build.new
      chmod 0644 ${stateDir}/last-build.new
      mv ${stateDir}/last-build.new ${stateDir}/last-build
    '';
  };
in
{
  users.users.loop-build-age = {
    isSystemUser = true;
    group = "loop-build-age";
  };
  users.groups.loop-build-age = { };
  systemd.services.loop-build-age = {
    description = "Look up when Loop was last uploaded to TestFlight";
    after = [ "network-online.target" ];
    wants = [ "network-online.target" ];
    serviceConfig = {
      Type = "oneshot";
      ExecStart = lib.getExe lookup;
      # A static user, not DynamicUser: that would put the state directory
      # under the root-only /var/lib/private, out of vector's reach.
      User = "loop-build-age";
      Group = "loop-build-age";
      StateDirectory = "loop-build-age";
      StateDirectoryMode = "0755";
    };
  };
  systemd.timers.loop-build-age = {
    wantedBy = [ "timers.target" ];
    timerConfig = {
      OnCalendar = "hourly";
      Persistent = true;
      RandomizedDelaySec = "5m";
    };
  };
  # Pushed rather than scraped (modules/services/vector-agent.nix), every 180s
  # so the series stays inside Prometheus's 5-minute staleness window.
  homelab.metrics.sources.loop_build = {
    type = "exec";
    mode = "scheduled";
    scheduled.exec_interval_secs = 180;
    command = [ "${pkgs.bash}/bin/bash" "-c" "cat ${stateDir}/last-build 2>/dev/null || true" ];
  };
  homelab.metrics.transforms.loop_build_fields = {
    type = "remap";
    inputs = [ "loop_build" ];
    source = ''
      .at = to_int!(strip_whitespace!(to_string!(.message)))
    '';
  };
  homelab.metrics.transforms.loop_build_metric = {
    type = "log_to_metric";
    inputs = [ "loop_build_fields" ];
    metrics = [{
      type = "gauge";
      field = "at";
      name = "homelab_loop_build_last_success_timestamp_seconds";
      tags.hostname = config.networking.hostName;
    }];
  };
}
