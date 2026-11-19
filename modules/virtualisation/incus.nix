# Incus virtualisation host with a btrfs default storage pool (homelab.incus options).

{ config, lib, pkgs, ... }:

let
  cfg = config.homelab.incus;

  # The metricsReaders read /1.0/metrics with a JWT-SVID (audience
  # incus.home.garvey.sh): this host's Vector (incus-metrics.nix). Incus
  # checks the token against SPIRE's OIDC discovery provider (issuer,
  # signature, expiry, audience), then this scriptlet decides. Every OIDC
  # client comes here, and any workload can get a JWT-SVID for any audience,
  # so the scriptlet checks the subject: the readers may read and nothing
  # else. The endpoint needs can_view_metrics on the server, but Incus keeps
  # each sample only if the caller has can_view on its instance
  # (instance:<project>/<name>) or, for host-level samples, on the server: its
  # can_view_metrics fallback is never reached through a scriptlet. So the
  # readers can also read server and instance details through the API.
  authorizationScriptlet = ''
    def authorize(details, object, entitlement):
        return (
            details.Protocol == "oidc"
            and details.Username in [${lib.concatMapStringsSep ", " (r: ''"${r}"'') cfg.metricsReaders}]
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

    acme = lib.mkOption {
      type = lib.types.nullOr (lib.types.submodule {
        options = {
          domain = lib.mkOption {
            type = lib.types.str;
            example = "incus.home.garvey.sh";
            description = "Name of the Let's Encrypt certificate Incus serves.";
          };
          tsigKey = lib.mkOption {
            type = lib.types.str;
            example = "acme-incus";
            description = "Knot TSIG key that may write the name's challenge (modules/containers/knot-auth.nix).";
          };
          sopsFile = lib.mkOption {
            type = lib.types.path;
            description = "sops file holding the key's secret as acme_tsig_secret.";
          };
        };
      });
      default = null;
      description = ''
        Serve a Let's Encrypt certificate instead of Incus's self-signed one.
        Incus's own ACME client issues and renews it, by DNS-01 in Knot.
      '';
    };

    metricsReaders = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      description = "SPIFFE IDs whose JWT-SVIDs may read /1.0/metrics.";
    };

    adminUsers = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      description = "Users to add to the incus-admin group.";
    };
  };

  config = lib.mkIf cfg.enable (lib.mkMerge [ {
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
  }

  (lib.mkIf (cfg.acme != null) {
    # Incus runs lego itself, renews daily when due and swaps the listener
    # certificate in place. VM agents trust a per-VM certificate, not this
    # one, so incus exec is unaffected by a renewal.
    #
    # The settings go in by `incus config set` rather than preseed because
    # acme.provider.environment holds the TSIG secret, and preseed is a store
    # path. lego gets that environment, which also turns off its propagation
    # check, as dnsAuthoritativePropagationCheck = false does for
    # security.acme: the check asks the public name servers, which the LAN
    # reaches only through blocky's negative cache, and Knot has committed the
    # update before lego moves on.
    sops.secrets.incus-acme-tsig = {
      inherit (cfg.acme) sopsFile;
      key = "acme_tsig_secret";
    };
    sops.templates."incus-acme.env".content = ''
      RFC2136_NAMESERVER=10.28.0.6:53
      RFC2136_TSIG_KEY=${cfg.acme.tsigKey}
      RFC2136_TSIG_ALGORITHM=hmac-sha256.
      RFC2136_TSIG_SECRET=${config.sops.placeholder.incus-acme-tsig}
      LEGO_DNS_PROPAGATION_DISABLE_ANS=true
      LEGO_DNS_PROPAGATION_DISABLE_RNS=true
    '';
    systemd.services.incus-acme = {
      description = "Configure Incus's ACME client";
      after = [ "incus.service" "incus-preseed.service" ];
      requires = [ "incus.service" ];
      wantedBy = [ "multi-user.target" ];
      serviceConfig = {
        Type = "oneshot";
        RemainAfterExit = true;
      };
      script = ''
        ${config.virtualisation.incus.package.client}/bin/incus config set \
          acme.domain=${cfg.acme.domain} \
          acme.email=garvey.nick@gmail.com \
          acme.agree_tos=true \
          acme.challenge=DNS-01 \
          acme.provider=rfc2136 \
          acme.provider.resolvers=10.28.0.6:53 \
          acme.provider.environment="$(cat ${config.sops.templates."incus-acme.env".path})"
      '';
    };
  })
  ]);
}
