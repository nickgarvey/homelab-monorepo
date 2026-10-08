# Pushes every store path this workstation builds to the homelab celler cache (k8s/manifests/celler) with `celler watch-store`.
#
# Each workstation has its own push-only token in secrets/celler-push.yaml,
# keyed by host name. Tokens are minted offline with the signing key in
# secrets/celler-admin.yaml (k8s/manifests/celler/README.md, "Push tokens") and
# expire after a year. Paths signed by cache.nixos.org or the CUDA cache are not
# uploaded. Deploys build here, so the hosts they target then substitute from
# the cache (modules/core/nixos-common.nix).
#
# The service has its own client config, read-only from the store, so it does
# not touch ~/.config/celler (where `celler login` keeps an admin token).
# Away from the LAN the pushes fail and the service retries; paths built in the
# meantime are not pushed later.
{ config, lib, pkgs, inputs, ... }:

let
  celler = inputs.self.packages.${pkgs.stdenv.hostPlatform.system}.celler;
  token = "/run/credentials/celler-watch-store.service/token";
  clientConfig = pkgs.writeTextDir "celler/config.toml" ''
    default-server = "homelab"

    [servers.homelab]
    endpoint = "https://celler.home.garvey.sh/"
    token-file = "${token}"
  '';
in
{
  sops.secrets.celler-push-token = {
    sopsFile = ../../secrets/celler-push.yaml;
    key = config.networking.hostName;
  };

  # For pushing by hand (`celler push homelab <path>`) with the admin login.
  environment.systemPackages = [ celler ];

  systemd.services.celler-watch-store = {
    description = "Push new store paths to the homelab celler cache";
    wantedBy = [ "multi-user.target" ];
    wants = [ "network-online.target" ];
    after = [ "network-online.target" ];
    environment = {
      XDG_CONFIG_HOME = "${clientConfig}";
      HOME = "/run/celler-watch-store";
    };
    serviceConfig = {
      ExecStart = "${lib.getExe' celler "celler"} watch-store homelab";
      DynamicUser = true;
      RuntimeDirectory = "celler-watch-store";
      LoadCredential = "token:${config.sops.secrets.celler-push-token.path}";
      Restart = "always";
      RestartSec = 30;
    };
  };
}
