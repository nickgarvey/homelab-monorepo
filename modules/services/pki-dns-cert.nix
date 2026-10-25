# Keeps this host's <hostname>.home.garvey.sh certificate from OpenBao's hosts CA by ACME (docs/openbao.md, "Hosts CA").
#
# The certificate lasts 7 days and is usable as both a server and a client
# certificate; it chains to pki/root.crt, which only services that verify
# private certificates trust. lego proves control with DNS-01 in Knot, using a
# TSIG key that may write only this host's _acme-challenge name
# (modules/containers/knot-auth.nix), held in secrets/pki-dns-<hostname>.yaml.
{ config, lib, pkgs, ... }:
let
  host = config.networking.hostName;
  name = "${host}.home.garvey.sh";
  certDir = "/var/lib/acme/${name}";
  lan = lib.findFirst (h: h.hostname == host) null (import ../networking/lan-hosts.nix).lanHosts;
  # Knot answers on its LAN IPv4; IPv6-only hosts reach it through NAT64.
  knot = if lan != null && lan.ipv4 != null then "10.28.0.6:53" else "[64:ff9b::a1c:6]:53";

  # The certificate directory also holds the key, so Vector (unprivileged) is
  # kept out of it: root writes the expiry to a file Vector can read.
  expiryFile = "/run/pki-dns-cert/expiry.json";
  writeExpiry = pkgs.writeShellApplication {
    name = "pki-dns-cert-write-expiry";
    runtimeInputs = with pkgs; [ openssl coreutils ];
    text = ''
      end=$(openssl x509 -in ${certDir}/cert.pem -noout -enddate 2>/dev/null | cut -d= -f2 || true)
      exp=0; [ -n "$end" ] && exp=$(date -d "$end" +%s)
      echo "{\"expiry\":$exp}" > ${expiryFile}.tmp
      mv ${expiryFile}.tmp ${expiryFile}
    '';
  };

  probe = pkgs.writeShellApplication {
    name = "pki-dns-cert-probe";
    text = ''
      cat ${expiryFile} 2>/dev/null || echo '{"expiry":0}'
    '';
  };
in
{
  sops.secrets.pki-dns-acme-tsig = {
    sopsFile = ../../secrets/pki-dns-${host}.yaml;
    key = "acme_tsig_secret";
  };
  sops.templates."pki-dns-acme.env".content = ''
    RFC2136_NAMESERVER=${knot}
    RFC2136_TSIG_KEY=acme-pki-dns-${host}
    RFC2136_TSIG_ALGORITHM=hmac-sha256.
    RFC2136_TSIG_SECRET=${config.sops.placeholder.pki-dns-acme-tsig}
  '';

  security.acme = {
    acceptTerms = true;
    defaults.email = "garvey.nick@gmail.com";
    certs.${name} = {
      server = "https://openbao.home.garvey.sh:8200/v1/pki-dns/acme/directory";
      dnsProvider = "rfc2136";
      dnsResolver = knot;
      environmentFile = config.sops.templates."pki-dns-acme.env".path;
      # Knot is the only authoritative server and commits the update before
      # lego continues; lego's own check would go to the published NS, which
      # the LAN cannot reach (docs/tls.md).
      dnsPropagationCheck = false;
      keyType = "ec256";
      # Renew with 3 of the 7 days left, so an OpenBao outage of a few days
      # does not let it lapse.
      validMinDays = 3;
      postRun = "systemctl --no-block start pki-dns-cert-expiry.service";
    };
  };

  # Also runs at boot and on deploy: postRun fires only when a new certificate
  # is issued.
  systemd.services.pki-dns-cert-expiry = {
    description = "Write the expiry of ${name} for Vector";
    wantedBy = [ "multi-user.target" ];
    after = [ "acme-${name}.service" ];
    serviceConfig = {
      Type = "oneshot";
      ExecStart = lib.getExe writeExpiry;
      RuntimeDirectory = "pki-dns-cert";
      RuntimeDirectoryMode = "0755";
      RuntimeDirectoryPreserve = true;
    };
  };

  # Expiry pushed to Prometheus (k8s/manifests/prometheus/rules/openbao.yaml).
  homelab.metrics.sources.pki_dns_cert = {
    type = "exec";
    mode = "scheduled";
    scheduled.exec_interval_secs = 180;
    command = [ (lib.getExe probe) ];
  };
  homelab.metrics.transforms.pki_dns_cert_fields = {
    type = "remap";
    inputs = [ "pki_dns_cert" ];
    source = ". = parse_json!(strip_whitespace!(to_string!(.message)))";
  };
  homelab.metrics.transforms.pki_dns_cert_metric = {
    type = "log_to_metric";
    inputs = [ "pki_dns_cert_fields" ];
    metrics = [{
      type = "gauge";
      field = "expiry";
      name = "pki_dns_cert_expiry_timestamp_seconds";
      tags.hostname = host;
    }];
  };
}
