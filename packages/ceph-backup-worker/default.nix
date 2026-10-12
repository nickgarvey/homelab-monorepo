# The ceph-backup-worker Go module (cmd/ceph-backup-worker): the CephBackup
# Temporal workflow and its activities. buildGoModule runs `go test ./...`,
# so the tests, which fake every command and Temporal, gate the build. Built
# by ./outputs.nix.
{ buildGoModule }:
buildGoModule {
  pname = "ceph-backup-worker";
  version = "0.1.0";
  src = ./.;
  vendorHash = "sha256-Xqh/P9qLw/mJHX6Wj/q4a2XOELC1zORdVn8V8qGZqZc=";
  env.CGO_ENABLED = "0";
  meta.mainProgram = "ceph-backup-worker";
}
