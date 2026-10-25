# OpenBao's configuration (docs/openbao.md). Apply with ./tofu.sh, which supplies
# the state encryption key and an admin token.

terraform {
  required_providers {
    vault = {
      source  = "hashicorp/vault"
      version = "~> 5.0"
    }
  }
}

provider "vault" {
  address = "https://openbao.home.garvey.sh:8200"
  # Use the caller's token as is; the admin token is short-lived already.
  skip_child_token = true
}

# Admin login: a JWT signed with one of the pinned keys (./login.sh). The talos
# key is the user's SSH key; the offline key lives in secrets/openbao-admin.yaml,
# encrypted to the user and the paper key, for when talos is gone.
resource "vault_jwt_auth_backend" "admin" {
  path        = "jwt"
  description = "Admin login with a pinned RSA key"
  # OpenBao stores the keys without their trailing newline.
  jwt_validation_pubkeys = [for f in ["talos-admin", "offline-admin"] : trimspace(file("${path.module}/${f}.pub.pem"))]
  jwt_supported_algs     = ["RS256"]
}

resource "vault_jwt_auth_backend_role" "admin" {
  backend         = vault_jwt_auth_backend.admin.path
  role_name       = "admin"
  role_type       = "jwt"
  user_claim      = "sub"
  bound_subject   = "ngarvey"
  bound_audiences = ["openbao"]
  token_policies  = [vault_policy.admin.name]
  token_ttl       = 3600
  token_max_ttl   = 3600
}

resource "vault_policy" "admin" {
  name   = "admin"
  policy = <<-EOT
    path "*" {
      capabilities = ["create", "read", "update", "patch", "delete", "list", "sudo"]
    }
  EOT
}

# The daily snapshot timer on each node (modules/services/openbao.nix). It logs
# in over the loopback listener, so both the secret ID and the token are bound
# to 127.0.0.1.
resource "vault_policy" "snapshot" {
  name   = "snapshot"
  policy = <<-EOT
    path "sys/storage/raft/snapshot" {
      capabilities = ["read"]
    }
  EOT
}

resource "vault_auth_backend" "approle" {
  type = "approle"
}

resource "vault_approle_auth_backend_role" "snapshot" {
  backend               = vault_auth_backend.approle.path
  role_name             = "snapshot"
  token_policies        = [vault_policy.snapshot.name]
  token_ttl             = 600
  token_max_ttl         = 600
  secret_id_bound_cidrs = ["127.0.0.1/32"]
  token_bound_cidrs     = ["127.0.0.1/32"]
}

# SSH user certificates for deploys and logins (docs/openbao.md). Every NixOS
# host trusts this CA (TrustedUserCAKeys in modules/core/nixos-common.nix); a
# certificate's principal must equal the account it logs in as.
resource "vault_mount" "ssh_user" {
  path        = "ssh-user"
  type        = "ssh"
  description = "SSH user CA"
}

resource "vault_ssh_secret_backend_ca" "user" {
  backend              = vault_mount.ssh_user.path
  generate_signing_key = true
  key_type             = "ed25519"
}

# talos's openbao-deploy key, refreshed hourly by modules/desktop/openbao-ssh-cert.nix.
resource "vault_ssh_secret_backend_role" "ngarvey" {
  backend                 = vault_mount.ssh_user.path
  name                    = "ngarvey"
  key_type                = "ca"
  allow_user_certificates = true
  allowed_users           = "ngarvey"
  default_user            = "ngarvey"
  ttl                     = "28800"
  max_ttl                 = "28800"
  allowed_extensions      = "permit-pty,permit-port-forwarding,permit-agent-forwarding"
  default_extensions = {
    "permit-pty"             = ""
    "permit-port-forwarding" = ""
  }
  key_id_format = "{{role_name}}-{{token_display_name}}"
}

# talos's nix-daemon logging in to the build helpers as nix-ssh
# (modules/desktop/openbao-ssh-cert.nix, modules/core/nix-builder.nix).
resource "vault_ssh_secret_backend_role" "nix_ssh" {
  backend                 = vault_mount.ssh_user.path
  name                    = "nix-ssh"
  key_type                = "ca"
  allow_user_certificates = true
  allowed_users           = "nix-ssh"
  default_user            = "nix-ssh"
  ttl                     = "28800"
  max_ttl                 = "28800"
  allowed_extensions      = ""
  default_extensions      = {}
  key_id_format           = "{{role_name}}-{{token_display_name}}"
}

resource "vault_policy" "talos_builder" {
  name   = "talos-builder"
  policy = <<-EOT
    path "ssh-user/sign/nix-ssh" {
      capabilities = ["update"]
    }
  EOT
}

resource "vault_approle_auth_backend_role" "talos_builder" {
  backend        = vault_auth_backend.approle.path
  role_name      = "talos-builder"
  token_policies = [vault_policy.talos_builder.name]
  token_ttl      = 600
  token_max_ttl  = 600
}

# SSH host certificates for the build helpers, so talos trusts them by CA
# instead of pinned host keys. A separate CA from the user one: host and user
# trust are configured in different places and must not be interchangeable.
resource "vault_mount" "ssh_host" {
  path        = "ssh-host"
  type        = "ssh"
  description = "SSH host CA"
}

resource "vault_ssh_secret_backend_ca" "host" {
  backend              = vault_mount.ssh_host.path
  generate_signing_key = true
  key_type             = "ed25519"
}

resource "vault_ssh_secret_backend_role" "builder_host" {
  backend                 = vault_mount.ssh_host.path
  name                    = "builder-host"
  key_type                = "ca"
  allow_host_certificates = true
  allowed_domains         = "lydia,wabbajack,lydia.home.garvey.sh,wabbajack.home.garvey.sh"
  allow_bare_domains      = true
  allow_subdomains        = false
  ttl                     = "2592000"
  max_ttl                 = "2592000"
  key_id_format           = "{{role_name}}-{{token_display_name}}"
}

resource "vault_policy" "builder_host" {
  name   = "builder-host"
  policy = <<-EOT
    path "ssh-host/sign/builder-host" {
      capabilities = ["update"]
    }
  EOT
}

resource "vault_approle_auth_backend_role" "builder_host" {
  backend        = vault_auth_backend.approle.path
  role_name      = "builder-host"
  token_policies = [vault_policy.builder_host.name]
  token_ttl      = 600
  token_max_ttl  = 600
}

# The cluster CA (docs/openbao.md, "Cluster CA"). cert-manager's cluster-ca
# ClusterIssuer and SPIRE sign through this mount; the CA key (cluster-ca-2026,
# below) was generated here and lives only here, and its certificate from
# pki/root.crt carries the name constraints. Leaves and SPIRE intermediates are
# capped at a year by the mount; the longest cluster certificate is 8760h.
resource "vault_mount" "pki_cluster" {
  path                  = "pki-cluster"
  type                  = "pki"
  description           = "Cluster CA (cert-manager and SPIRE)"
  max_lease_ttl_seconds = 31536000
}

# cert-manager signs every cluster certificate through sign-verbatim: the CSR's
# subject (the agents worker's CN and OU, the postgres roles' CNs), SANs and key
# usages are kept as requested. approver-policy (k8s/manifests/cert-manager/
# cluster-policies.yaml) decides what may be requested; the cluster CA
# certificate's name constraints bound what this role can produce.
resource "vault_pki_secret_backend_role" "cluster_leaf" {
  backend  = vault_mount.pki_cluster.path
  name     = "cluster-leaf"
  ttl      = 86400
  max_ttl  = 31536000
  key_type = "any"
  # Short-lived and never revoked: approver-policy is the control, not CRLs.
  no_store = true
}

resource "vault_pki_secret_backend_config_auto_tidy" "pki_cluster" {
  backend              = vault_mount.pki_cluster.path
  enabled              = true
  interval_duration    = "24h"
  tidy_cert_store      = true
  tidy_revoked_certs   = true
  tidy_expired_issuers = false
  safety_buffer        = "72h"
}

# Service account tokens from the k3s cluster, checked against its signing key
# (./k3s-sa.pub.pem, from kubectl get --raw /openid/v1/jwks) instead of a
# TokenReview, so a login does not depend on the API server. If k3s's
# service.key changes, refresh the PEM and apply.
resource "vault_jwt_auth_backend" "k8s" {
  path                   = "k8s"
  description            = "k3s service account tokens"
  jwt_validation_pubkeys = [trimspace(file("${path.module}/k3s-sa.pub.pem"))]
  jwt_supported_algs     = ["RS256"]
  bound_issuer           = "https://kubernetes.default.svc.cluster.local"
}

resource "vault_policy" "cert_manager" {
  name   = "cert-manager"
  policy = <<-EOT
    path "pki-cluster/sign-verbatim/cluster-leaf" {
      capabilities = ["update"]
    }
  EOT
}

# cert-manager requests a token for this service account (the ClusterIssuer's
# serviceAccountRef) with the extra audience "openbao".
resource "vault_jwt_auth_backend_role" "cert_manager" {
  backend         = vault_jwt_auth_backend.k8s.path
  role_name       = "cert-manager"
  role_type       = "jwt"
  user_claim      = "sub"
  bound_subject   = "system:serviceaccount:cert-manager:openbao-issuer"
  bound_audiences = ["openbao"]
  token_policies  = [vault_policy.cert_manager.name]
  token_ttl       = 600
  token_max_ttl   = 600
}

# SPIRE's vault UpstreamAuthority signs its 24h CA (ca_ttl) here. The parameters
# are exactly what it sends (spire pkg/server/common/vault/client.go): anything
# else, such as a longer TTL, a path length or other names, is refused.
resource "vault_policy" "spire_server" {
  name   = "spire-server"
  policy = <<-EOT
    path "pki-cluster/root/sign-intermediate" {
      capabilities = ["update"]
      allowed_parameters = {
        "csr"          = []
        "common_name"  = ["home.garvey.sh SPIRE CA"]
        "organization" = ["SPIFFE"]
        "country"      = ["US"]
        "uri_sans"     = ["spiffe://home.garvey.sh"]
        "ttl"          = ["86400"]
      }
    }
  EOT
}

resource "vault_jwt_auth_backend_role" "spire_server" {
  backend         = vault_jwt_auth_backend.k8s.path
  role_name       = "spire-server"
  role_type       = "jwt"
  user_claim      = "sub"
  bound_subject   = "system:serviceaccount:spire:spire-server"
  bound_audiences = ["openbao"]
  token_policies  = [vault_policy.spire_server.name]
  token_ttl       = 600
  token_max_ttl   = 600
}

# The hosts CA (docs/openbao.md, "Hosts CA"): ACME for <name>.home.garvey.sh
# outside k8s.home.garvey.sh, 7-day certificates usable as server and client
# certificates. Its key is generated here and never leaves OpenBao; the root
# signs its CSR on talos (pki/dns-ca.ext), and the signed certificate is
# pki/dns-ca.crt.
resource "vault_mount" "pki_dns" {
  path                        = "pki-dns"
  type                        = "pki"
  description                 = "Hosts CA (ACME, home.garvey.sh)"
  max_lease_ttl_seconds       = 604800
  allowed_response_headers    = ["Last-Modified", "Location", "Replay-Nonce", "Link"]
  passthrough_request_headers = ["If-Modified-Since"]
}

resource "vault_pki_secret_backend_intermediate_cert_request" "pki_dns" {
  backend     = vault_mount.pki_dns.path
  type        = "internal"
  common_name = "Garvey Homelab Hosts CA 2026"
  key_type    = "ec"
  key_bits    = 256
  key_name    = "hosts-ca-2026"
}

resource "vault_pki_secret_backend_intermediate_set_signed" "pki_dns" {
  backend     = vault_mount.pki_dns.path
  certificate = "${file("${path.module}/../dns-ca.crt")}${file("${path.module}/../root.crt")}"
}

# ACME needs the mount's public URL.
resource "vault_pki_secret_backend_config_cluster" "pki_dns" {
  backend  = vault_mount.pki_dns.path
  path     = "https://openbao.home.garvey.sh:8200/v1/pki-dns"
  aia_path = "https://openbao.home.garvey.sh:8200/v1/pki-dns"
}

# Every ACME order is issued by this role: a name under home.garvey.sh (the
# hosts CA's constraints remove k8s.home.garvey.sh), usable for both server and
# client authentication, for 7 days.
resource "vault_pki_secret_backend_role" "host" {
  backend            = vault_mount.pki_dns.path
  name               = "host"
  ttl                = 604800
  max_ttl            = 604800
  allowed_domains    = ["home.garvey.sh"]
  allow_subdomains   = true
  allow_bare_domains = false
  allow_ip_sans      = false
  enforce_hostnames  = true
  server_flag        = true
  client_flag        = true
  key_type           = "any"
  key_usage          = ["DigitalSignature", "KeyAgreement", "KeyEncipherment"]
}

# Proof of control is DNS-01 in Knot, checked against Knot itself through
# NAT64; writing a challenge needs that name's TSIG key
# (modules/containers/knot-auth.nix). Without allow_role_ext_key_usage the
# role's clientAuth would be dropped.
resource "vault_pki_secret_backend_config_acme" "pki_dns" {
  backend                  = vault_mount.pki_dns.path
  enabled                  = true
  allowed_issuers          = ["*"]
  allowed_roles            = [vault_pki_secret_backend_role.host.name]
  default_directory_policy = "role:${vault_pki_secret_backend_role.host.name}"
  allow_role_ext_key_usage = true
  dns_resolver             = "[64:ff9b::a1c:6]:53"
  eab_policy               = "not-required"
  depends_on               = [vault_pki_secret_backend_config_cluster.pki_dns, vault_pki_secret_backend_intermediate_set_signed.pki_dns]
}

resource "vault_pki_secret_backend_config_auto_tidy" "pki_dns" {
  backend                    = vault_mount.pki_dns.path
  enabled                    = true
  interval_duration          = "24h"
  tidy_cert_store            = true
  tidy_revoked_certs         = true
  tidy_acme                  = true
  safety_buffer              = "72h"
  acme_account_safety_buffer = "720h"
}

# Hosts log in with their pki-dns certificate (docs/openbao.md, "Hosts CA"):
# the listener keeps its Let's Encrypt certificate and asks for a client
# certificate, which this role checks against the root and authorizes by SAN.
resource "vault_auth_backend" "cert" {
  type = "cert"
}

resource "vault_cert_auth_backend_role" "hosts" {
  backend          = vault_auth_backend.cert.path
  name             = "hosts"
  certificate      = file("${path.module}/../root.crt")
  allowed_dns_sans = ["lydia.home.garvey.sh"]
  token_policies   = ["default"]
  token_ttl        = 300
  token_max_ttl    = 300
}

# The cluster CA's own key, generated here; it never leaves OpenBao. The root
# signs its CSR on talos (pki/cluster-ca.ext); the signed certificate is
# pki/cluster-ca.crt and becomes the mount's default issuer, which both
# cert-manager's sign-verbatim role and SPIRE's sign-intermediate use.
resource "vault_pki_secret_backend_intermediate_cert_request" "pki_cluster" {
  backend     = vault_mount.pki_cluster.path
  type        = "internal"
  common_name = "Garvey Homelab Cluster CA 2026"
  key_type    = "ec"
  key_bits    = 256
  key_name    = "cluster-ca-2026"
}

resource "vault_pki_secret_backend_intermediate_set_signed" "pki_cluster" {
  backend     = vault_mount.pki_cluster.path
  certificate = "${file("${path.module}/../cluster-ca.crt")}${file("${path.module}/../root.crt")}"
}

resource "vault_pki_secret_backend_config_issuers" "pki_cluster" {
  backend                       = vault_mount.pki_cluster.path
  default                       = vault_pki_secret_backend_intermediate_set_signed.pki_cluster.imported_issuers[0]
  default_follows_latest_issuer = false
}
