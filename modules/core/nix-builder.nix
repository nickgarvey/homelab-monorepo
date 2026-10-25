# Lets talos offload Nix builds to this host over ssh-ng, with the builds held to a share of its memory and CPU.
#
# talos is the only client: nix.buildMachines in hosts/talos/configuration.nix
# lists the hosts that import this. Builds are optional capacity there. talos
# can still build everything itself, so this host being down only makes a
# build slower (nix-oc1lr).
#
# talos's nix-daemon logs in with an OpenBao-signed nix-ssh certificate, and
# this host presents an OpenBao-signed host certificate (docs/openbao.md).
# nix.sshServe creates the nix-ssh user, which can only speak the Nix store
# protocol. `trusted` lets it import the unsigned derivations talos sends, which
# a remote builder needs.
#
# The builds run in this host's nix-daemon.service cgroup (nix-daemon --stdio
# hands them to the main daemon). The limits there keep a large build, such
# as Ceph's ~2.5 GB-per-compiler-process OSD sources, from pushing out the
# host's own services. No substituter is served from here: clients reach
# celler and cache.nixos.org directly.
{ config, lib, pkgs, ... }:
let
  cfg = config.homelab.nixBuilder;
  hostname = config.networking.hostName;
  addr = "https://openbao.home.garvey.sh:8200";
  hostCert = "/etc/ssh/ssh_host_ed25519_key-cert.pub";
  # Signs this host's SSH host key with the OpenBao host CA, so talos trusts it
  # by CA (programs.ssh.knownHosts in modules/desktop/openbao-ssh-cert.nix)
  # rather than a pinned key. Certificates last 30 days; renewed with 10 left.
  renewHostCert = pkgs.writeShellApplication {
    name = "openbao-host-cert";
    runtimeInputs = with pkgs; [ openssh curl jq coreutils gnused systemd ];
    text = ''
      if [ -f ${hostCert} ]; then
        end=$(ssh-keygen -L -f ${hostCert} | sed -n 's/^ *Valid: from .* to //p')
        if [ -n "$end" ] && [ $(( $(date -d "$end" +%s) - $(date +%s) )) -gt 864000 ]; then
          exit 0
        fi
      fi
      c=$CREDENTIALS_DIRECTORY
      token=$(jq -n --rawfile r "$c/role-id" --rawfile s "$c/secret-id" \
          '{role_id: ($r | rtrimstr("\n")), secret_id: ($s | rtrimstr("\n"))}' \
        | curl -fsS --data @- ${addr}/v1/auth/approle/login | jq -er .auth.client_token)
      jq -n --arg k "$(cat /etc/ssh/ssh_host_ed25519_key.pub)" --arg p "${hostname},${hostname}.home.garvey.sh" \
          '{public_key: $k, cert_type: "host", valid_principals: $p}' \
        | curl -fsS -H "X-Vault-Token: $token" --data @- ${addr}/v1/ssh-host/sign/builder-host \
        | jq -er '.data.signed_key | rtrimstr("\n")' > ${hostCert}.tmp
      mv ${hostCert}.tmp ${hostCert}
      systemctl try-reload-or-restart sshd.service
      curl -fsS -X POST -H "X-Vault-Token: $token" ${addr}/v1/auth/token/revoke-self >/dev/null || true
    '';
  };
in
{
  options.homelab.nixBuilder = {
    memoryMax = lib.mkOption {
      type = lib.types.str;
      example = "48G";
      description = "MemoryMax for nix-daemon.service, which every build on this host runs under.";
    };
    cores = lib.mkOption {
      type = lib.types.ints.unsigned;
      default = 0;
      description = "nix.settings.cores: make -j for each build. 0 uses every core.";
    };
  };

  config = {
    nix.sshServe = {
      enable = true;
      protocol = "ssh-ng";
      write = true;
      trusted = true;
      # No authorized keys: talos logs in with an OpenBao-signed nix-ssh
      # certificate (TrustedUserCAKeys in modules/core/nixos-common.nix).
      keys = [ ];
    };

    # sshd has no keepalive by default, so if talos vanishes mid-build the
    # session, and the build it drives, would run on until TCP gives up. This
    # ends both ~30s after talos stops answering, matching talos's side
    # (ServerAliveInterval in hosts/talos/configuration.nix).
    services.openssh.extraConfig = ''
      Match User nix-ssh
        ClientAliveInterval 15
        ClientAliveCountMax 2
      Match All
    '';

    # The host certificate (renewHostCert above). sshd only logs an error and
    # carries on when the file is missing, as it is before the first signing.
    services.openssh.settings.HostCertificate = hostCert;
    sops.secrets = lib.mapAttrs (_: key: {
      sopsFile = ../../secrets/openbao-builder-host.yaml;
      inherit key;
    }) {
      openbao-builder-host-role-id = "role_id";
      openbao-builder-host-secret-id = "secret_id";
    };
    systemd.services.openbao-host-cert = {
      description = "Renew the OpenBao-signed SSH host certificate";
      wantedBy = [ "multi-user.target" ];
      after = [ "network-online.target" "sshd.service" ];
      wants = [ "network-online.target" ];
      serviceConfig = {
        Type = "oneshot";
        ExecStart = lib.getExe renewHostCert;
        LoadCredential = [
          "role-id:${config.sops.secrets.openbao-builder-host-role-id.path}"
          "secret-id:${config.sops.secrets.openbao-builder-host-secret-id.path}"
        ];
      };
    };
    systemd.timers.openbao-host-cert = {
      wantedBy = [ "timers.target" ];
      timerConfig = { OnCalendar = "daily"; RandomizedDelaySec = "1h"; Persistent = true; };
    };

    nix.settings.cores = cfg.cores;

    # Builds yield to the host's own work for CPU and disk, and are the ones
    # OOM-killed when they outgrow memoryMax.
    systemd.services.nix-daemon.serviceConfig = {
      MemoryMax = cfg.memoryMax;
      CPUWeight = 20;
      IOWeight = 20;
      Nice = 10;
    };
  };
}
