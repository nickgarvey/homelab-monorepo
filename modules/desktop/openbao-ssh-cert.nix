# talos's OpenBao-signed SSH certificates: the user's deploy certificate, and nix-daemon's nix-ssh certificate for the build helpers (docs/openbao.md).
#
# The deploy key is in no authorized_keys anywhere: it only gets in with a
# valid certificate. ~/.ssh/id_rsa is offered after it, and is the break-glass
# key when OpenBao is unreachable and the certificate has expired.
#
# The key lives outside ~/.ssh on purpose: gcr-ssh-agent (the session agent)
# loads every ~/.ssh key that has a .pub beside it, certificate included, and the
# agent would keep each expired certificate as one more identity to offer first,
# until sshd's MaxAuthTries ran out.
{ config, lib, pkgs, ... }:
let
  addr = "https://openbao.home.garvey.sh:8200";
  builderKey = "/var/lib/nix-builder-ssh/id_ed25519";
  # nix-daemon logs in to the build helpers (hosts/talos/configuration.nix) as
  # nix-ssh with this key and an 8-hour certificate; the helpers have no
  # authorized key for it. Logs in with the talos-builder AppRole.
  refreshBuilder = pkgs.writeShellApplication {
    name = "openbao-builder-cert";
    runtimeInputs = with pkgs; [ openssh curl jq coreutils gnused ];
    text = ''
      key=${builderKey}
      [ -f "$key" ] || ssh-keygen -q -t ed25519 -N "" -C "nix-daemon@talos" -f "$key"
      if [ -f "$key-cert.pub" ]; then
        end=$(ssh-keygen -L -f "$key-cert.pub" | sed -n 's/^ *Valid: from .* to //p')
        if [ -n "$end" ] && [ $(( $(date -d "$end" +%s) - $(date +%s) )) -gt 7200 ]; then
          exit 0
        fi
      fi
      c=$CREDENTIALS_DIRECTORY
      token=$(jq -n --rawfile r "$c/role-id" --rawfile s "$c/secret-id" \
          '{role_id: ($r | rtrimstr("\n")), secret_id: ($s | rtrimstr("\n"))}' \
        | curl -fsS --data @- ${addr}/v1/auth/approle/login | jq -er .auth.client_token)
      jq -n --arg k "$(cat "$key.pub")" '{public_key: $k, valid_principals: "nix-ssh"}' \
        | curl -fsS -H "X-Vault-Token: $token" --data @- ${addr}/v1/ssh-user/sign/nix-ssh \
        | jq -er '.data.signed_key | rtrimstr("\n")' > "$key-cert.pub.tmp"
      mv "$key-cert.pub.tmp" "$key-cert.pub"
      curl -fsS -X POST -H "X-Vault-Token: $token" ${addr}/v1/auth/token/revoke-self >/dev/null || true
    '';
  };
  refresh = pkgs.writeShellApplication {
    name = "openbao-ssh-cert";
    runtimeInputs = with pkgs; [ bash openssh openssl curl jq coreutils gnused ];
    text = ''
      dir=$HOME/.local/state/openbao-ssh
      key=$dir/deploy
      install -d -m 700 "$dir"
      [ -f "$key" ] || ssh-keygen -q -t ed25519 -N "" -C "openbao-deploy" -f "$key"
      # Certificates last 8 hours (the ngarvey role, pki/openbao/main.tf); renew
      # once fewer than 2 remain.
      if [ -f "$key-cert.pub" ]; then
        end=$(ssh-keygen -L -f "$key-cert.pub" | sed -n 's/^ *Valid: from .* to //p')
        if [ -n "$end" ] && [ $(( $(date -d "$end" +%s) - $(date +%s) )) -gt 7200 ]; then
          exit 0
        fi
      fi
      token=$(BAO_ADDR=${addr} bash ${../../pki/openbao/login.sh})
      jq -n --arg k "$(cat "$key.pub")" '{public_key: $k, valid_principals: "ngarvey"}' \
        | curl -fsS -H "X-Vault-Token: $token" --data @- ${addr}/v1/ssh-user/sign/ngarvey \
        | jq -er '.data.signed_key | rtrimstr("\n")' > "$key-cert.pub.tmp"
      mv "$key-cert.pub.tmp" "$key-cert.pub"
      curl -fsS -X POST -H "X-Vault-Token: $token" ${addr}/v1/auth/token/revoke-self >/dev/null || true
    '';
  };
in
{
  systemd.user.services.openbao-ssh-cert = {
    description = "Refresh the OpenBao-signed SSH certificate";
    unitConfig.ConditionUser = "ngarvey";
    serviceConfig = {
      Type = "oneshot";
      ExecStart = lib.getExe refresh;
    };
  };
  systemd.user.timers.openbao-ssh-cert = {
    wantedBy = [ "timers.target" ];
    unitConfig.ConditionUser = "ngarvey";
    timerConfig = {
      OnStartupSec = "1min";
      OnUnitActiveSec = "1h";
    };
  };

  sops.secrets = lib.mapAttrs (_: key: {
    sopsFile = ../../secrets/openbao-talos-builder.yaml;
    inherit key;
  }) {
    openbao-talos-builder-role-id = "role_id";
    openbao-talos-builder-secret-id = "secret_id";
  };
  systemd.services.openbao-builder-cert = {
    description = "Refresh nix-daemon's OpenBao-signed SSH certificate for the build helpers";
    wantedBy = [ "multi-user.target" ];
    after = [ "network-online.target" ];
    wants = [ "network-online.target" ];
    serviceConfig = {
      Type = "oneshot";
      ExecStart = lib.getExe refreshBuilder;
      StateDirectory = "nix-builder-ssh";
      StateDirectoryMode = "0700";
      LoadCredential = [
        "role-id:${config.sops.secrets.openbao-talos-builder-role-id.path}"
        "secret-id:${config.sops.secrets.openbao-talos-builder-secret-id.path}"
      ];
    };
  };
  systemd.timers.openbao-builder-cert = {
    wantedBy = [ "timers.target" ];
    timerConfig = { OnBootSec = "1min"; OnUnitActiveSec = "1h"; };
  };

  # The build helpers present host certificates from the OpenBao host CA
  # (modules/core/nix-builder.nix), so they are trusted by CA, not pinned keys.
  programs.ssh.knownHosts.build-helpers = {
    certAuthority = true;
    hostNames = [ "lydia" "wabbajack" "lydia.home.garvey.sh" "wabbajack.home.garvey.sh" ];
    publicKeyFile = ../../pki/openbao/ssh-host-ca.pub;
  };

  # Naming any IdentityFile drops ssh's defaults, so id_rsa is listed again.
  # The certificate is picked up from deploy-cert.pub beside the key automatically.
  programs.ssh.extraConfig = ''
    Match localuser ngarvey
      IdentityFile ~/.local/state/openbao-ssh/deploy
      IdentityFile ~/.ssh/id_rsa
  '';
}
