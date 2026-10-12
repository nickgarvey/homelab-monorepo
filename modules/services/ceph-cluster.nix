# Facts about the Ceph cluster that both its nodes (ceph.nix) and its clients (cephfs-mount.nix) need: the fsid and which hosts run mons.
#
# Plain data, imported with `import`, not a NixOS module. Mon addresses are
# each host's static IPv6 in modules/networking/lan-hosts.nix; the mons speak
# msgr2 only, on 3300.
{
  fsid = "8d324da3-f3ab-40f8-aaa1-71ed33705f75";
  mons = [ "frul" "joor" "zah" ];
}
