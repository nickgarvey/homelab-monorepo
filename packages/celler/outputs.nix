# Celler Nix binary cache: the package (client `celler`, server `cellerd`) built
# from its flake input with object_store patched for RGW's STS, and the cellerd
# image run in k8s/manifests/celler. Imported by packages/outputs.nix.
{ nixpkgs, celler, system }:
let
  pkgs = import nixpkgs { inherit system; };

  # object_store sends AssumeRoleWithWebIdentity's parameters in the query
  # string; RGW answers that with 405 and accepts them only as a form body (see
  # the patch header). The patch applies to whatever object_store Celler locks,
  # so a version bump that breaks it fails the build.
  stsPatch = ../../patches/object_store-sts-form-body.patch;
  craneLib = (celler.inputs.crane.mkLib pkgs).overrideScope (final: prev: {
    vendorCargoDeps = args: prev.vendorCargoDeps (args // {
      overrideVendorCargoPackage = p: drv:
        if p.name == "object_store"
        then drv.overrideAttrs (old: { patches = (old.patches or [ ]) ++ [ stsPatch ]; })
        else drv;
    });
  });

  # Celler's own build recipe, with the patched crane.
  package = (pkgs.callPackage "${celler}/crane.nix" { inherit craneLib; }).celler;
in
{
  inherit package;

  # Pushed as oci.garvey.sh/celler:<tag>. The tag is the celler input's
  # revision plus a hash of the patch, so bumping either changes the tag in
  # k8s/manifests/celler/deployment.yaml too.
  image = pkgs.dockerTools.buildLayeredImage {
    name = "celler";
    tag = "${celler.shortRev}-${builtins.substring 0 7 (builtins.hashFile "sha256" stsPatch)}";

    contents = pkgs.buildEnv {
      name = "image-root";
      paths = [ package pkgs.cacert ];
      pathsToLink = [ "/bin" "/etc" ];
    };

    config = {
      Entrypoint = [ "/bin/cellerd" ];
      Cmd = [ "-f" "/etc/celler/server.toml" ];
      User = "65534:65534";
      ExposedPorts = { "8080/tcp" = { }; };
      Env = [
        "SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
      ];
    };
  };
}
