# SPIRE agent for host processes: attests to the in-cluster server with this host's pki-dns certificate (x509pop) and serves the Workload API on /run/spire/agent/public/api.sock.
#
# The server is spire-server in k3s, reached at spire.home.garvey.sh:443
# (k8s/manifests/spire/README.md, "NixOS hosts"). The agent verifies it by its
# SVID against pki/root.crt. The server derives the agent ID from the
# certificate CN, spiffe://home.garvey.sh/spire/agent/x509pop/<host>.home.garvey.sh,
# which stays the same across the weekly renewals; registration entries for
# this host's services are ClusterStaticEntries parented to it
# (k8s/manifests/spire/host-entries.yaml). Services are told apart by their
# systemd unit (selector systemd:id:<unit>), which also covers DynamicUser
# services such as Vector.
#
# Needs modules/services/pki-dns-cert.nix on the same host.
{ config, lib, ... }:
let
  name = "${config.networking.hostName}.home.garvey.sh";
  certDir = "/var/lib/acme/${name}";
in
{
  assertions = [{
    assertion = config.security.acme.certs ? ${name};
    message = "spire-agent.nix needs modules/services/pki-dns-cert.nix for ${name}.";
  }];

  services.spire.agent = {
    enable = true;
    settings = {
      agent = {
        trust_domain = "home.garvey.sh";
        server_address = "spire.home.garvey.sh";
        server_port = 443;
        trust_bundle_path = "${../../pki/root.crt}";
      };
      plugins = {
        # On disk, so a restart reuses the agent SVID instead of re-attesting.
        KeyManager.disk.plugin_data.directory = "$STATE_DIRECTORY";
        # cert.pem is the full chain (leaf, then the hosts CA). Both files are
        # credentials copied by systemd at start; the certificate directory
        # stays root/acme only.
        NodeAttestor.x509pop.plugin_data = {
          private_key_path = "$CREDENTIALS_DIRECTORY/key.pem";
          certificate_path = "$CREDENTIALS_DIRECTORY/cert.pem";
        };
        WorkloadAttestor.systemd.plugin_data = { };
      };
    };
  };

  systemd.services.spire-agent = {
    wants = [ "network-online.target" "acme-${name}.service" ];
    after = [ "network-online.target" "acme-${name}.service" ];
    serviceConfig = {
      LoadCredential = [
        "key.pem:${certDir}/key.pem"
        "cert.pem:${certDir}/cert.pem"
      ];
      # Keep retrying while k3s or the network is down.
      RestartSec = 10;
    };
    unitConfig.StartLimitIntervalSec = 0;
  };

  # The credentials are copies taken at start, so a renewal restarts the agent
  # to pick up the new certificate for its next re-attestation.
  security.acme.certs.${name}.reloadServices = [ "spire-agent.service" ];
}
