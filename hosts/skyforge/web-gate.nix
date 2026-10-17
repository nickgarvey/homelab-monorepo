# nginx in front of Mainsail, Moonraker and the webcam: TLS on 443, and an Authelia login or an API key for everything except the webcam.
#
# Who gets in, decided per request by the internal authz location below:
#   - A request with no X-Api-Key header (browsers, the phone) goes to Authelia
#     forward-auth (auth.home.garvey.sh, in k3s), policy two_factor
#     (k8s/manifests/authelia/configmap.yaml). Without a session it is
#     redirected to the Authelia portal.
#   - A request whose X-Api-Key matches secrets/skyforge-orca.yaml (OrcaSlicer,
#     which can send nothing else) is let through without Authelia.
#   - A request with any other X-Api-Key gets 403 and never reaches Authelia.
#   - /webcam/stream and /webcam/snapshot are open: a read-only stream on the
#     LAN is accepted.
#   - /sw.js is open and serves ./sw-unregister.js, not Mainsail's service
#     worker. Mainsail's worker serves the cached page without asking nginx, so
#     after the Authelia session expires the page never reaches the login
#     redirect and sits at "connecting" while its websocket gets 302s.
# If k3s or Authelia is down, Mainsail is unreachable from a browser; the API-key
# path, and so printing from OrcaSlicer, does not depend on it.
#
# To rotate the API key: `sops secrets/skyforge-orca.yaml`, then deploy skyforge
# and the workstations that install it into OrcaSlicer.
{ config, ... }:

let
  domain = "skyforge.home.garvey.sh";
  # Knot (knot-auth container on dragonsreach) takes the RFC2136 challenge
  # updates; skyforge has IPv4 on the same LAN.
  knot = "10.28.0.6:53";
  autheliaAuthz = "https://auth.home.garvey.sh/api/authz/auth-request";
  ustreamer = "http://[::1]:8080";
  stripClientAddr = ''
    proxy_set_header X-Real-IP "";
    proxy_set_header X-Forwarded-For "";
  '';
in
{
  # DNS-01 against our own Knot, as in modules/containers/garage.nix. The TSIG
  # key may only write TXT records at _acme-challenge.skyforge
  # (modules/containers/knot-auth.nix).
  sops.secrets.skyforge-acme-tsig = {
    sopsFile = ../../secrets/skyforge-acme.yaml;
    key = "acme_tsig_secret";
  };
  sops.templates."skyforge-acme.env".content = ''
    RFC2136_NAMESERVER=${knot}
    RFC2136_TSIG_KEY=acme-skyforge
    RFC2136_TSIG_ALGORITHM=hmac-sha256.
    RFC2136_TSIG_SECRET=${config.sops.placeholder.skyforge-acme-tsig}
  '';
  security.acme = {
    acceptTerms = true;
    defaults.email = "garvey.nick@gmail.com";
    certs.${domain} = {
      dnsProvider = "rfc2136";
      dnsResolver = knot;
      environmentFile = config.sops.templates."skyforge-acme.env".path;
      # Same reason as garage.nix: lego's propagation check queries the zone's
      # NS via blocky, whose negative cache outlasts its timeout; Knot is the
      # only authoritative server, so there is nothing to wait for.
      dnsPropagationCheck = false;
      group = config.services.nginx.group;
      reloadServices = [ "nginx.service" ];
    };
  };

  # The key goes into nginx through an include rendered at activation, so it
  # never lands in the Nix store.
  sops.secrets.skyforge-orca-key = {
    sopsFile = ../../secrets/skyforge-orca.yaml;
    key = "orca_api_key";
  };
  sops.templates."skyforge-orca-key.conf" = {
    owner = config.services.nginx.user;
    restartUnits = [ "nginx.service" ];
    content = ''
      map $http_x_api_key $skyforge_api_key {
        default denied;
        "" absent;
        "${config.sops.placeholder.skyforge-orca-key}" valid;
      }
    '';
  };

  services.nginx = {
    # Moonraker trusts its peer, nginx on ::1, but takes X-Real-IP or
    # X-Forwarded-For as the client address when present (Tornado xheaders).
    # Strip both so the address it checks is always nginx's. Set per location,
    # because the proxyWebsockets headers on the mainsail module's locations
    # stop server-level proxy_set_header from being inherited; and here rather
    # than in services.mainsail.nginx, which would redefine those locations.
    virtualHosts.${domain}.locations = {
      "/websocket".extraConfig = stripClientAddr;
      "~ ^/(printer|api|access|machine|server)/".extraConfig = stripClientAddr;
    };

    # The key is 64 hex characters, which does not fit the default 64-byte
    # map hash bucket.
    mapHashBucketSize = 128;
    appendHttpConfig = ''
      include ${config.sops.templates."skyforge-orca-key.conf".path};
    '';

    # Port 80: redirect to https. Also the default server, so a request for the
    # bare `skyforge` name lands here.
    virtualHosts."${domain}-http" = {
      serverName = domain;
      serverAliases = [ "skyforge" ];
      default = true;
      locations."/".return = "301 https://${domain}$request_uri";
    };
  };

  # The mainsail module builds this vhost from services.mainsail.nginx plus its
  # own locations; this adds TLS and the gate. auth_request at server level
  # covers every location that does not turn it off.
  services.mainsail.nginx = {
    onlySSL = true;
    useACMEHost = domain;
    extraConfig = ''
      auth_request /internal/authelia/authz;
      auth_request_set $redirection_url $upstream_http_location;
      error_page 401 =302 $redirection_url;
    '';
    locations = {
      # Authelia's nginx integration for the AuthRequest endpoint, with the
      # API-key decision in front of it. Authelia answers 200, or 401 with the
      # portal URL in Location.
      "/internal/authelia/authz".extraConfig = ''
        internal;
        auth_request off;
        if ($skyforge_api_key = valid) { return 204; }
        if ($skyforge_api_key = denied) { return 403; }

        # A variable in proxy_pass makes nginx resolve the name per request
        # rather than once at startup, so nginx still starts when DNS is not
        # up yet.
        resolver 127.0.0.53 valid=60s;
        set $authelia_authz ${autheliaAuthz};
        proxy_pass $authelia_authz;
        proxy_ssl_server_name on;
        proxy_ssl_verify on;
        # The default depth of 1 stops short of Let's Encrypt's root.
        proxy_ssl_verify_depth 3;
        proxy_ssl_trusted_certificate /etc/ssl/certs/ca-certificates.crt;
        proxy_http_version 1.1;
        proxy_set_header X-Original-Method $request_method;
        proxy_set_header X-Original-URL $scheme://$http_host$request_uri;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header Content-Length "";
        proxy_set_header Connection "";
        proxy_pass_request_body off;
      '';

      "= /webcam/stream" = {
        proxyPass = "${ustreamer}/stream";
        extraConfig = ''
          auth_request off;
          proxy_buffering off;
          proxy_read_timeout 1h;
        '';
      };
      "= /sw.js" = {
        alias = ./sw-unregister.js;
        extraConfig = ''
          auth_request off;
          add_header Cache-Control "no-store";
        '';
      };
      "= /webcam/snapshot" = {
        proxyPass = "${ustreamer}/snapshot";
        extraConfig = "auth_request off;";
      };
    };
  };
}
