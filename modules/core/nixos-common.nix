# Baseline config for every NixOS host: common packages, nix settings, the ngarvey user and SSH.

{ config, lib, pkgs, inputs, ... }:
{
  imports = [
    ./nspawn-cleanup.nix
    ../networking/ipv6-accept-ra-routes.nix
    ../services/vector-agent.nix
    ./time.nix
  ];

  environment.systemPackages = with pkgs; [
    btop
    dig
    dmidecode
    efibootmgr
    file
    ghostty.terminfo  # For terminfo support when SSH'ing from Ghostty
    neovim
    pciutils
    tmux
    unzip
    usbutils
    wget
  ];

  nix.settings = {
    experimental-features = [ "nix-command" "flakes" ];
    # homelab: our celler cache (k8s/manifests/celler), with what the
    # workstations build. Hosts trust only its signing key, not the pushers.
    extra-substituters = [
      "https://cache.nixos-cuda.org"
      "https://celler.home.garvey.sh/homelab"
    ];
    extra-trusted-public-keys = [
      "cache.nixos-cuda.org:74DUi4Ye579gUqzH4ziL9IyiJBlDpMRn9MBN8oNan9M="
      "homelab:6sDcOpt7utnF/hUg3mhEM5J35n+Qh5XTIIckJayzRxw="
    ];
    # celler runs on k3s, whose nodes use this config too, and is reachable
    # only on the LAN: an unreachable cache costs one short timeout, then Nix
    # builds what it cannot substitute.
    connect-timeout = 5;
    fallback = true;
    trusted-users = [ "root" "ngarvey" ];
  };

  environment.variables = {
    EDITOR = "nvim";
    VISUAL = "nvim";
  };

  programs.git = {
    enable = true;
    lfs.enable = true;
  };

  # Keys only: ngarvey has passwordless sudo, so a guessed password would be root.
  services.openssh = {
    enable = true;
    settings = {
      PasswordAuthentication = false;
      KbdInteractiveAuthentication = false;
    };
  };

  # No LLMNR responder or lookups: anyone on the LAN can answer a single-label
  # query, and nothing here resolves single-label names.
  services.resolved.settings.Resolve.LLMNR = "false";

  boot.supportedFilesystems.zfs = false;

  # Cap kept boot generations so /boot doesn't fill up.
  boot.loader.systemd-boot.configurationLimit = 5;

  # Suppress post-quantum key exchange warnings from SSH client
  programs.ssh.extraConfig = "WarnWeakCrypto no-pq-kex";

  nix.optimise.automatic = true;
  nix.gc = {
    automatic = true;
    dates = "weekly";
    options = "--delete-older-than 30d";
  };

  users.users.ngarvey = {
    isNormalUser = true;
    extraGroups = [ "wheel" "networkmanager" ];
    openssh.authorizedKeys.keys = [
      "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCbmansQ84WUYb3frRU8CKPrZb6DdrfnHavtebK6JF5OQdK3C9nK6Xzoz6YKN4zISv7Vx+o7IReJNwjwV6JrUuOrcavBjTvMCgjotdnlYsk9gpuQjDd0MqHD6WdvuDSWxceKbCIP+6AGrVHKJRycFuLkF49f0fnDDy61+w0NWE3t/U1i2yiWOF+SlwvCxlvMYPFYkMWYarmi2Z3MXV1JCIEGwuv7nTQs/o1EEIk9G/YcjhiRMBRvYp6JaTJIXlpVeGpDp9K79VFWCSm6LdQENSWGwrfBeipdq9qRYHulbzTjWtF3LCcYQUm0Z8ZIIhnaqcqIHgFnYMSB79m/XhvKK3T"
    ];
  };
  security.sudo.wheelNeedsPassword = false;
}
