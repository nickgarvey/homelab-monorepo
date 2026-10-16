# Overlay that lets nixpkgs' ceph build: the bundled qatlib and qatzip autogen.sh have their shebang below the license header (nixpkgs#576414).
#
# CMake runs ./autogen.sh directly, and with no shebang on line 1 the exec
# fails with ENOEXEC ("exec format error") at the qatlib_ext configure step.
# Used by modules/services/ceph-package.nix (NixOS hosts) and
# packages/ceph-backup-worker/outputs.nix.
#
# Remove this overlay once nixpkgs fixes it. Evaluation fails when nixpkgs'
# ceph files mention autogen.sh (their fix landed), and the patch stops
# applying when the tarball moves the shebang itself.
final: prev:
let
  cephDir = prev.path + "/pkgs/by-name/ce/ceph";
  upstreamText = builtins.readFile (cephDir + "/ceph.nix") + builtins.readFile (cephDir + "/src.nix")
    + builtins.concatStringsSep "\n" (builtins.attrNames (builtins.readDir (cephDir + "/patches")));
  fixedUpstream = prev.lib.hasInfix "autogen" upstreamText;
in
{
  ceph =
    if fixedUpstream then
      throw "nixpkgs' ceph now handles the qatlib/qatzip autogen.sh shebang; remove patches/ceph-overlay.nix, patches/ceph-qat-autogen-shebang.patch and modules/services/ceph-package.nix."
    else
      prev.ceph.overrideAttrs (old: {
        patches = (old.patches or [ ]) ++ [ ./ceph-qat-autogen-shebang.patch ];
      });
}
