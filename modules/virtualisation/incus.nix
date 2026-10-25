# Incus virtualisation host with a btrfs default storage pool (homelab.incus options).

{ config, lib, pkgs, ... }:

let
  cfg = config.homelab.incus;

  # Prometheus in k3s reads /1.0/metrics with a JWT-SVID
  # (k8s/manifests/prometheus/scrape-configs/incus.yaml). Incus checks
  # the token against SPIRE's OIDC discovery provider (issuer, signature,
  # expiry, audience), then this scriptlet decides. Every OIDC client comes
  # here, and any pod can get a JWT-SVID for any audience, so the scriptlet
  # checks the subject: Prometheus may read and nothing else. The endpoint
  # needs can_view_metrics on the server, but Incus keeps each sample only if
  # the caller has can_view on its instance (instance:<project>/<name>) or, for
  # host-level samples, on the server: its can_view_metrics fallback is never
  # reached through a scriptlet. So Prometheus can also read server and
  # instance details through the API.
  metricsReader = "spiffe://home.garvey.sh/ns/prometheus/sa/prometheus";
  authorizationScriptlet = ''
    def authorize(details, object, entitlement):
        return (
            details.Protocol == "oidc"
            and details.Username == "${metricsReader}"
            and entitlement in ("can_view", "can_view_metrics")
            and (object == "server:incus" or object.startswith("instance:"))
        )
  '';
in
{
  options.homelab.incus = {
    enable = lib.mkEnableOption "Incus virtualisation host";

    storageSource = lib.mkOption {
      type = lib.types.str;
      description = "Host path backing the btrfs \"default\" storage pool.";
      example = "/fast/incus";
    };

    httpsAddress = lib.mkOption {
      type = lib.types.str;
      default = ":8443";
      description = "Listen address for the HTTPS API and web UI.";
    };

    adminUsers = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      description = "Users to add to the incus-admin group.";
    };
  };

  config = lib.mkIf cfg.enable {
    virtualisation.incus = {
      enable = true;
      ui.enable = true;
      preseed = {
        config = {
          "core.https_address" = cfg.httpsAddress;
          "oidc.issuer" = "https://spiffe-oidc.home.garvey.sh";
          # Incus enables OIDC only with a client ID set; bearer tokens never
          # use it (it is for the browser login flow, which SPIRE has none of).
          "oidc.client.id" = "incus";
          "oidc.audience" = "incus.home.garvey.sh";
          "authorization.client.oidc" = "scriptlet";
          "authorization.scriptlet" = authorizationScriptlet;
        };
        storage_pools = [
          {
            name = "default";
            driver = "btrfs";
            config = {
              source = cfg.storageSource;
            };
          }
        ];
      };
    };

    users.groups.incus-admin.members = cfg.adminUsers;

    # Running QEMU once faults it into the page cache, so incusd's startup
    # feature probe doesn't pay for the cold read. qemu_kvm is the same
    # derivation the incus module puts on incusd's PATH.
    #
    # "-" and the timeout keep this strictly best-effort: a slow or failed
    # warm-up must not stop incus from starting.
    systemd.services.incus.serviceConfig.ExecStartPre = lib.mkBefore [
      "-${pkgs.coreutils}/bin/timeout 60 ${pkgs.qemu_kvm}/bin/qemu-system-x86_64 -version"
    ];
  };
}
