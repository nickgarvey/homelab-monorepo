#!/usr/bin/env bash
# fetch-vendor.sh [--check] <name>...
# Downloads vendor/<name>/$OUT from the upstream URL in vendor/<name>/source.env.
#   --check: download to a temp dir and fail if the committed file differs.
# source.env sets VERSION, URL (which may use $VERSION) and OUT.
# Run from k8s/.
set -euo pipefail
cd "$(dirname "$0")/.."

check=0
[ "${1:-}" = --check ] && { check=1; shift; }
[ $# -gt 0 ] || { sed -n 2,6p "$0" | sed 's/^# \?//'; exit 2; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

status=0
for name in "$@"; do
  (
    VERSION= URL= OUT=
    # shellcheck disable=SC1090
    source "vendor/$name/source.env"
    curl -fsSL "$URL" -o "$tmp/$name"
    if [ $check = 1 ]; then
      if ! diff -u "vendor/$name/$OUT" "$tmp/$name" > "$tmp/$name.diff"; then
        echo "vendor/$name/$OUT differs from $URL" >&2
        head -40 "$tmp/$name.diff" >&2
        exit 1
      fi
    else
      mv "$tmp/$name" "vendor/$name/$OUT"
      echo "fetched vendor/$name/$OUT"
    fi
  ) || status=1
done
exit $status
