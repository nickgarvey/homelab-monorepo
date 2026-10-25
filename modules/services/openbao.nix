# OpenBao, the private CA's issuing tier: one node of the 3-node Raft cluster on fus, ro and dah (docs/openbao.md).
#
# Runs as a plain systemd service, not in k3s, so certificate issuance keeps
# working when the cluster is down. Raft keeps its data on the node's own disk;
# nothing here depends on k3s or Ceph at runtime.
{ config, lib, pkgs, ... }:
let
  hostname = config.networking.hostName;
  nodes = [ "fus" "ro" "dah" ];
  domain = "home.garvey.sh";
  sharedName = "openbao.${domain}";
  nodeName = n: "openbao-${n}.${domain}";
  certDir = "/var/lib/acme/${sharedName}";
  # Knot's container is IPv4-only and these nodes are IPv6-only (as in
  # modules/services/ceph.nix).
  knotViaNat64 = "[64:ff9b::a1c:6]:53";
  # Plain HTTP on loopback, for this node's own tooling: the snapshot timer, the
  # metrics probe, the deploy tool's health gate, and recovery (it is the only
  # listener that serves the unauthenticated generate-root endpoints). Anyone who
  # can reach it already has root here, and with it the seal key.
  localAddr = "http://127.0.0.1:8202";
  stateDir = "/var/lib/openbao-snapshot";
  # The daily snapshot timer (below). Its AppRole credentials only exist once the
  # cluster is initialised; a rebuilt cluster turns this off until they are.
  snapshots = true;

  probe = pkgs.writeShellApplication {
    name = "openbao-probe";
    runtimeInputs = with pkgs; [ curl jq openssl coreutils ];
    text = ''
      # One JSON line for the metrics transform below. sys/health with these
      # codes always answers 200, so the body is all that matters.
      h=$(curl -fsS -m 5 '${localAddr}/v1/sys/health?standbyok=true&sealedcode=200&uninitcode=200' || echo '{}')
      end=$(openssl s_client -connect '${nodeName hostname}:8200' -servername '${nodeName hostname}' </dev/null 2>/dev/null \
        | openssl x509 -noout -enddate 2>/dev/null | cut -d= -f2 || true)
      cert=0; [ -n "$end" ] && cert=$(date -d "$end" +%s)
      snap=0; [ -f ${stateDir}/last-success ] && snap=$(cat ${stateDir}/last-success)
      jq -cn --argjson h "$h" --argjson cert "$cert" --argjson snap "$snap" '{
        up: (if $h == {} then 0 else 1 end),
        sealed: (if $h.sealed == false then 0 else 1 end),
        active: (if $h.sealed == false and $h.standby == false then 1 else 0 end),
        cert: $cert, snap: $snap }'
    '';
  };

  snapshot = pkgs.writeShellApplication {
    name = "openbao-snapshot";
    runtimeInputs = with pkgs; [ curl openbao awscli2 coreutils ];
    text = ''
      # Every node runs this daily; only the active node writes a snapshot.
      code=$(curl -sS -o /dev/null -w '%{http_code}' -m 5 '${localAddr}/v1/sys/health' || true)
      if [ "$code" != 200 ]; then echo "not the active node (sys/health $code); skipping"; exit 0; fi
      export BAO_ADDR=${localAddr}
      c=$CREDENTIALS_DIRECTORY
      BAO_TOKEN=$(bao write -field=token auth/approle/login \
        role_id=@"$c/role-id" secret_id=@"$c/secret-id")
      export BAO_TOKEN
      f="$RUNTIME_DIRECTORY/raft.snap"
      bao operator raft snapshot save "$f"
      AWS_ACCESS_KEY_ID=$(cat "$c/s3-access-key") AWS_SECRET_ACCESS_KEY=$(cat "$c/s3-secret-key") \
      AWS_REQUEST_CHECKSUM_CALCULATION=when_required AWS_DEFAULT_REGION=default \
        aws s3 cp --only-show-errors --endpoint-url https://rgw.${domain} \
          "$f" "s3://openbao-snapshots/$(date -u +%Y-%m-%dT%H%M%SZ)-${hostname}.snap"
      date +%s > ${stateDir}/last-success
    '';
  };
in
{
  config = lib.mkMerge [ {
    assertions = [{
      assertion = lib.elem hostname nodes;
      message = "modules/services/openbao.nix is for the Raft nodes ${toString nodes} only";
    }];

    sops.secrets.openbao-seal-key = {
      sopsFile = ../../secrets/openbao.yaml;
      key = "seal_key";
    };

    # The listener certificate, by DNS-01 against our Knot like the garage and RGW
    # nodes (docs/tls.md). The TSIG key may only write TXT at the four OpenBao
    # challenge names (modules/containers/knot-auth.nix).
    sops.secrets.openbao-acme-tsig = {
      sopsFile = ../../secrets/openbao-acme.yaml;
      key = "acme_tsig_secret";
    };
    sops.templates."openbao-acme.env".content = ''
      RFC2136_NAMESERVER=${knotViaNat64}
      RFC2136_TSIG_KEY=acme-openbao
      RFC2136_TSIG_ALGORITHM=hmac-sha256.
      RFC2136_TSIG_SECRET=${config.sops.placeholder.openbao-acme-tsig}
    '';
    users.groups.openbao-tls = { };
    security.acme = {
      acceptTerms = true;
      defaults.email = "garvey.nick@gmail.com";
      certs.${sharedName} = {
        extraDomainNames = [ (nodeName hostname) ];
        dnsProvider = "rfc2136";
        dnsResolver = knotViaNat64;
        environmentFile = config.sops.templates."openbao-acme.env".path;
        # Same reason as modules/containers/garage.nix: lego's propagation check
        # queries the zone's NS via blocky, whose negative cache outlasts its
        # timeout; Knot is the only authoritative server.
        dnsPropagationCheck = false;
        group = "openbao-tls";
        # OpenBao re-reads its listener certificate on SIGHUP (the unit's reload).
        reloadServices = [ "openbao.service" ];
      };
    };

    services.openbao = {
      enable = true;
      settings = {
        api_addr = "https://${nodeName hostname}:8200";
        cluster_addr = "https://${nodeName hostname}:8201";
        listener.api = {
          type = "tcp";
          address = "[::]:8200";
          cluster_address = "[::]:8201";
          tls_cert_file = "${certDir}/fullchain.pem";
          tls_key_file = "${certDir}/key.pem";
        };
        listener.local = {
          type = "tcp";
          address = "127.0.0.1:8202";
          tls_disable = true;
          disable_unauthed_generate_root_endpoints = false;
        };
        storage.raft = {
          path = "/var/lib/openbao";
          node_id = hostname;
          retry_join = map (n: { leader_api_addr = "https://${nodeName n}:8200"; })
            (lib.remove hostname nodes);
        };
        # Every request and response to the journal of the node that served it
      # (secret values are HMAC'd). OpenBao takes audit devices from its config
      # file only; a failing device would block requests, and stdout cannot fail.
      audit = [{
        file.stdout = {
          description = "Audit log to the journal";
          options.file_path = "stdout";
        };
      }];
      seal.static = {
          current_key_id = "seal-2026-10";
          current_key = "file:///run/credentials/openbao.service/seal-key";
        };
      };
    };

    systemd.services.openbao = {
      # acme-<name>.service puts a placeholder certificate in place before the
      # first real one is issued, so the listener always has files to load.
      wants = [ "acme-${sharedName}.service" ];
      after = [ "acme-${sharedName}.service" ];
      serviceConfig = {
        LoadCredential = [ "seal-key:${config.sops.secrets.openbao-seal-key.path}" ];
        SupplementaryGroups = [ "openbao-tls" ];
      };
    };

    # Health, listener-certificate expiry and the last snapshot, pushed to
    # Prometheus (k8s/manifests/prometheus/rules/openbao.yaml).
    homelab.metrics.sources.openbao = {
      type = "exec";
      mode = "scheduled";
      # Under Prometheus's 5-minute staleness window, as for the other pushed
      # metrics (modules/router/wan-dns-drift.nix).
      scheduled.exec_interval_secs = 180;
      command = [ (lib.getExe probe) ];
    };
    homelab.metrics.transforms.openbao_fields = {
      type = "remap";
      inputs = [ "openbao" ];
      source = ". = parse_json!(strip_whitespace!(to_string!(.message)))";
    };
    homelab.metrics.transforms.openbao_metric = {
      type = "log_to_metric";
      inputs = [ "openbao_fields" ];
      metrics = map (m: {
        type = "gauge";
        inherit (m) field name;
        tags.hostname = hostname;
      }) [
        { field = "up"; name = "openbao_up"; }
        { field = "sealed"; name = "openbao_sealed"; }
        { field = "active"; name = "openbao_active"; }
        { field = "cert"; name = "openbao_listener_cert_expiry_timestamp_seconds"; }
        { field = "snap"; name = "openbao_snapshot_last_success_timestamp_seconds"; }
      ];
    };

    systemd.tmpfiles.rules = [ "d ${stateDir} 0755 root root -" ];
  }

  (lib.mkIf snapshots {
    sops.secrets = lib.mapAttrs (_: key: {
      sopsFile = ../../secrets/openbao-snapshot.yaml;
      inherit key;
    }) {
      openbao-snapshot-role-id = "role_id";
      openbao-snapshot-secret-id = "secret_id";
      openbao-snapshot-s3-access-key = "s3_access_key";
      openbao-snapshot-s3-secret-key = "s3_secret_key";
    };
    # Daily Raft snapshot to the openbao-snapshots bucket on Ceph RGW (joor, zah,
    # frul: a separate failure domain from these nodes). The bucket's lifecycle
    # rule expires snapshots after 30 days. AppRole role `snapshot` may only read
    # sys/storage/raft/snapshot (pki/openbao/).
    systemd.services.openbao-snapshot = {
      description = "Save an OpenBao Raft snapshot to Ceph RGW";
      after = [ "openbao.service" "network-online.target" ];
      wants = [ "network-online.target" ];
      serviceConfig = {
        Type = "oneshot";
        ExecStart = lib.getExe snapshot;
        RuntimeDirectory = "openbao-snapshot";
        # The bao CLI looks for its config under $HOME, shelling out to find
        # one when it is unset.
        Environment = "HOME=/run/openbao-snapshot";
        LoadCredential = map (n: "${n}:${config.sops.secrets."openbao-snapshot-${n}".path}")
          [ "role-id" "secret-id" "s3-access-key" "s3-secret-key" ];
      };
    };
    systemd.timers.openbao-snapshot = {
      wantedBy = [ "timers.target" ];
      timerConfig = { OnCalendar = "daily"; RandomizedDelaySec = "1h"; Persistent = true; };
    };
  })
  ];
}
