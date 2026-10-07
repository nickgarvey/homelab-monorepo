#!/usr/bin/env bash
# render-chart.sh [--check] <name>... | --all
# Renders vendor/<name>/<name>.yaml from the Helm chart named in
# vendor/<name>/chart.env and the values in vendor/<name>/values.yaml.
#   --check: render to a temp dir and fail if the committed file differs.
# chart.env sets REPO, CHART, VERSION, RELEASE, NAMESPACE, and CREATE_NAMESPACE=1
# when the chart does not emit its own Namespace object.
# Run from k8s/; needs helm (in the default dev shell).
set -euo pipefail
cd "$(dirname "$0")/.."

# The cluster's minor version, so Capabilities checks in the charts don't
# depend on whichever helm runs the render. Bump with k3s.
KUBE_VERSION=1.36

check=0
[ "${1:-}" = --check ] && { check=1; shift; }
if [ "${1:-}" = --all ]; then
  set -- $(for f in vendor/*/chart.env; do basename "$(dirname "$f")"; done)
fi
[ $# -gt 0 ] || { sed -n 2,8p "$0" | sed 's/^# \?//'; exit 2; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

render() {
  local name=$1 REPO CHART VERSION RELEASE NAMESPACE CREATE_NAMESPACE=0
  # shellcheck disable=SC1090
  source "vendor/$name/chart.env"
  if [ "$CREATE_NAMESPACE" = 1 ]; then
    printf -- '---\n# Builtin: "helm template" does not emit a Namespace\napiVersion: v1\nkind: Namespace\nmetadata:\n  name: %s\n' "$NAMESPACE"
  fi
  helm template "$RELEASE" "$CHART" --repo "$REPO" --version "$VERSION" \
    --namespace "$NAMESPACE" --kube-version "$KUBE_VERSION" --include-crds --no-hooks \
    -f "vendor/$name/values.yaml"
}

status=0
for name in "$@"; do
  out=vendor/$name/$name.yaml
  if [ $check = 1 ]; then
    render "$name" > "$tmp/$name.yaml"
    if ! diff -u "$out" "$tmp/$name.yaml" > "$tmp/$name.diff"; then
      echo "$out is stale; re-run scripts/render-chart.sh $name" >&2
      head -40 "$tmp/$name.diff" >&2
      status=1
    fi
  else
    render "$name" > "$tmp/$name.yaml"
    mv "$tmp/$name.yaml" "$out"
    echo "rendered $out"
  fi
done
exit $status
