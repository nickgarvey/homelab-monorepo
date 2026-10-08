# Declares the RGW STS roles that let k3s apps reach their S3 buckets with their SPIFFE identity, and reconciles them with radosgw-admin.
#
# Each app gets one role in the RGW account `k8s`. The role trusts exactly
# the JWT-SVID spiffe://home.garvey.sh/ns/<namespace>/sa/<serviceAccount>
# (audience rgw.home.garvey.sh, enforced by the OIDC provider registered for
# spiffe-oidc.home.garvey.sh) and allows s3:* on buckets named
# <bucketPrefix>*, nothing else; the app creates its own buckets. A pod gets
# credentials with AWS_WEB_IDENTITY_TOKEN_FILE + AWS_ROLE_ARN; see "S3 on Ceph
# RGW" in k8s/manifests/spire/README.md.
#
# RGW has no IAM permissions boundaries, so whatever can create roles is admin
# of the account. Roles are therefore only made here, by radosgw-admin on a
# Ceph node, never by anything in the cluster. The reconciler owns every role
# under the path /nix-managed/: it creates and updates the ones listed and
# deletes the rest. Roles outside that path are left alone. Import this on one
# Ceph node.
{ config, lib, pkgs, ... }:

let
  cfg = config.services.rgwRoles;
  ceph = config.services.ceph.mon.package;

  account = "RGW77873637642885287";
  issuer = "spiffe-oidc.home.garvey.sh";
  trustDomain = "home.garvey.sh";
  managedPath = "/nix-managed/";

  trust = app: builtins.toJSON {
    Version = "2012-10-17";
    Statement = [{
      Effect = "Allow";
      Principal.Federated = [ "arn:aws:iam::${account}:oidc-provider/${issuer}" ];
      Action = [ "sts:AssumeRoleWithWebIdentity" ];
      Condition.StringEquals."${issuer}:sub" =
        "spiffe://${trustDomain}/ns/${app.namespace}/sa/${app.serviceAccount}";
    }];
  };
  permissions = app: builtins.toJSON {
    Version = "2012-10-17";
    Statement = [{
      Effect = "Allow";
      Action = [ "s3:*" ];
      Resource = [
        "arn:aws:s3:::${app.bucketPrefix}*"
        "arn:aws:s3:::${app.bucketPrefix}*/*"
      ];
    }];
  };

  desired = pkgs.writeText "rgw-roles.json" (builtins.toJSON
    (lib.mapAttrsToList (name: app: {
      inherit name;
      trust = trust app;
      policy = permissions app;
    }) cfg.apps));

  prefixes = lib.mapAttrsToList (name: app: { inherit name; p = app.bucketPrefix; }) cfg.apps;
  # One app's prefix may not start another's: role "cache" (cache*) would
  # otherwise reach every bucket of an app with prefix "cache-x".
  overlaps = lib.concatMap (a: map (b: "${a.name} (${a.p}) and ${b.name} (${b.p})")
    (lib.filter (b: b.name != a.name && lib.hasPrefix a.p b.p) prefixes)) prefixes;

  reconcile = pkgs.writeShellApplication {
    name = "rgw-roles-reconcile";
    runtimeInputs = [ ceph pkgs.jq pkgs.coreutils ];
    text = ''
      A=${account}
      MANAGED=${managedPath}
      DESIRED=${desired}
      same() { [ "$(jq -cS . <<<"$1")" = "$(jq -cS . <<<"$2")" ]; }

      while read -r role; do
        name=$(jq -r .name <<<"$role")
        trust=$(jq -r .trust <<<"$role")
        policy=$(jq -r .policy <<<"$role")
        if ! cur=$(radosgw-admin role get --account-id="$A" --role-name="$name" 2>/dev/null); then
          radosgw-admin role create --account-id="$A" --role-name="$name" \
            --path="$MANAGED" --assume-role-policy-doc="$trust" >/dev/null
          echo "$name: created"
          cur=$(radosgw-admin role get --account-id="$A" --role-name="$name")
        fi
        if [ "$(jq -r .Path <<<"$cur")" != "$MANAGED" ]; then
          echo "$name: a role with this name exists outside $MANAGED; not touching it" >&2
          exit 1
        fi
        if ! same "$(jq -r .AssumeRolePolicyDocument <<<"$cur")" "$trust"; then
          radosgw-admin role-trust-policy modify --account-id="$A" --role-name="$name" \
            --assume-role-policy-doc="$trust" >/dev/null
          echo "$name: trust policy updated"
        fi
        if ! same "$(jq -r '.PermissionPolicies[]? | select(.PolicyName == "bucket") | .PolicyValue' <<<"$cur")" "$policy"; then
          radosgw-admin role-policy put --account-id="$A" --role-name="$name" \
            --policy-name=bucket --policy-doc="$policy" >/dev/null
          echo "$name: permission policy updated"
        fi
        for extra in $(jq -r '.PermissionPolicies[]? | select(.PolicyName != "bucket") | .PolicyName' <<<"$cur"); do
          radosgw-admin role-policy delete --account-id="$A" --role-name="$name" --policy-name="$extra" >/dev/null
          echo "$name: removed unmanaged policy $extra"
        done
      done < <(jq -c '.[]' "$DESIRED")

      for name in $(radosgw-admin role list --account-id="$A" --path-prefix="$MANAGED" | jq -r '.[].RoleName'); do
        jq -e --arg n "$name" 'any(.[]; .name == $n)' "$DESIRED" >/dev/null && continue
        for p in $(radosgw-admin role-policy list --account-id="$A" --role-name="$name" | jq -r '.[]'); do
          radosgw-admin role-policy delete --account-id="$A" --role-name="$name" --policy-name="$p" >/dev/null
        done
        radosgw-admin role delete --account-id="$A" --role-name="$name" >/dev/null
        echo "$name: deleted (no longer declared)"
      done
    '';
  };

  unit = {
    description = "Reconcile RGW STS roles for k3s apps";
    wants = [ "ceph-mon.target" ];
    after = [ "ceph-mon.target" "network-online.target" ];
    serviceConfig = {
      Type = "oneshot";
      ExecStart = lib.getExe reconcile;
    };
  };
in
{
  options.services.rgwRoles.apps = lib.mkOption {
    description = "RGW STS roles for k3s apps, keyed by role name.";
    default = { };
    type = lib.types.attrsOf (lib.types.submodule {
      options = {
        namespace = lib.mkOption { type = lib.types.str; };
        serviceAccount = lib.mkOption { type = lib.types.str; };
        bucketPrefix = lib.mkOption {
          type = lib.types.strMatching "[a-z0-9][a-z0-9.-]{2,}";
          description = "The app may create and use every bucket whose name starts with this.";
        };
      };
    });
  };

  config = {
    # The apps. Adding one: an entry here, deploy this host, then give the app
    # the role's ARN, arn:aws:iam::RGW77873637642885287:role/nix-managed/<name>
    # (the path is part of the ARN).
    services.rgwRoles.apps = {
      # The celler Nix binary cache (k8s/manifests/celler).
      celler = { namespace = "celler"; serviceAccount = "celler"; bucketPrefix = "celler"; };
    };

    assertions = [{
      assertion = overlaps == [ ];
      message = "rgw-roles.nix: bucket prefixes overlap: ${lib.concatStringsSep "; " overlaps}";
    }];

    environment.systemPackages = [ reconcile ];

    # rgw-roles runs on every switch that changes the declared roles (its
    # ExecStart embeds them, so the unit changes and is restarted).
    # rgw-roles-drift runs the same daily, to undo hand edits; it is separate
    # because a timer cannot re-run a oneshot that RemainAfterExit keeps active.
    systemd.services.rgw-roles = unit // {
      wantedBy = [ "multi-user.target" ];
      serviceConfig = unit.serviceConfig // { RemainAfterExit = true; };
    };
    systemd.services.rgw-roles-drift = unit;
    systemd.timers.rgw-roles-drift = {
      wantedBy = [ "timers.target" ];
      timerConfig = { OnCalendar = "daily"; RandomizedDelaySec = "1h"; Persistent = true; };
    };
  };
}
