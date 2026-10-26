# Frigate NVR in an nspawn container, with the Coral TPU and camera credentials passed in.

{ config, lib, pkgs, ... }:

let
  cfg = config.nspawn.frigate;
  # The name the UI is served under: the `frigate` record in
  # modules/networking/dns.nix, and what the certificate attests.
  domain = "frigate.home.garvey.sh";
  autheliaAuthz = "https://auth.home.garvey.sh/api/authz/auth-request";
  # The frigate module's vhost, behind the Authelia gate.
  frigatePort = 8080;
in
{
  imports = [ ./common.nix ];

  options.nspawn.frigate = {
    localAddress = lib.mkOption {
      type = lib.types.str;
      description = "IPv4 address with prefix length for the frigate container (e.g. \"10.28.12.109/16\" to match the flat LAN).";
    };

    localAddress6 = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      description = "IPv6 address with prefix length for the frigate container.";
    };

    hostBridgeAddress = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      description = "Host bridge IPv6 address in the same /64 as localAddress6: the container's next hop for the intra-site /48 (see containers/common.nix). Required with localAddress6.";
    };

    hostBridge = lib.mkOption {
      type = lib.types.str;
      description = "Host bridge interface for the container network.";
    };

    dataPath = lib.mkOption {
      type = lib.types.str;
      description = "Host path for persistent frigate data.";
    };

    cachePath = lib.mkOption {
      type = lib.types.str;
      description = "Host path for frigate cache.";
    };
  };

  config = {
    # Coral Edge TPU (host loads modules for nspawn container)
    boot.extraModulePackages = with config.boot.kernelPackages; [ gasket ];
    boot.kernelModules = [ "apex" ];
    services.udev.extraRules = ''
      SUBSYSTEM=="apex", MODE="0660", GROUP="root"
    '';

    # RTSP credentials for cameras, rendered into an env file and bind-mounted
    # into the container so Frigate can substitute {FRIGATE_RTSP_USER} /
    # {FRIGATE_RTSP_PASSWORD} at config-load time.
    sops.secrets.frigate-rtsp-user = {
      sopsFile = ../../secrets/frigate.yaml;
      key = "frigate_rtsp_user";
    };
    sops.secrets.frigate-rtsp-password = {
      sopsFile = ../../secrets/frigate.yaml;
      key = "frigate_rtsp_password";
    };
    sops.templates."frigate-rtsp.env".content = ''
      FRIGATE_RTSP_USER=${config.sops.placeholder.frigate-rtsp-user}
      FRIGATE_RTSP_PASSWORD=${config.sops.placeholder.frigate-rtsp-password}
    '';

    # Certificate for the UI, issued on the host by DNS-01 against our own Knot
    # (the garage.nix pattern) and bind-mounted into the container's nginx.
    # The TSIG key is scoped to `_acme-challenge.frigate` and TXT records only
    # (modules/containers/knot-auth.nix).
    sops.secrets.frigate-acme-tsig = {
      sopsFile = ../../secrets/frigate-acme.yaml;
      key = "acme_tsig_secret";
    };
    sops.templates."frigate-acme.env".content = ''
      RFC2136_NAMESERVER=10.28.0.6:53
      RFC2136_TSIG_KEY=acme-frigate
      RFC2136_TSIG_ALGORITHM=hmac-sha256.
      RFC2136_TSIG_SECRET=${config.sops.placeholder.frigate-acme-tsig}
    '';

    security.acme = {
      acceptTerms = true;
      defaults.email = "garvey.nick@gmail.com";
      certs.${domain} = {
        dnsProvider = "rfc2136";
        dnsResolver = "10.28.0.6:53";
        environmentFile = config.sops.templates."frigate-acme.env".path;
        # Off for the reason given in modules/containers/garage.nix: lego's
        # check queries the public NS, which from the LAN lands on blocky's
        # negative cache, and Knot commits the update before lego moves on.
        dnsPropagationCheck = false;
        # nginx reads the certificate at startup and frigate-tls-import re-runs
        # on every nginx start, so a restart picks up a renewal. `|| true` so
        # a stopped container cannot fail a renewal that already succeeded.
        postRun = ''
          ${pkgs.systemd}/bin/systemctl -M frigate restart nginx.service || true
        '';
      };
    };

    # nspawn needs the bind-mount source to exist at container start, which
    # can precede the first issuance.
    systemd.tmpfiles.rules = [
      "d /var/lib/acme/${domain} 0750 acme acme -"
    ];

    nspawn.network.frigate = {
      attachment = "bridge";
      hostBridge = cfg.hostBridge;
      localAddress = cfg.localAddress;
      localAddress6 = cfg.localAddress6;
      hostBridgeAddress = cfg.hostBridgeAddress;
      ipv4Gateway = "10.28.0.1";
      ipv4Nameservers = [ "10.28.0.1" ];
    };

    containers.frigate = {
      bindMounts = {
        "/var/lib/frigate" = {
          hostPath = cfg.dataPath;
          isReadOnly = false;
        };
        "/var/cache/frigate" = {
          hostPath = cfg.cachePath;
          isReadOnly = false;
        };
        "/run/frigate-rtsp.env" = {
          hostPath = config.sops.templates."frigate-rtsp.env".path;
          isReadOnly = true;
        };
        "/run/frigate-tls" = {
          hostPath = "/var/lib/acme/${domain}";
          isReadOnly = true;
        };
      };

      allowedDevices = [
        { node = "/dev/apex_0"; modifier = "rwm"; }
      ];
      extraFlags = [ "--bind=/dev/apex_0" ];

      config = { config, pkgs, lib, ... }: let
        # Frigate's UI log tab reads /dev/shm/logs/<svc>/current, populated by
        # s6-log in the Docker image. NixOS has no s6, so mirror the journal.
        mkLogMirror = svc: {
          description = "Mirror ${svc} journal to /dev/shm/logs/${svc}/current";
          after = [ "${svc}.service" ];
          wantedBy = [ "${svc}.service" ];
          serviceConfig = {
            ExecStart = "${pkgs.bash}/bin/bash -c 'exec ${pkgs.systemd}/bin/journalctl -u ${svc}.service -f -o cat -n 1000 > /dev/shm/logs/${svc}/current'";
            Restart = "always";
            RestartSec = "5";
          };
        };
      in {
        # Coral modules are loaded on the host; suppress inside container
        boot.extraModulePackages = lib.mkForce [];

        systemd.tmpfiles.rules = [
          "d /dev/shm/logs 0755 root root - -"
          "d /dev/shm/logs/frigate 0755 root root - -"
          "d /dev/shm/logs/go2rtc 0755 root root - -"
          "d /dev/shm/logs/nginx 0755 root root - -"
        ];

        systemd.services.frigate-log-mirror = mkLogMirror "frigate";
        systemd.services.go2rtc-log-mirror = mkLogMirror "go2rtc";
        systemd.services.nginx-log-mirror = mkLogMirror "nginx";

        # Fix /dev/apex_0 permissions (udev doesn't trigger for bind-mounted devices)
        systemd.services.frigate.serviceConfig.ExecStartPre = lib.mkBefore [
          "+${pkgs.coreutils}/bin/chown root:coral /dev/apex_0"
          "+${pkgs.coreutils}/bin/chmod 660 /dev/apex_0"
        ];

        # RTSP creds sourced from host-rendered sops template.
        systemd.services.frigate.serviceConfig.EnvironmentFile = "/run/frigate-rtsp.env";
        # NixOS splits frigate and go2rtc into separate services; go2rtc also
        # needs the RTSP creds to expand ${FRIGATE_RTSP_USER}/${FRIGATE_RTSP_PASSWORD}.
        systemd.services.go2rtc.serviceConfig.EnvironmentFile = "/run/frigate-rtsp.env";

        # Cap stop timeout: shutdown was hitting the 90s default and dominating
        # host poweroff time.
        systemd.services.frigate.serviceConfig.TimeoutStopSec = "5s";
        systemd.services.go2rtc.serviceConfig.TimeoutStopSec = "5s";

        services.go2rtc = {
          enable = true;
          settings = {
            # Loopback only: both are unauthenticated and would otherwise
            # serve live camera streams to the whole LAN, bypassing Frigate's
            # auth. All external access goes through nginx (auth_request) or
            # WebRTC on 8555 (DTLS-SRTP, gated by authenticated signaling).
            api.listen = "127.0.0.1:1984";
            rtsp.listen = "127.0.0.1:8554";
            streams.camera = [
              "rtsp://\${FRIGATE_RTSP_USER}:\${FRIGATE_RTSP_PASSWORD}@camera.home.garvey.sh:554/h264Preview_01_main"
            ];
            webrtc.candidates = [ "frigate.home.garvey.sh:8555" ];
          };
        };

        # nginx runs as the `nginx` user and cannot read the bind-mounted cert
        # (0640 acme:acme on the host; host gids mean nothing in here). Root
        # installs a private copy before every nginx start, which is also how
        # a renewed cert gets picked up.
        systemd.services.frigate-tls-import = {
          description = "Install the host-issued TLS certificate for nginx";
          before = [ "nginx.service" ];
          requiredBy = [ "nginx.service" ];
          serviceConfig.Type = "oneshot";
          path = [ pkgs.coreutils ];
          script = ''
            install -d -o nginx -g nginx -m 0700 /run/frigate-nginx-tls
            install -o nginx -g nginx -m 0400 \
              /run/frigate-tls/fullchain.pem /run/frigate-nginx-tls/fullchain.pem
            install -o nginx -g nginx -m 0400 \
              /run/frigate-tls/key.pem /run/frigate-nginx-tls/key.pem
          '';
        };

        # Two vhosts. The gate on 443 (80 only redirects) asks Authelia
        # (auth.home.garvey.sh, in k3s; policy in
        # k8s/manifests/authelia/configmap.yaml) about every request, the
        # skyforge web-gate.nix pattern, and passes the user and groups
        # Authelia returns to the frigate module's vhost on loopback. The gate
        # sets Remote-User and Remote-Groups itself, so whatever a client sent
        # under those names never reaches Frigate, which trusts them
        # (proxy.header_map below). The module's vhost cannot carry the
        # Authelia check itself: its locations already use auth_request for
        # Frigate's own /auth, and nginx allows one per location.
        # If k3s or Authelia is down, the UI is unreachable; recording goes on.
        services.nginx.recommendedTlsSettings = true;
        services.nginx.virtualHosts.frigate-gate = {
          serverName = domain;
          forceSSL = true;
          sslCertificate = "/run/frigate-nginx-tls/fullchain.pem";
          sslCertificateKey = "/run/frigate-nginx-tls/key.pem";
          extraConfig = ''
            auth_request /internal/authelia/authz;
            auth_request_set $authelia_user $upstream_http_remote_user;
            auth_request_set $authelia_groups $upstream_http_remote_groups;
            auth_request_set $redirection_url $upstream_http_location;
            error_page 401 =302 $redirection_url;
          '';
          locations = {
            # Authelia's nginx integration for the AuthRequest endpoint: 200,
            # or 401 with the portal URL in Location.
            "/internal/authelia/authz".extraConfig = ''
              internal;
              auth_request off;
              # A variable in proxy_pass makes nginx resolve the name per
              # request, so nginx still starts when DNS is not up yet.
              resolver 10.28.0.1 valid=60s;
              set $authelia_authz ${autheliaAuthz};
              proxy_pass $authelia_authz;
              proxy_ssl_server_name on;
              proxy_ssl_verify on;
              # The default depth of 1 stops short of Let's Encrypt's root.
              proxy_ssl_verify_depth 3;
              proxy_ssl_trusted_certificate /etc/ssl/certs/ca-certificates.crt;
              proxy_http_version 1.1;
              proxy_set_header X-Original-Method $request_method;
              proxy_set_header X-Original-URL $scheme://$http_host$request_uri;
              proxy_set_header X-Forwarded-For $remote_addr;
              proxy_set_header Content-Length "";
              proxy_set_header Connection "";
              proxy_pass_request_body off;
            '';
            "/" = {
              proxyPass = "http://127.0.0.1:${toString frigatePort}";
              proxyWebsockets = true;
              extraConfig = ''
                proxy_set_header Host $host;
                proxy_set_header X-Forwarded-For $remote_addr;
                proxy_set_header X-Forwarded-Proto $scheme;
                proxy_set_header Remote-User $authelia_user;
                proxy_set_header Remote-Groups $authelia_groups;
                # Live streams, recordings and exports go straight through.
                proxy_buffering off;
                proxy_request_buffering off;
                proxy_read_timeout 1h;
              '';
            };
          };
        };
        # Not port 5000: requests arriving there are trusted as admin without
        # auth (the module's internal listener).
        services.nginx.virtualHosts.frigate.listen = [
          { addr = "127.0.0.1"; port = frigatePort; }
        ];

        services.frigate = {
          enable = true;
          hostname = "frigate";
          # Build-time config validator can't resolve {FRIGATE_RTSP_USER}/{FRIGATE_RTSP_PASSWORD}
          # placeholders that Frigate substitutes at runtime from the env file.
          checkConfig = false;
          settings = {
            # Authelia authenticates (the gate above). With auth off, Frigate
            # takes the user and role from the headers the gate sets: members
            # of the Authelia `admins` group are admin, everyone else viewer.
            auth.enabled = false;
            proxy = {
              header_map = {
                user = "remote-user";
                role = "remote-groups";
                role_map.admin = [ "admins" ];
              };
              default_role = "viewer";
              logout_url = "https://auth.home.garvey.sh/logout";
            };
            mqtt.enabled = false;
            detectors.coral = {
              type = "edgetpu";
              device = "pci";
            };

            ffmpeg.output_args.record =
              "-f segment -segment_time 10 -segment_format mp4 "
              + "-reset_timestamps 1 -strftime 1 -c:v libx264 -an -crf 38";

            cameras.camera = {
              ffmpeg.inputs = [
                {
                  path = "rtsp://{FRIGATE_RTSP_USER}:{FRIGATE_RTSP_PASSWORD}@camera.home.garvey.sh:554/h264Preview_01_main";
                  roles = [ "record" ];
                }
                {
                  path = "rtsp://{FRIGATE_RTSP_USER}:{FRIGATE_RTSP_PASSWORD}@camera.home.garvey.sh:554/h264Preview_01_sub";
                  roles = [ "detect" ];
                }
              ];
              detect = {
                enabled = true;
                width = 640;
                height = 360;
              };
            };

            go2rtc = {
              streams.camera = [
                "rtsp://{FRIGATE_RTSP_USER}:{FRIGATE_RTSP_PASSWORD}@camera.home.garvey.sh:554/h264Preview_01_main"
              ];
              webrtc.candidates = [ "frigate.home.garvey.sh:8555" ];
            };

            objects.track = [ "cat" ];

            record = {
              enabled = true;
              retain = {
                days = 7;
                mode = "motion";
              };
              alerts.retain.days = 200;
              detections.retain.days = 200;
            };
          };
        };

        # Defense in depth over the go2rtc loopback binds: only the
        # authenticated nginx front (443, and 80 for the redirect) and WebRTC
        # media (8555, DTLS-SRTP) are reachable, from the LAN and from tailnet
        # devices (which arrive via dragonsreach's subnet route).
        networking.firewall = {
          enable = true;
          allowedTCPPorts = [ 80 443 8555 ];
          allowedUDPPorts = [ 8555 ];
        };
      };
    };
  };
}
