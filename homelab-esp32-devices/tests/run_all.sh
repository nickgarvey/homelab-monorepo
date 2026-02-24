#!/usr/bin/env bash
#
# Run all tests: host unit tests + firmware build verification.
#
# Usage (from repo root, inside the Nix devShell):
#   nix develop --command bash tests/run_all.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
UNIT_BUILD="$REPO_ROOT/tests/build"

echo "========================================="
echo " Step 1: Host unit tests (common/)"
echo "========================================="
cmake -S "$REPO_ROOT/tests" -B "$UNIT_BUILD" -DCMAKE_BUILD_TYPE=Debug
cmake --build "$UNIT_BUILD"
"$UNIT_BUILD/run_tests"

echo ""
echo "========================================="
echo " Step 2: Firmware build verification"
echo "========================================="
bash "$REPO_ROOT/tests/test_build.sh"
