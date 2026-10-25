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
{ pkgs, ... }:

let
  repo = "/home/ngarvey/homelab-monorepo";
  dest = "/home/ngarvey/local-drive/backups/homelab-monorepo";
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
}
