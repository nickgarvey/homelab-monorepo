# pki outputs: the check that every in-repo copy of the root or the hosts CA is exactly the file in pki/. Imported by the root flake.nix.
{ nixpkgs, ... }:
let
  pkgs = nixpkgs.legacyPackages.x86_64-linux;
  # Copies that are not read from a cert-manager Secret, as file, yq path to
  # the PEM, and the certificate it must be.
  copies = [
    { file = ../k8s/manifests/postgres/client-ca.yaml; path = ''.stringData."ca.crt"''; crt = ./root.crt; }
    { file = ../k8s/manifests/litellm/postgres-ca.yaml; path = ''.data."ca.crt"''; crt = ./root.crt; }
    { file = ../k8s/vendor/spire/values.yaml; path = ''."spire-server".nodeAttestor.x509POP.caBundle.bundle''; crt = ./dns-ca.crt; }
  ];
in
{
  checks.x86_64-linux.pki-root-copies = pkgs.runCommand "pki-root-copies" {
    nativeBuildInputs = [ pkgs.yq-go pkgs.openssl ];
  } (''
    fp() { openssl x509 -noout -fingerprint -sha256; }
    check() {
      pem=$(yq -r "$2" "$1")
      n=$(printf '%s\n' "$pem" | grep -c 'BEGIN CERTIFICATE' || true)
      if [ "$n" != 1 ] || [ "$(printf '%s\n' "$pem" | fp)" != "$(fp < "$3")" ]; then
        echo "$1 ($2) must hold exactly $4; it holds $n certificate(s)" >&2
        exit 1
      fi
    }
  '' + pkgs.lib.concatMapStrings (c: ''
    check ${c.file} '${c.path}' ${c.crt} pki/${baseNameOf (toString c.crt)}
  '') copies + ''
    touch $out
  '');
}
