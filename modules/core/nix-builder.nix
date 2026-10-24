# Lets talos offload Nix builds to this host over ssh-ng, with the builds held to a share of its memory and CPU.
#
# talos is the only client: nix.buildMachines in hosts/talos/configuration.nix
# lists the hosts that import this. Builds are optional capacity there. talos
# can still build everything itself, so this host being down only makes a
# build slower (nix-oc1lr).
#
# talos's nix-daemon logs in with talos's SSH host key, so there is no
# separate builder secret. nix.sshServe creates the nix-ssh user, which can
# only speak the Nix store protocol. `trusted` lets it import the unsigned
# derivations talos sends, which a remote builder needs.
#
# The builds run in this host's nix-daemon.service cgroup (nix-daemon --stdio
# hands them to the main daemon). The limits there keep a large build, such
# as Ceph's ~2.5 GB-per-compiler-process OSD sources, from pushing out the
# host's own services. No substituter is served from here: clients reach
# celler and cache.nixos.org directly.
{ config, lib, ... }:
let
  cfg = config.homelab.nixBuilder;
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
      keys = [
        # talos: /etc/ssh/ssh_host_ed25519_key.pub
        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDWd1AMSCeWjCbB77gtV4D3NRT5oYsC//15+vhf8KWe1 root@talos"
      ];
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
