# Runs the flake-bot worker (packages/flake-bot): daily `nix flake update` onto dev and a build of every dev commit, pushed to celler and published for hosts to pull (ait nix-b77Zn).
#
# The worker is a Temporal worker reached through the frontend's LAN
# LoadBalancer. Its identity is the SPIRE agent's SVID for this unit,
# spiffe://home.garvey.sh/host/<host>/flake-bot (selector
# systemd:id:flake-bot.service in k8s/manifests/spire/host-entries.yaml): the
# X.509-SVID for Temporal, the JWT-SVID for RGW. Its two stored credentials,
# the GitHub App key and the celler push token, are systemd credentials.
#
# Builds go through the nix daemon as the flake-bot user, so they take this
# host's builders and its binary caches; its clone, worktrees and build logs
# are in /var/lib/flake-bot.
{ config, lib, pkgs, inputs, ... }:

let
  system = pkgs.stdenv.hostPlatform.system;
  flake-bot = inputs.self.packages.${system}.flake-bot;
  celler = inputs.self.packages.${system}.celler;
  creds = "/run/credentials/flake-bot.service";
  cellerConfig = pkgs.writeTextDir "celler/config.toml" ''
    default-server = "homelab"

    [servers.homelab]
    endpoint = "https://celler.home.garvey.sh/"
    token-file = "${creds}/celler-token"
  '';
in
{
  assertions = [{
    assertion = config.services.spire.agent.enable;
    message = "flake-bot.nix needs modules/services/spire-agent.nix.";
  }];

  sops.secrets.flake-bot-github-app-key = {
    sopsFile = ../../secrets/flake-bot.yaml;
    key = "github_app_private_key";
  };
  sops.secrets.flake-bot-celler-token = {
    sopsFile = ../../secrets/celler-push.yaml;
    key = config.networking.hostName;
  };

  users.users.flake-bot = {
    isSystemUser = true;
    group = "flake-bot";
    home = "/var/lib/flake-bot";
  };
  users.groups.flake-bot = { };

  systemd.services.flake-bot = {
    description = "flake-bot Temporal worker: flake updates and dev builds";
    # git for nix, should a flake input be fetched through the git CLI.
    path = [ pkgs.git ];
    wantedBy = [ "multi-user.target" ];
    wants = [ "network-online.target" "spire-agent.service" ];
    after = [ "network-online.target" "spire-agent.service" "nix-daemon.socket" ];
    environment = {
      HOME = "/var/lib/flake-bot";
      # nix flake update and nix build write their caches here.
      XDG_CACHE_HOME = "/var/lib/flake-bot/cache";
    };
    serviceConfig = {
      ExecStart = lib.escapeShellArgs [
        (lib.getExe flake-bot)
        "--github-app-key" "${creds}/github-app-key"
        "--celler-config-home" "${cellerConfig}"
        "--spire-socket" "unix://${config.services.spire.agent.settings.agent.socket_path}"
        "--git" (lib.getExe pkgs.git)
        "--nix" (lib.getExe config.nix.package)
        "--celler" (lib.getExe' celler "celler")
        "--aws" (lib.getExe pkgs.awscli2)
        "--system" system
      ];
      User = "flake-bot";
      Group = "flake-bot";
      StateDirectory = "flake-bot";
      StateDirectoryMode = "0750";
      LoadCredential = [
        "github-app-key:${config.sops.secrets.flake-bot-github-app-key.path}"
        "celler-token:${config.sops.secrets.flake-bot-celler-token.path}"
      ];
      Restart = "always";
      RestartSec = 30;
      # Evaluation is the worker's own work; builds run in the daemon.
      Nice = 10;
      NoNewPrivileges = true;
      PrivateTmp = true;
      ProtectHome = true;
    };
  };
}
