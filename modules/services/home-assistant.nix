# Home Assistant as a podman pod that is its own host on the LAN: HA, the Matter server, OTBR and Z-Wave JS UI share one network namespace on vmbr0.
#
# Network. A holder container (the pause image) owns the namespace: a veth on
# vmbr0 through netavark's bridge driver in unmanaged mode, with the identity
# of the `homeassistant` entry in modules/networking/lan-hosts.nix (MAC, IPv4,
# IPv6; static, so DNS and every client keep working unchanged). The other
# containers join it with --network=container:, and the holder never changes
# with nixpkgs, so deploys leave the namespace alone. The namespace's sysctls
# are set when the holder is created (they are read-only inside it): IPv6
# forwarding with accept_ra=2 for OTBR, and route-information options up to
# /64 so the router's /48 RIO and the Thread OMR routes are learned. netavark
# runs with firewall_driver "none" and adds no nftables rules on lydia;
# lydia's nixos-fw does not see bridged traffic, so the pod is unfiltered,
# as the HAOS box was. Port 8123 is protected by HA's own auth; the Matter
# server, OTBR's REST API and web UI and Z-Wave JS UI listen on loopback only.
#
# Radios. OTBR and Z-Wave JS UI need the two USB sticks, so they only run with
# radios.enable. OTBR's start script builds its ip6tables/ipset rules inside
# the pod's namespace (tested on lydia on 2026-11-16, ait nix-4SVOl).
#
# State lives in /fast/homeassistant (hosts/lydia/configuration.nix), backed
# up by modules/services/home-assistant-backup.nix. TLS: lego on lydia issues
# homeassistant.home.garvey.sh through Knot (key acme-homeassistant-lydia in
# modules/containers/knot-auth.nix); HA reads it from /ssl at startup, so a
# renewal restarts the HA container.
{ config, lib, pkgs, ... }:

let
  cfg = config.homelab.homeassistant;
  inherit (import ../networking/lan-hosts.nix) lanHosts;
  lanEntry = lib.findFirst (h: h.hostname == "homeassistant")
    (throw "home-assistant: no homeassistant entry in modules/networking/lan-hosts.nix") lanHosts;

  name = "homeassistant.home.garvey.sh";
  state = "/fast/homeassistant";
  network = "homeassistant-lan";
  holder = "homeassistant-net";

  # Versions match what ran on HAOS at migration (ait nix-4SVOl.1), so the
  # restored data needs no migration. Pinned by digest; the tag is a comment.
  images = {
    pause = "registry.k8s.io/pause@sha256:278fb9dbcca9518083ad1e11276933a2e96f23de604a3a08cc3c80002767d24c"; # 3.10.1
    homeassistant = "ghcr.io/home-assistant/home-assistant@sha256:6785d1eca68747a524c156faae1b139ed415c07357386ae6fe17291986e59e82"; # 2026.11.1
    matter = "ghcr.io/matter-js/matterjs-server@sha256:ee149c7c67eff0e90f131e8f0d628f7b099591ec58473c8cda1209e8ae4df66e"; # 1.5.5
    otbr = "docker.io/openthread/border-router@sha256:70d2260eb5022f4c40be4f50195a1952d49041fc766fb3b2f213b06dcdc31a18"; # v2026.10.0
    zwave = "docker.io/zwavejs/zwave-js-ui@sha256:00360afd0b0e833d6166d4127f4ef41d2b24c75f2b2172d32dd4e9d26d613778"; # 11.26.2 (Z-Wave JS 15.32.3)
  };

  zwaveRadio = "/dev/serial/by-id/usb-Silicon_Labs_HubZ_Smart_Home_Controller_41500614-if00-port0";
  threadRadio = "/dev/serial/by-id/usb-Nordic_Semiconductor_nRF528xx_OpenThread_Device_D978657F3F3E-if00";
  # The HubZ's second port is its Zigbee radio, which ZHA inside HA drives.
  zigbeeRadio = "/dev/serial/by-id/usb-Silicon_Labs_HubZ_Smart_Home_Controller_41500614-if01-port0";

  joinPod = [ "--network=container:${holder}" ];
  members = [ "homeassistant" "matter-server" ] ++ lib.optionals cfg.radios.enable [ "otbr" "zwave-js" ];

in
{
  options.homelab.homeassistant = {
    enable = lib.mkEnableOption "Home Assistant as a podman pod on vmbr0";

    identity = {
      mac = lib.mkOption {
        type = lib.types.str;
        default = lanEntry.mac;
        description = "MAC address of the pod's interface on vmbr0.";
      };
      ipv4 = lib.mkOption {
        type = lib.types.str;
        default = lanEntry.ipv4;
        description = "The pod's IPv4 address in 10.28.0.0/16.";
      };
      ipv6 = lib.mkOption {
        type = lib.types.str;
        default = lanEntry.ipv6;
        description = "The pod's IPv6 address in 2001:470:482f::/64.";
      };
    };

    radios.enable = lib.mkEnableOption ''
      OTBR and Z-Wave JS UI, which need the Thread and Z-Wave USB sticks
      plugged into this host
    '';
  };

  config = lib.mkIf cfg.enable (lib.mkMerge [
    {
      virtualisation.podman.enable = true;
      virtualisation.oci-containers.backend = "podman";
      # netavark would otherwise add NAT and forwarding rules to lydia's
      # nftables; this network needs none.
      virtualisation.containers.containersConf.settings.network.firewall_driver = "none";
      # No network here uses aardvark-dns, so lydia's firewall needs no port-53
      # opening for podman* interfaces.
      virtualisation.podman.dns.openFirewall = false;

      virtualisation.oci-containers.containers = {
        ${holder} = {
          image = images.pause;
          networks = [ network ];
          hostname = "homeassistant";
          extraOptions = [
            "--mac-address=${cfg.identity.mac}"
            "--ip=${cfg.identity.ipv4}"
            "--ip6=${cfg.identity.ipv6}"
            "--dns=10.28.0.1"
            "--sysctl=net.ipv4.conf.all.forwarding=1"
            "--sysctl=net.ipv6.conf.all.forwarding=1"
            "--sysctl=net.ipv6.conf.eth0.forwarding=1"
            "--sysctl=net.ipv6.conf.eth0.accept_ra=2"
            "--sysctl=net.ipv6.conf.eth0.accept_ra_rt_info_max_plen=64"
            # Only the static address: no SLAAC address from the router's prefix.
            "--sysctl=net.ipv6.conf.eth0.autoconf=0"
          ];
        };

        homeassistant = {
          image = images.homeassistant;
          # NET_RAW for DHCP-based discovery (aiodhcpwatcher) and ping, as on HAOS.
          extraOptions = joinPod ++ [ "--cap-add=NET_RAW" ];
          volumes = [
            "${state}/config:/config"
            "/var/lib/acme/${name}:/ssl:ro"
          ];
        };

        matter-server = {
          image = images.matter;
          extraOptions = joinPod;
          volumes = [ "${state}/matter:/data" ];
          cmd = [
            "--storage-path" "/data"
            # The fabric the HAOS add-on created: vendor 0x134B (Nabu Casa),
            # fabric 2, stored in /data/server-2-134b. Without these the server
            # starts a new, empty fabric under its defaults (0xFFF1, 1).
            "--vendorid" "4939"
            "--fabricid" "2"
            "--listen-address" "127.0.0.1"
            "--listen-address" "::1"
            "--primary-interface" "eth0"
            # The Thread devices built in firmware/ use the test vendor ID 0xFFF1.
            "--enable-test-net-dcl"
          ];
        };
      };

      # Every member shares the holder's namespace: it stops with the holder,
      # and starts again whenever the holder starts (also when the holder is
      # restarted after a crash). Wants, not Upholds: Upholds would restart a
      # member that a deploy stopped, from its old unit, before the new one is
      # loaded, and the deploy would then leave that stale container running.
      systemd.services = lib.mkMerge [
        (lib.genAttrs (map (m: "podman-${m}") members) (_: {
          bindsTo = [ "podman-${holder}.service" ];
          after = [ "podman-${holder}.service" ];
          unitConfig.RequiresMountsFor = [ state ];
        }))
        {
          "podman-${holder}".wants = map (m: "podman-${m}.service") members;

          "podman-network-${network}" = {
            description = "podman network ${network} (vmbr0, unmanaged)";
            wantedBy = [ "multi-user.target" ];
            before = [ "podman-${holder}.service" ];
            requiredBy = [ "podman-${holder}.service" ];
            path = [ config.virtualisation.podman.package ];
            serviceConfig = { Type = "oneshot"; RemainAfterExit = true; };
            script = ''
              podman network exists ${network} || podman network create \
                --driver bridge --opt mode=unmanaged --interface-name vmbr0 --disable-dns \
                --subnet 10.28.0.0/16 --gateway 10.28.0.1 \
                --ipv6 --subnet 2001:470:482f::/64 --gateway 2001:470:482f::1 \
                ${network}
            '';
          };
        }
      ];

      systemd.tmpfiles.rules = [
        "d ${state}/config 0700 root root -"
        # The Matter server image runs as 1000:1000.
        "d ${state}/matter 0700 1000 1000 -"
        "d ${state}/thread 0700 root root -"
        "d ${state}/zwave 0700 root root -"
        # The bind-mount source must exist before the first issuance.
        "d /var/lib/acme/${name} 0750 acme acme -"
      ];

      sops.secrets.homeassistant-acme-tsig = {
        sopsFile = ../../secrets/homeassistant-acme.yaml;
        key = "acme_tsig_secret";
      };
      sops.templates."homeassistant-acme.env".content = ''
        RFC2136_NAMESERVER=10.28.0.6:53
        RFC2136_TSIG_KEY=acme-homeassistant-lydia
        RFC2136_TSIG_ALGORITHM=hmac-sha256.
        RFC2136_TSIG_SECRET=${config.sops.placeholder.homeassistant-acme-tsig}
      '';
      security.acme = {
        acceptTerms = true;
        defaults.email = "garvey.nick@gmail.com";
        certs.${name} = {
          dnsProvider = "rfc2136";
          dnsResolver = "10.28.0.6:53";
          environmentFile = config.sops.templates."homeassistant-acme.env".path;
          # Off for the reason given in modules/containers/garage.nix: lego's
          # check queries the public NS, which from the LAN lands on blocky's
          # negative cache.
          dnsAuthoritativePropagationCheck = false;
          # HA loads the certificate only at startup.
          postRun = ''
            ${pkgs.systemd}/bin/systemctl try-restart podman-homeassistant.service || true
          '';
        };
      };
    }

    (lib.mkIf cfg.radios.enable {
      virtualisation.oci-containers.containers = {
        # ZHA's config entry names the radio by this by-id path.
        homeassistant.devices = [ "${zigbeeRadio}:${zigbeeRadio}" ];

        otbr = {
          image = images.otbr;
          extraOptions = joinPod ++ [ "--cap-add=NET_ADMIN" "--cap-add=NET_RAW" ];
          devices = [ "/dev/net/tun" "${threadRadio}:/dev/ttyACM0" ];
          volumes = [ "${state}/thread:/data/thread" ];
          environment = {
            OT_INFRA_IF = "eth0";
            OT_THREAD_IF = "wpan0";
            OT_LOG_LEVEL = "6";
            # As on HAOS: 460800 baud with hardware flow control.
            OT_RCP_DEVICE = "spinel+hdlc+uart:///dev/ttyACM0?uart-baudrate=460800&uart-flow-control";
            OT_REST_LISTEN_ADDR = "127.0.0.1";
            OT_WEB_LISTEN_ADDR = "127.0.0.1";
          };
        };

        zwave-js = {
          image = images.zwave;
          extraOptions = joinPod;
          devices = [ "${zwaveRadio}:/dev/zwave" ];
          volumes = [
            "${state}/zwave:/usr/src/app/store"
            "${config.sops.templates."homeassistant-zwave-settings.json".path}:/etc/zwave-settings.json:ro"
          ];
          environment = {
            ZWAVE_PORT = "/dev/zwave";
            ZWAVE_EXTERNAL_SETTINGS = "/etc/zwave-settings.json";
            HOST = "127.0.0.1";
          };
        };
      };

      # Z-Wave JS UI's Z-Wave settings, as the HAOS Z-Wave JS add-on passed them:
      # its store's settings.json holds no `zwave` section, everything comes
      # from this file (flat keys, api/lib/externalSettings.ts upstream). The
      # cache directory is the store's cache/, where the add-on kept it, so the
      # network is not re-interviewed. The keys are the add-on's
      # (ait nix-4SVOl.1).
      sops.secrets = lib.genAttrs [
        "zwave-s0-legacy" "zwave-s2-unauthenticated" "zwave-s2-authenticated"
        "zwave-s2-access-control" "zwave-lr-s2-authenticated" "zwave-lr-s2-access-control"
      ] (key: { sopsFile = ../../secrets/homeassistant.yaml; inherit key; });
      sops.templates."homeassistant-zwave-settings.json".content =
        with config.sops.placeholder; builtins.toJSON {
          serverEnabled = true;
          serverPort = 3000;
          serverHost = "127.0.0.1";
          serverServiceDiscoveryDisabled = true;
          logLevel = "info";
          logToFile = false;
          maxFiles = 7;
          storage.cacheDir = "/usr/src/app/store/cache";
          securityKeys = {
            S0_Legacy = zwave-s0-legacy;
            S2_Unauthenticated = zwave-s2-unauthenticated;
            S2_Authenticated = zwave-s2-authenticated;
            S2_AccessControl = zwave-s2-access-control;
          };
          securityKeysLongRange = {
            S2_Authenticated = zwave-lr-s2-authenticated;
            S2_AccessControl = zwave-lr-s2-access-control;
          };
        };
    })
  ]);
}
