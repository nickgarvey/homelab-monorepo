# ceph-backup-worker and its image, which k8s/manifests/ceph-backup runs on
# the storage nodes. Imported by packages/outputs.nix.
{ nixpkgs, system }:
let
  pkgs = import nixpkgs { inherit system; };
  package = pkgs.callPackage ./. { };

  # The CLIs the activities shell out to. pkgs.ceph is the package the
  # storage nodes run (modules/services/ceph.nix), so the kernel-client
  # tools match the cluster's version.
  root = pkgs.buildEnv {
    name = "ceph-backup-worker-root";
    paths = [
      package
      pkgs.ceph # rbd, ceph, rados, mount.ceph
      pkgs.kopia
      pkgs.rclone
      pkgs.fuse3 # fusermount3, for rclone mount
      pkgs.util-linux # mount, umount, blkid, findmnt, mountpoint
      pkgs.e2fsprogs
      pkgs.kmod # modprobe, for rbd map and mount.ceph
      pkgs.cacert
      pkgs.coreutils
      pkgs.bashInteractive # kubectl exec, by hand
    ];
    pathsToLink = [ "/bin" "/sbin" "/etc" ];
  };
in
{
  inherit package;

  # Pushed as oci.garvey.sh/ceph-backup-worker:<tag>, the tag being the
  # store hash of everything in the image, so a kopia or ceph bump changes
  # it too.
  image = pkgs.dockerTools.buildLayeredImage {
    name = "ceph-backup-worker";
    tag = builtins.substring 11 32 root.outPath;
    contents = root;
    extraCommands = ''
      mkdir -p tmp run var/cache root
      chmod 1777 tmp
    '';
    config = {
      Entrypoint = [ "/bin/ceph-backup-worker" ];
      # Root in a privileged pod: it maps RBD images and mounts filesystems.
      User = "0:0";
      Env = [
        "PATH=/bin:/sbin"
        "HOME=/root"
        "SSL_CERT_FILE=/etc/ssl/certs/ca-bundle.crt"
      ];
      ExposedPorts."9090/tcp" = { };
    };
  };
}
