# Kernel netconsole: printk sent as UDP to the netconsole receiver on k3s, so the last messages before a hang or panic are kept off the host.
#
# The receiver is k8s/manifests/netconsole/. Its network policy admits the
# host's static IPv6 from lan-hosts.nix, which is why
# netconsole sends from that address rather than whatever source the kernel
# would pick.
#
# netconsole does no neighbour discovery: it needs the next hop's MAC. LB IPs
# are reached through the router, so the unit looks the router up at start
# (route to the target, then its neighbour entry) instead of hardcoding it.

{ config, lib, pkgs, ... }:

let
  inherit (import ../networking/lan-hosts.nix) lanHosts;
  hostEntry = lib.findFirst (h: h.hostname == config.networking.hostName) null lanHosts;
  localIP = if hostEntry == null then null else hostEntry.ipv6;

  # Service in k8s/manifests/netconsole/service.yaml.
  remoteIP = "2001:470:482f:2::5015";
  remotePort = 6666;

  target = "/sys/kernel/config/netconsole/lan";
in
{
  assertions = [{
    assertion = localIP != null;
    message = "netconsole: ${config.networking.hostName} needs a static ipv6 in modules/networking/lan-hosts.nix";
  }];

  boot.kernelModules = [ "netconsole" ];

  # netconsole only gets messages at or above the console loglevel. The default
  # (4) drops warnings, which is where hung-task and RCU-stall reports start.
  boot.consoleLogLevel = 7;

  systemd.services.netconsole = {
    description = "Kernel netconsole to ${remoteIP}";
    wantedBy = [ "multi-user.target" ];
    wants = [ "network-online.target" ];
    after = [ "network-online.target" "sys-kernel-config.mount" ];
    path = [ pkgs.iproute2 pkgs.iputils pkgs.gawk ];
    serviceConfig = {
      Type = "oneshot";
      RemainAfterExit = true;
      Restart = "on-failure";
      RestartSec = 30;
    };
    script = ''
      set -euo pipefail

      route=$(ip -6 route get ${remoteIP} from ${localIP})
      dev=$(awk '{ for (i = 1; i < NF; i++) if ($i == "dev") print $(i + 1) }' <<<"$route")
      gw=$(awk '{ for (i = 1; i < NF; i++) if ($i == "via") print $(i + 1) }' <<<"$route")
      # On-link target (no gateway): the target itself is the next hop.
      hop=''${gw:-${remoteIP}}
      # A link-local gateway needs its interface named.
      if [ -n "$gw" ]; then ping_hop="$gw%$dev"; else ping_hop=$hop; fi

      mac=""
      for _ in 1 2 3 4 5; do
        ping -6 -c1 -W1 "$ping_hop" >/dev/null 2>&1 || true
        mac=$(ip -6 neigh show "$hop" dev "$dev" | awk '{ for (i = 1; i < NF; i++) if ($i == "lladdr") print $(i + 1) }')
        [ -n "$mac" ] && break
        sleep 1
      done
      if [ -z "$mac" ]; then
        echo "no MAC for next hop $hop on $dev" >&2
        exit 1
      fi

      # Disabling an already disabled target is EINVAL.
      if [ -d ${target} ]; then
        if [ "$(cat ${target}/enabled)" = 1 ]; then echo 0 > ${target}/enabled; fi
      else
        mkdir ${target}
      fi
      echo "$dev" > ${target}/dev_name
      echo ${localIP} > ${target}/local_ip
      echo ${remoteIP} > ${target}/remote_ip
      echo ${toString remotePort} > ${target}/remote_port
      echo "$mac" > ${target}/remote_mac
      echo 1 > ${target}/extended
      # Each message names its sender, a " host=<name>" line after the text, so
      # the receiver files logs by hostname rather than address.
      mkdir -p ${target}/userdata/host
      echo ${config.networking.hostName} > ${target}/userdata/host/value
      echo 1 > ${target}/enabled
      echo "netconsole: $dev ${localIP} -> [${remoteIP}]:${toString remotePort} via $hop ($mac)"
    '';
    preStop = ''
      if [ -d ${target} ]; then
        if [ "$(cat ${target}/enabled)" = 1 ]; then echo 0 > ${target}/enabled; fi
        rmdir ${target}/userdata/host 2>/dev/null || true
        rmdir ${target}
      fi
    '';
  };
}
