# OpenShell v0.1.3 built from source with the IPv6 policy-DNS patch
# (patches/openshell-ipv6-policy-dns.patch) and the supervisor template-labels
# patch (patches/openshell-supervisor-template-labels.patch): the CLI, and the
# gateway, supervisor and sandbox-runtime images that k8s/vendor/openshell runs.
# nixpkgs packages only the CLI, at an older release (0.0.x) that cannot talk
# to a 0.1 gateway. Imported by packages/outputs.nix.
{ nixpkgs, system }:
let
  pkgs = import nixpkgs { inherit system; };
  inherit (pkgs) lib;

  version = "0.1.3";

  # Upstream's v0.1.3 policy DNS answers every AAAA query with NODATA, so no
  # sandbox in this IPv6-only cluster could resolve a name (NVIDIA/OpenShell#3702,
  # rebased; see patches/UPSTREAMABLE_FIXES.md). agents/outputs.nix checks it
  # is applied.
  patch = ../../patches/openshell-ipv6-policy-dns.patch;

  # Upstream's Kubernetes driver puts the sandbox template's labels only on the
  # workload pod, never on the supervisor pod that holds the SPIFFE identity,
  # where the openshell-sandboxes ClusterSPIFFEID (k8s/vendor/spire/values.yaml)
  # reads the agent type. This copies them over, under the driver's own labels
  # (see patches/UPSTREAMABLE_FIXES.md). agents/outputs.nix gives it a
  # deadline. Applies on top of the #3702 patch.
  labelsPatch = ../../patches/openshell-supervisor-template-labels.patch;

  src = pkgs.applyPatches {
    name = "openshell-${version}-src";
    src = pkgs.fetchFromGitHub {
      owner = "NVIDIA";
      repo = "OpenShell";
      tag = "v${version}";
      hash = "sha256-7cIJ6YbBWev+C4uo2wxGer+nEpE3hxPEejC+BSo/VDc=";
    };
    patches = [ patch labelsPatch ];
    # The workspace's crates are versioned 0.0.0; release builds stamp it.
    postPatch = ''
      substituteInPlace Cargo.toml \
        --replace-fail 'version = "0.0.0"' 'version = "${version}"'
    '';
  };

  cargoDeps = pkgs.rustPlatform.fetchCargoVendor {
    inherit src;
    name = "openshell-${version}";
    hash = "sha256-V1EB2O31opEYm01BC2yYGejgDrVAzTDJEEbZOy9fUVI=";
  };

  common = {
    inherit version src cargoDeps;
    # Baked into the gateway as the default image tag; must match the version.
    env.OPENSHELL_IMAGE_TAG = version;
  };

  # The gateway and the supervisor, linked against glibc. The gateway links the
  # system libz3 (upstream's images vendor it).
  workspace = pkgs.rustPlatform.buildRustPackage (common // {
    pname = "openshell";
    nativeBuildInputs = [ pkgs.pkg-config pkgs.rustPlatform.bindgenHook ];
    buildInputs = [ pkgs.z3 ];
    # --bin applies to every -p, so this builds only these two binaries.
    cargoBuildFlags = [
      "-p" "openshell-gateway" "--bin" "openshell-gateway"
      "-p" "openshell-supervisor" "--bin" "openshell-supervisor"
    ];
    # The patched crates' own tests, which include the patches'.
    cargoTestFlags = [
      "-p" "openshell-core" "-p" "openshell-supervisor-network"
      "-p" "openshell-driver-kubernetes"
    ];
    # Tests the build sandbox cannot run: the filesystem preflight ones exec
    # /bin/sleep and the like, and two proxy ones resolve dns.google.
    checkFlags = [
      "--skip=e2fsprogs::tests::filesystem_preflight_"
      "--skip=proxy::tests::test_forward_public_ip_allowed_without_allowed_ips"
      "--skip=proxy::tests::test_resolve_check_allowed_ips_rejects_outside_allowlist"
    ];
  });

  # The CLI, built on its own so that changing it leaves the images' digests
  # alone.
  cli = pkgs.rustPlatform.buildRustPackage (common // {
    pname = "openshell-cli";
    nativeBuildInputs = [ pkgs.pkg-config pkgs.rustPlatform.bindgenHook ];
    buildInputs = [ pkgs.z3 ];
    cargoBuildFlags = [ "-p" "openshell-cli" "--bin" "openshell" ];
    doCheck = false;
    meta.mainProgram = "openshell";
  });

  # The sandbox runtime. The driver copies this binary into each sandbox's
  # workload container and runs it there, inside whatever image the sandbox
  # uses, so it must be static (upstream builds it for musl too).
  sandbox = pkgs.pkgsStatic.rustPlatform.buildRustPackage (common // {
    pname = "openshell-sandbox";
    cargoBuildFlags = [ "-p" "openshell-sandbox" "--bin" "openshell-sandbox" ];
    doCheck = false;
  });

  # An image's tag is the version plus its binary's store hash (the
  # supervisor's also its CA bundle's), so every build gets its own tag.
  tagOf = drv: "${version}-${builtins.substring 11 7 drv.outPath}";

  # The images follow upstream's deploy/docker/Dockerfile.{gateway,supervisor,
  # sandbox}: the same binary paths, entrypoints and users, which the chart and
  # the Kubernetes driver rely on. Pushed as oci.garvey.sh/openshell/<name>:<tag>
  # and pinned by digest in k8s/vendor/openshell/values.yaml.
  gateway-image = pkgs.dockerTools.buildLayeredImage {
    name = "openshell/gateway";
    tag = tagOf workspace;
    contents = [ pkgs.cacert ];
    extraCommands = ''
      mkdir -p usr/local/bin app tmp
      chmod 1777 tmp
      ln -s ${workspace}/bin/openshell-gateway usr/local/bin/openshell-gateway
    '';
    config = {
      Entrypoint = [ "/usr/local/bin/openshell-gateway" ];
      Cmd = [ "--bind-address" "0.0.0.0" "--port" "8080" ];
      User = "1000:1000";
      WorkingDir = "/app";
      ExposedPorts = { "8080/tcp" = { }; };
      # The OIDC issuer's JWKS is fetched over HTTPS.
      Env = [ "SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt" ];
    };
  };

  # The supervisor's trust store: the public CAs plus our root (pki/root.crt at
  # the repo root), to which in-cluster server certificates such as agent-token's
  # (k8s/manifests/agents/) chain. Token grants verify their token_endpoint against
  # SSL_CERT_FILE (reqwest's native roots, rustls-native-certs). The proxy's
  # upstream TLS does not use it: it trusts the bundled Mozilla roots plus
  # the first of a fixed list of system bundle paths, none of which the image
  # has.
  supervisorCAs = pkgs.cacert.override { extraCertificateFiles = [ ../../pki/root.crt ]; };

  # Runs as root, as upstream's: the driver sets the supervisor's user.
  supervisor-image = pkgs.dockerTools.buildLayeredImage {
    name = "openshell/supervisor";
    tag = "${tagOf workspace}-${builtins.substring 11 7 supervisorCAs.outPath}";
    contents = [ supervisorCAs ];
    extraCommands = ''
      ln -s ${workspace}/bin/openshell-supervisor openshell-supervisor
    '';
    config = {
      Entrypoint = [ "/openshell-supervisor" ];
      Env = [ "SSL_CERT_FILE=${supervisorCAs}/etc/ssl/certs/ca-bundle.crt" ];
    };
  };

  # Only the static binary, as a real file: the bootstrap init container copies
  # it into the sandbox's runtime volume.
  sandbox-image = pkgs.dockerTools.buildImage {
    name = "openshell/sandbox";
    tag = tagOf sandbox;
    extraCommands = ''
      install -m 0555 ${sandbox}/bin/openshell-sandbox openshell-sandbox
    '';
    config = {
      Entrypoint = [ "/openshell-sandbox" ];
      User = "65532:65532";
    };
  };
  # Unpacks each image and checks what the chart and the Kubernetes driver rely
  # on: the entrypoint exists and is executable, the user, the sandbox binary
  # is a static file of its own, the supervisor takes the patch's flag and
  # trusts the root.
  images-test = pkgs.runCommand "openshell-images-test"
    { nativeBuildInputs = [ pkgs.jq pkgs.file ]; } ''
      # unpack <image> <dir>: the image's layers, in order, into <dir>/root.
      unpack() {
        mkdir -p "$2/root"
        # Image tarballs carry read-only directory modes; keep ours writable.
        tar -xzf "$1" -C "$2" --no-overwrite-dir
        for layer in $(jq -r '.[0].Layers[]' "$2/manifest.json"); do
          tar -xf "$2/$layer" -C "$2/root" --no-overwrite-dir 2> /dev/null
        done
        jq '.config' "$2/$(jq -r '.[0].Config' "$2/manifest.json")" > "$2/config.json"
      }
      # expect <dir> <jq filter> <value>
      expect() {
        got=$(jq -c "$2" "$1/config.json")
        [ "$got" = "$3" ] || { echo "$1: $2 is $got, expected $3"; exit 1; }
      }
      # executable <dir> <path>: <path> in the image, following store symlinks.
      executable() {
        p="$1/root$2"
        if [ -L "$p" ]; then p="$1/root$(readlink "$p")"; fi
        [ -f "$p" ] && [ -x "$p" ] || { echo "$1: $2 is not an executable file"; exit 1; }
        echo "$p"
      }

      unpack ${gateway-image} gateway
      expect gateway .Entrypoint '["/usr/local/bin/openshell-gateway"]'
      expect gateway .User '"1000:1000"'
      expect gateway .WorkingDir '"/app"'
      executable gateway /usr/local/bin/openshell-gateway > /dev/null

      unpack ${supervisor-image} supervisor
      expect supervisor .Entrypoint '["/openshell-supervisor"]'
      supervisor=$(executable supervisor /openshell-supervisor)
      "$supervisor" --help | grep -q -- --policy-dns-ipv6-egress \
        || { echo "supervisor: no --policy-dns-ipv6-egress; is the patch applied?"; exit 1; }
      # Token grants verify the token endpoint against SSL_CERT_FILE, which
      # must hold the root.
      cafile=$(jq -r '.Env[] | select(startswith("SSL_CERT_FILE=")) | ltrimstr("SSL_CERT_FILE=")' supervisor/config.json)
      [ -f "supervisor/root$cafile" ] \
        || { echo "supervisor: SSL_CERT_FILE $cafile is not in the image"; exit 1; }
      grep -qF "$(sed -n 5p ${../../pki/root.crt})" "supervisor/root$cafile" \
        || { echo "supervisor: SSL_CERT_FILE does not hold the root"; exit 1; }

      unpack ${sandbox-image} sandbox
      expect sandbox .Entrypoint '["/openshell-sandbox"]'
      expect sandbox .User '"65532:65532"'
      [ ! -L sandbox/root/openshell-sandbox ] \
        || { echo "sandbox: /openshell-sandbox must be a file, not a symlink"; exit 1; }
      executable sandbox /openshell-sandbox > /dev/null
      file sandbox/root/openshell-sandbox | grep -q 'static-pie linked\|statically linked' \
        || { echo "sandbox: /openshell-sandbox is not static"; exit 1; }
      [ "$(sandbox/root/openshell-sandbox --version)" = "openshell-sandbox ${version}" ]

      touch $out
    '';
in
{
  inherit cli sandbox gateway-image supervisor-image sandbox-image images-test;
}
