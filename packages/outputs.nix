# packages outputs: homelab packages, container images and the deploy tool.
# Imported by the root flake.nix.
{ nixpkgs, celler, ... }:
let
  system = "x86_64-linux";
  pkgs = import nixpkgs { inherit system; };

  playwright-renderer = import ./playwright-renderer/outputs.nix { inherit nixpkgs system; };
  trmnl-display = import ./trmnl-display/outputs.nix { inherit nixpkgs system; };
  ninfer = import ./ninfer/outputs.nix { inherit nixpkgs system; };
  lan-table = import ./lan-table/outputs.nix { inherit nixpkgs system; };
  openshell = import ./openshell/outputs.nix { inherit nixpkgs system; };
  celler-pkg = import ./celler/outputs.nix { inherit nixpkgs celler system; };
  temporal-server = import ./temporal-server/outputs.nix { inherit nixpkgs system; };
  ceph-backup-worker = import ./ceph-backup-worker/outputs.nix { inherit nixpkgs system; };
  longhorn-backup-pruner = import ./longhorn-backup-pruner/outputs.nix { inherit nixpkgs system; };
  matrix-synapse = import ./matrix-synapse/outputs.nix { inherit nixpkgs system; };
  matrix-authentication-service = import ./matrix-authentication-service/outputs.nix { inherit nixpkgs system; };

  # The deploy binary, built from ./deploy. buildGoModule runs `go test`
  # as part of the build, so this derivation is also the test check.
  deploy = pkgs.buildGoModule {
    pname = "deploy";
    version = "0.1.0";
    src = ./deploy;
    vendorHash = null; # no external deps
  };

  # `nix run .#loop-build [-- validate|identifiers|certs|build|status]`: runs
  # and watches a Loop Browser Build workflow of the LoopWorkspace fork, or
  # reports the age and version of its last good build.
  loop-build = pkgs.writeShellApplication {
    name = "loop-build";
    runtimeInputs = [ pkgs.gh pkgs.jq pkgs.coreutils ];
    text = ''
      repo="''${LOOP_REPO:-nickgarvey/LoopWorkspace}"
      ref=main

      # dispatch <workflow file>: runs it on $ref and watches the run it made.
      dispatch() {
        local workflow="$1" since run=""
        # A minute of slack for skew between this clock and GitHub's.
        since=$(( $(date +%s) - 60 ))
        gh workflow run "$workflow" -R "$repo" --ref "$ref"
        for _ in $(seq 30); do
          run=$(gh run list -R "$repo" -w "$workflow" --event workflow_dispatch -L1 \
            --json databaseId,createdAt,url \
            --jq ".[] | select((.createdAt | fromdateiso8601) >= $since)")
          [ -n "$run" ] && break
          sleep 2
        done
        if [ -z "$run" ]; then
          echo "loop-build: dispatched $workflow on $repo, but no run appeared within a minute" >&2
          exit 1
        fi
        jq -r .url <<<"$run"
        gh run watch "$(jq -r .databaseId <<<"$run")" -R "$repo" --exit-status
      }

      # status: the last successful build, its TestFlight expiry, and the version on $ref.
      status() {
        local last
        last=$(for w in build_loop.yml build_loop_auto.yml; do
            gh run list -R "$repo" -w "$w" --status success -L1 --json createdAt,url
          done | jq -s 'add | max_by(.createdAt) // empty')
        if [ -z "$last" ]; then
          echo "No successful build of $repo yet."
        else
          jq -r '(.createdAt | fromdateiso8601) as $t
            | (90 - ((now - $t) / 86400 | floor)) as $left
            | "Last good build: \($t | strftime("%Y-%m-%d")) \(.url)",
              "TestFlight days left: \($left)",
              if $left < 30 then "WARNING: the TestFlight build expires in \($left) days. Run loop-build." else empty end' <<<"$last"
        fi
        echo "Loop version on $ref: $(gh api "repos/$repo/contents/VersionOverride.xcconfig?ref=$ref" \
          --jq '.content | @base64d | capture("LOOP_MARKETING_VERSION *= *(?<v>[^\\s]+)").v')"
      }

      case "''${1:-build}" in
        validate) dispatch validate_secrets.yml ;;
        identifiers) dispatch add_identifiers.yml ;;
        certs) dispatch create_certs.yml ;;
        build) dispatch build_loop.yml ;;
        status) status ;;
        *)
          echo "usage: loop-build [validate|identifiers|certs|build|status]" >&2
          exit 1
          ;;
      esac
    '';
  };
in
{
  packages.${system} = {
    inherit playwright-renderer;
    inherit trmnl-display;
    ninfer = ninfer.ninfer;
    ninfer-image = ninfer.image;
    inherit lan-table;
    openshell = openshell.cli;
    openshell-gateway-image = openshell.gateway-image;
    openshell-supervisor-image = openshell.supervisor-image;
    openshell-sandbox-image = openshell.sandbox-image;
    celler = celler-pkg.package;
    celler-image = celler-pkg.image;
    inherit deploy;
    inherit loop-build;
    temporal-server = temporal-server.package;
    temporal-server-image = temporal-server.image;
    ceph-backup-worker = ceph-backup-worker.package;
    ceph-backup-worker-image = ceph-backup-worker.image;
    longhorn-backup-pruner = longhorn-backup-pruner.package;
    longhorn-backup-pruner-image = longhorn-backup-pruner.image;
    matrix-synapse = matrix-synapse.package;
    matrix-synapse-image = matrix-synapse.image;
    matrix-authentication-service = matrix-authentication-service.package;
    matrix-authentication-service-image = matrix-authentication-service.image;
  };

  checks.${system} = {
    deploy-tests = deploy;
    temporal-server-tests = temporal-server.package;
    ceph-backup-worker-tests = ceph-backup-worker.package;
    openshell-images-test = openshell.images-test;
  };
}
