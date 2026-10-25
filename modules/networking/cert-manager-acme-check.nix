# Flake check: the names in dns.nix's certManagerAcme are exactly the dnsNames of
# the Certificates on the letsencrypt-rfc2136 issuers in k8s/manifests, so Knot's
# acme-cert-manager ACL (modules/containers/knot-auth.nix) neither misses a name
# nor allows a stale one.
{ pkgs, lib }:
let
  dns = import ./dns.nix { inherit lib; };
  expected = lib.concatStringsSep "\n" (lib.sort lib.lessThan (
    map (n: "${n}.home.garvey.sh") dns.certManagerAcme.home
    ++ map (n: "${n}.garvey.sh") dns.certManagerAcme.garveySh
  ));
in
pkgs.runCommand "cert-manager-acme-names" {
  nativeBuildInputs = [ pkgs.yq-go ];
  manifests = ../../k8s/manifests;
  inherit expected;
} ''
  find "$manifests" -name '*.yaml' -print0 \
    | xargs -0 yq -N '
        select(.kind == "Certificate" and (.spec.issuerRef.name // "" | test("^letsencrypt-rfc2136-")))
        | .spec.dnsNames[]' \
    | sort -u > actual
  printf '%s\n' "$expected" > expected
  if ! diff -u expected actual; then
    echo "certManagerAcme in modules/networking/dns.nix (-) differs from the letsencrypt-rfc2136 Certificates in k8s/manifests (+)" >&2
    exit 1
  fi
  touch $out
''
