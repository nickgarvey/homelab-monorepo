#!/usr/bin/env bash
#
# Build verification test for devices/garage-opener.
#
# Checks that the firmware image:
#   1. Builds successfully via `nix build`
#   2. Produces a non-trivially-sized .bin
#   3. Passes esptool.py image_info (validates image header/checksums)
#   4. Contains expected ELF symbols
#
# Usage (from repo root):
#   nix develop --command bash tests/test_build.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MIN_BINARY_SIZE=131072   # 128 KiB

PASS=0
FAIL=0

pass() { echo "[PASS] $1"; ((PASS++)) || true; }
fail() { echo "[FAIL] $1"; ((FAIL++)) || true; }

echo "=== Build verification: garage-opener ==="
echo ""

# ---- 1. nix build -----------------------------------------------------------
echo "Running: nix build .#garage-opener ..."
OUT=$(nix build .#garage-opener --no-link --print-out-paths 2>/dev/null | tail -1)
if [[ -n "$OUT" ]]; then
    pass "nix build succeeded"
else
    fail "nix build failed"
    exit 1
fi
echo "Output: $OUT"
echo ""

BINARY="$OUT/garage_door_opener.bin"
ELF="$OUT/garage_door_opener.elf"

# ---- 2. Binary existence ----------------------------------------------------
if [[ -f "$BINARY" ]]; then
    pass "firmware binary exists"
else
    fail "firmware binary not found: $BINARY"
fi

# ---- 3. Binary size sanity check -------------------------------------------
if [[ -f "$BINARY" ]]; then
    SIZE=$(stat -c%s "$BINARY")
    if [[ "$SIZE" -ge "$MIN_BINARY_SIZE" ]]; then
        pass "binary size ${SIZE} bytes >= minimum ${MIN_BINARY_SIZE} bytes"
    else
        fail "binary size ${SIZE} bytes is suspiciously small (min ${MIN_BINARY_SIZE})"
    fi
fi

# ---- 4. esptool image_info --------------------------------------------------
if [[ -f "$BINARY" ]]; then
    echo ""
    echo "--- esptool.py image_info ---"
    if esptool.py --chip esp32s2 image_info "$BINARY" 2>&1; then
        pass "esptool.py image_info: image structure valid"
    else
        fail "esptool.py image_info: image validation failed"
    fi
fi

# ---- 5. ELF symbol checks ---------------------------------------------------
if [[ -f "$ELF" ]]; then
    echo ""
    echo "--- ELF symbol checks ---"
    NM_OUT=$(xtensa-esp32s2-elf-nm "$ELF" 2>/dev/null)
    for sym in app_main wifi_manager_init ha_client_init neopixel_init neopixel_set wifi_manager_connected; do
        if grep -q " T ${sym}\b\| t ${sym}\b" <<< "$NM_OUT"; then
            pass "symbol present: $sym"
        else
            fail "symbol missing: $sym"
        fi
    done
fi

# ---- Summary ----------------------------------------------------------------
echo ""
echo "==============================="
echo "Build tests: $PASS passed, $FAIL failed"
echo "==============================="

[[ "$FAIL" -eq 0 ]]
