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
  path                   = "jwt"
  description            = "Admin login with a pinned RSA key"
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
