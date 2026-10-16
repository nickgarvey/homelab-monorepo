# Applies patches/ceph-overlay.nix so every Ceph consumer on a host (daemons, ceph-client for mount.ceph) gets the buildable ceph.
{ ... }:

{
  nixpkgs.overlays = [ (import ../../patches/ceph-overlay.nix) ];
}
