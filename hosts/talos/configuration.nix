{ config, pkgs, ... }:

{
  imports = [
    ./hardware-configuration.nix
    ../../modules/desktop/common-workstation.nix
    ../../modules/desktop/niri.nix
    ../../modules/desktop/openscad.nix
    ../../modules/desktop/openbao-ssh-cert.nix
    ../../modules/networking/network-manager.nix
  ];

  networking = {
    hostName = "talos";
    hostId = "a4c946db";
  };

  boot = {
    loader.systemd-boot.enable = true;
    loader.efi.canTouchEfiVariables = true;

    kernelModules = [ "nvidia" "nvidia_drm" "nvidia_uvm" "nvidia_modeset" ];

    # PCIe ASPM leaves the RTX 5090's link in a low-power state the driver
    # trips over; keep the link out of L0s/L1 entirely.
    kernelParams = [ "pcie_aspm=off" ];

    binfmt.emulatedSystems = [ "aarch64-linux" ];
  };

  hardware.nvidia = {
    modesetting.enable = true;
    powerManagement.enable = true;
    powerManagement.finegrained = false;
    open = true;
    nvidiaSettings = true;
    package = config.boot.kernelPackages.nvidiaPackages.stable;
  };

  # Exposes the GPU to containers as the CDI device `nvidia.com/gpu=all`
  # (`docker run --device=nvidia.com/gpu=all ...`). `--gpus all` does not work:
  # the NixOS module wires CDI only, it installs no nvidia OCI runtime.
  hardware.nvidia-container-toolkit.enable = true;

  # The RTX 5090 drives the display. xserver itself stays off (niri is a
  # Wayland compositor) — this only selects the DRM driver.
  services.xserver.videoDrivers = [ "nvidia" ];

  # The local ninfer llama server runs here (RTX 5090), so pi is given its
  # provider and can be pointed at it. DeepSeek stays the default model.
  homelab.pi.ninfer.enable = true;

  # ~/local-drive on CephFS (modules/services/cephfs-mount.nix).
  homelab.cephfs.enable = true;

  homelab.niri.outputs = ''
    output "ASUSTek COMPUTER INC XG27UQDMS W3LMAV000673" {
        mode "3840x2160@240.000"
        scale 1.5
        position x=0 y=0
        variable-refresh-rate
    }
  '';

  # talos builds every host's closure, some from source (ceph, vector). With
  # no swap, a build that outgrows RAM thrashes the whole machine into a hang
  # rather than being OOM-killed. MemoryMax confines the OOM kill to the
  # builds and leaves the desktop ~15 GB. With 24 cores per build, ceph and
  # vector compiling side by side peak right at the cap without an OOM kill.
  # max-jobs stays at 32, so many small derivations still run in parallel.
  systemd.services.nix-daemon.serviceConfig.MemoryMax = "75%";
  nix.settings.cores = 24;

  # Build helpers (modules/core/nix-builder.nix, nix-oc1lr). They add capacity
  # but are never required: talos keeps its own build slots and every
  # system-feature, so a helper being down only makes builds slower. The
  # daemon logs in with an OpenBao-signed certificate and trusts the helpers by
  # their host certificates (docs/openbao.md). Helpers fetch from the caches
  # themselves (builders-use-substitutes) instead of receiving inputs from talos.
  nix.distributedBuilds = true;
  nix.settings.builders-use-substitutes = true;
  nix.buildMachines = [
    {
      # The main helper: 32 threads, 125 GB. The highest speedFactor makes it
      # the first choice for Ceph and kernels; binfmt lets it build aarch64.
      hostName = "wabbajack";
      systems = [ "x86_64-linux" "aarch64-linux" ];
      protocol = "ssh-ng";
      sshUser = "nix-ssh";
      # Signed as nix-ssh by OpenBao (modules/desktop/openbao-ssh-cert.nix).
      sshKey = "/var/lib/nix-builder-ssh/id_ed25519";
      maxJobs = 2;
      speedFactor = 4;
      supportedFeatures = [ "big-parallel" "nixos-test" "benchmark" "kvm" ];
    }
    {
      hostName = "lydia";
      systems = [ "x86_64-linux" ];
      protocol = "ssh-ng";
      sshUser = "nix-ssh";
      # Signed as nix-ssh by OpenBao (modules/desktop/openbao-ssh-cert.nix).
      sshKey = "/var/lib/nix-builder-ssh/id_ed25519";
      maxJobs = 2;
      speedFactor = 1;
      supportedFeatures = [ "big-parallel" "nixos-test" "benchmark" "kvm" ];
    }
  ];
  # An unreachable helper costs ~5s before Nix moves on; one that dies
  # mid-build is noticed in ~30s rather than at TCP's timeout.
  programs.ssh.extraConfig = ''
    Host lydia wabbajack
      ConnectTimeout 5
      ServerAliveInterval 15
      ServerAliveCountMax 2
  '';

  users.users.ngarvey.packages = with pkgs; [
    rsync
  ];

  system.stateVersion = "25.11";
}
