# pki outputs: the check that every in-repo copy of the root is exactly pki/root.crt. Imported by the root flake.nix.
{ nixpkgs, ... }:
let
  pkgs = nixpkgs.legacyPackages.x86_64-linux;
  # Copies of the root that are not read from a cert-manager Secret, as
  # file and yq path to the PEM.
  copies = [
    { file = ../k8s/manifests/postgres/client-ca.yaml; path = ''.stringData."ca.crt"''; }
    { file = ../k8s/manifests/litellm/postgres-ca.yaml; path = ''.data."ca.crt"''; }
  ];
in
{
  checks.x86_64-linux.pki-root-copies = pkgs.runCommand "pki-root-copies" {
    nativeBuildInputs = [ pkgs.yq-go pkgs.openssl ];
  } (''
    fp() { openssl x509 -noout -fingerprint -sha256; }
    root=$(fp < ${./root.crt})
    check() {
      pem=$(yq -r "$2" "$1")
      n=$(printf '%s\n' "$pem" | grep -c 'BEGIN CERTIFICATE' || true)
      if [ "$n" != 1 ] || [ "$(printf '%s\n' "$pem" | fp)" != "$root" ]; then
        echo "$1 ($2) must hold exactly pki/root.crt; it holds $n certificate(s)" >&2
        exit 1
      fi
    }
  '' + pkgs.lib.concatMapStrings (c: ''
    check ${c.file} '${c.path}'
  '') copies + ''
    touch $out
  '');
}
