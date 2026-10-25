# Backs up ~/homelab-monorepo daily to CephFS (~/local-drive), keeping 30 days of archives.
#
# Each run writes ~/local-drive/backups/homelab-monorepo/<YYYY-MM-DD>.tar.zst
# holding:
#   repo.bundle        git bundle --all: every branch, tag and the stash
#   ait.db             the gitignored ait issue database (sqlite .backup)
#   uncommitted.patch  git diff HEAD --binary of tracked files
#   untracked.tar      untracked files that are not gitignored
# Restore: git clone repo.bundle, then copy ait.db to .ait/ and apply the rest.
#
# The Ceph backup worker copies CephFS off-site (docs/ceph-backup.md), so the
# archives also reach Storj. Persistent=true runs a missed day at next boot.
# Each success is pushed to Prometheus as a timestamp, and
# k8s/manifests/prometheus/rules/repo-backup.yaml alerts when it goes stale.
#
# This is a stopgap: eventually a self-hosted git server should hold the repo
# (and so be what gets backed up), and this module can go.
{ config, lib, pkgs, ... }:

let
  repo = "/home/ngarvey/homelab-monorepo";
  dest = "/home/ngarvey/local-drive/backups/homelab-monorepo";
  stateDir = "/var/lib/repo-backup";

  probe = pkgs.writeShellApplication {
    name = "repo-backup-probe";
    text = ''
      t=0; [ -f ${stateDir}/last-success ] && t=$(cat ${stateDir}/last-success)
      echo "{\"last_success\":$t}"
    '';
  };
in
{
  systemd.services.repo-backup = {
    description = "Back up ${repo} to CephFS";
    wants = [ "network-online.target" ];
    after = [ "network-online.target" ];
    unitConfig.RequiresMountsFor = [ dest ];
    path = with pkgs; [ git sqlite gnutar zstd coreutils findutils ];
    serviceConfig = {
      Type = "oneshot";
      User = "ngarvey";
      Group = "users";
      Nice = 10;
      IOSchedulingClass = "idle";
      StateDirectory = "repo-backup";
    };
    script = ''
      set -euo pipefail
      stamp=$(date +%F)
      work=$(mktemp -d)
      trap 'rm -rf "$work"' EXIT

      git -C ${repo} bundle create "$work/repo.bundle" --all
      git -C ${repo} bundle verify --quiet "$work/repo.bundle"
      sqlite3 ${repo}/.ait/ait.db ".backup '$work/ait.db'"
      git -C ${repo} diff HEAD --binary > "$work/uncommitted.patch"
      git -C ${repo} ls-files -z --others --exclude-standard \
        | tar -C ${repo} --null -T - -cf "$work/untracked.tar"

      mkdir -p ${dest}
      tar -C "$work" -c repo.bundle ait.db uncommitted.patch untracked.tar \
        | zstd -q -T0 > "${dest}/.$stamp.tar.zst.tmp"
      mv -f "${dest}/.$stamp.tar.zst.tmp" "${dest}/$stamp.tar.zst"

      find ${dest} -maxdepth 1 -name '????-??-??.tar.zst' -mtime +30 -delete
      date +%s > ${stateDir}/last-success
    '';
  };

  systemd.timers.repo-backup = {
    wantedBy = [ "timers.target" ];
    timerConfig = {
      OnCalendar = "daily";
      Persistent = true;
      RandomizedDelaySec = "1h";
    };
  };

  homelab.metrics.sources.repo_backup = {
    type = "exec";
    mode = "scheduled";
    scheduled.exec_interval_secs = 180;
    command = [ (lib.getExe probe) ];
  };
  homelab.metrics.transforms.repo_backup_fields = {
    type = "remap";
    inputs = [ "repo_backup" ];
    source = ". = parse_json!(strip_whitespace!(to_string!(.message)))";
  };
  homelab.metrics.transforms.repo_backup_metric = {
    type = "log_to_metric";
    inputs = [ "repo_backup_fields" ];
    metrics = [{
      type = "gauge";
      field = "last_success";
      name = "repo_backup_last_success_timestamp_seconds";
      tags.hostname = config.networking.hostName;
    }];
  };
}
