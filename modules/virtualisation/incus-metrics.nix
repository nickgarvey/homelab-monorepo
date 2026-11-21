# Vector scrapes this Incus host's /1.0/metrics with its own JWT-SVID and pushes the samples to Prometheus.
#
# Vector's SPIFFE ID, spiffe://home.garvey.sh/host/<host>/vector, comes from
# the host's SPIRE agent (modules/services/spire-agent.nix) through the entry
# in k8s/manifests/spire/host-entries.yaml, which selects vector.service. The
# token is fetched by Vector's exec secret backend: the script runs as a child
# of Vector, so the agent attests it as vector.service. Vector resolves
# secrets only when it loads its config, so a timer reloads it well inside
# the JWT-SVID's 1h lifetime. Incus lets this ID read metrics through its
# authorization scriptlet (homelab.incus.metricsReaders).
#
# Needs modules/virtualisation/incus.nix and modules/services/spire-agent.nix.
{ config, lib, pkgs, ... }:
let
  host = config.networking.hostName;
  audience = "incus.home.garvey.sh";
  socket = config.services.spire.agent.settings.agent.socket_path;

  # Vector's exec secret protocol: a JSON request on stdin, and on stdout a
  # value for every secret asked for. A backend error would fail the whole
  # config load and stop every host metric, so when SPIRE is unreachable the
  # script hands back a placeholder: Incus refuses the scrape until the next
  # reload, and the other metrics keep flowing.
  fetchToken = pkgs.writeShellApplication {
    name = "incus-metrics-token";
    runtimeInputs = [ config.services.spire.agent.package pkgs.jq pkgs.coreutils ];
    text = ''
      cat > /dev/null
      if ! token=$(spire-agent api fetch jwt -audience ${audience} \
          -socketPath ${socket} -timeout 10s -output json \
          | jq -er 'first(.[].svids? // empty | .[0].svid)'); then
        echo "incus-metrics-token: no JWT-SVID from the SPIRE agent; Incus will refuse the scrape until the next reload" >&2
        token=unavailable
      fi
      jq -nc --arg t "$token" '{incus_token: {value: $t, error: null}}'
    '';
  };
in
{
  assertions = [
    { assertion = config.homelab.incus.enable; message = "incus-metrics.nix needs homelab.incus.enable."; }
    { assertion = config.services.spire.agent.enable; message = "incus-metrics.nix needs modules/services/spire-agent.nix."; }
  ];

  homelab.incus.metricsReaders = [ "spiffe://home.garvey.sh/host/${host}/vector" ];

  services.vector.settings.secret.spiffe = {
    type = "exec";
    command = [ (lib.getExe fetchToken) ];
    timeout = 20;
  };

  homelab.metrics.sources.incus = {
    type = "prometheus_scrape";
    # Incus serves a Let's Encrypt certificate for this name.
    endpoints = [ "https://incus.home.garvey.sh:8443/1.0/metrics" ];
    scrape_interval_secs = 30;
    auth = {
      strategy = "bearer";
      token = "SECRET[spiffe.incus_token]";
    };
  };
  # The labels queries select Incus samples by.
  homelab.metrics.transforms.incus_metrics = {
    type = "remap";
    inputs = [ "incus" ];
    source = ''
      .tags.job = "incus"
      .tags.hostname = "${host}"
    '';
  };
  homelab.metrics.sinkInputs = [ "incus_metrics" ];

  systemd.services.vector-token-reload = {
    description = "Reload Vector so it fetches a fresh JWT-SVID for Incus";
    serviceConfig = {
      Type = "oneshot";
      ExecStart = "${config.systemd.package}/bin/systemctl try-reload-or-restart vector.service";
    };
  };
  systemd.timers.vector-token-reload = {
    wantedBy = [ "timers.target" ];
    timerConfig = {
      OnActiveSec = "20min";
      OnUnitActiveSec = "20min";
    };
  };
}
