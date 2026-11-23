# glkvm-cert: the program that keeps glkvm.home.garvey.sh's Let's Encrypt
# certificate current on the GL.iNet KVM itself, and the script that installs
# it there (docs/tls.md). Imported by packages/outputs.nix.
{ nixpkgs, system }:
let
  pkgs = import nixpkgs { inherit system; };
  inherit (pkgs) lib;

  # Built for this machine; buildGoModule runs `go test` here.
  package = pkgs.buildGoModule {
    pname = "glkvm-cert";
    version = "0.1.0";
    src = lib.fileset.toSource {
      root = ./.;
      fileset = lib.fileset.unions [ ./go.mod ./go.sum ./main.go ./main_test.go ];
    };
    vendorHash = "sha256-l5uOiX2q+szLM6Y749P+fGzCaVAH2zyx2EUd+WjJ6Ko=";
    env.CGO_ENABLED = "0";
    ldflags = [ "-s" "-w" ];
    meta.mainProgram = "glkvm-cert";
  };

  # The same source for the KVM's 32-bit ARMv7 CPU. It is a static binary, so
  # it needs nothing from the device's Buildroot userland.
  arm = package.overrideAttrs (old: {
    pname = "glkvm-cert-armv7";
    env = old.env // { GOARCH = "arm"; GOARM = "7"; };
    doCheck = false;
    # Cross builds land in bin/linux_arm.
    postInstall = ''
      mv $out/bin/linux_arm/glkvm-cert $out/bin/glkvm-cert
      rmdir $out/bin/linux_arm
    '';
  });

  # `nix run .#glkvm-cert-install [-- --staging] [--force]`: copies the
  # binary, its boot hook and its configuration to the KVM over SSH and
  # (re)starts it. Run it with the KVM switched on.
  #   --staging  order from Let's Encrypt's staging CA
  #   --force    replace the certificate now, whatever its state (needed to go
  #              from a staging certificate to a real one)
  install = pkgs.writeShellApplication {
    name = "glkvm-cert-install";
    runtimeInputs = [ pkgs.openssh pkgs.sops pkgs.jq ];
    # The remote commands are meant to expand $dir and $hook on this side.
    excludeShellChecks = [ "SC2029" ];
    text = ''
      host="''${GLKVM_HOST:-root@glkvm.home.garvey.sh}"
      directory=https://acme-v02.api.letsencrypt.org/directory
      force=
      for arg in "$@"; do
        case "$arg" in
          --staging) directory=https://acme-staging-v02.api.letsencrypt.org/directory ;;
          --force) force=1 ;;
          *) echo "usage: glkvm-cert-install [--staging] [--force]" >&2; exit 1 ;;
        esac
      done

      dir=/etc/kvmd/user/glkvm-cert
      hook=/etc/kvmd/user/scripts/S50glkvm-cert.sh

      # Files go over as `cat >`, which needs nothing of the device's dropbear
      # beyond a shell.
      ssh "$host" "mkdir -p $dir /etc/kvmd/user/scripts"
      ssh "$host" "cat > $dir/glkvm-cert.new && chmod 0755 $dir/glkvm-cert.new && mv $dir/glkvm-cert.new $dir/glkvm-cert" \
        < ${arm}/bin/glkvm-cert
      ssh "$host" "cat > $hook" < ${./S50glkvm-cert.sh}

      # The TSIG secret goes from sops to the device without touching disk here.
      sops decrypt --extract '["acme_tsig_secret"]' ${../../secrets/glkvm-acme.yaml} \
        | jq -Rn --arg directory "$directory" '{
            domain: "glkvm.home.garvey.sh",
            directory: $directory,
            nameserver: "10.28.0.6:53",
            zone: "home.garvey.sh",
            tsig_key: "acme-glkvm",
            tsig_secret: input
          }' \
        | ssh "$host" "umask 077 && cat > $dir/config.json"

      ssh "$host" "sh $hook stop"
      if [ -n "$force" ]; then
        ssh "$host" "$dir/glkvm-cert -once -force"
      fi
      ssh "$host" "sh $hook start"
      echo "glkvm-cert is running on $host; its log is in the device's syslog (run logread there)."
    '';
  };
in
{
  inherit package arm install;
}
