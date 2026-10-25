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
