#!/usr/bin/env bash
#
# Run all tests: host unit tests + firmware build verification.
#
# Usage (from repo root, inside the Nix devShell):
#   nix develop --command bash tests/run_all.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
UNIT_BUILD="$REPO_ROOT/tests/build"

# tests/CMakeLists.txt builds Unity from ${UNITY_SOURCE_DIR}. The devShell exports
# it (see flake.nix); outside the devShell cmake would otherwise expand it to the
# empty string and fail with the unhelpful "Cannot find source file: /src/unity.c".
if [ -z "${UNITY_SOURCE_DIR:-}" ]; then
  echo "error: UNITY_SOURCE_DIR is not set — run this inside the devShell:" >&2
  echo "  nix develop --command bash tests/run_all.sh" >&2
  exit 1
fi

echo "========================================="
echo " Step 1: Host unit tests (common/)"
echo "========================================="
cmake -S "$REPO_ROOT/tests" -B "$UNIT_BUILD" -DCMAKE_BUILD_TYPE=Debug \
  -DUNITY_SOURCE_DIR="$UNITY_SOURCE_DIR"
cmake --build "$UNIT_BUILD"
"$UNIT_BUILD/run_tests"

echo ""
echo "========================================="
echo " Step 2: Firmware build verification (garage-opener)"
echo "========================================="
bash "$REPO_ROOT/tests/test_build.sh"

echo ""
echo "========================================="
echo " Step 3: Firmware build verification (freezer-temp-sensor)"
echo "========================================="
bash "$REPO_ROOT/tests/test_build_freezer.sh"
