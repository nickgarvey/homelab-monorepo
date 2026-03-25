#!/usr/bin/env bash
#
# Build verification test for devices/freezer-temp-sensor.
#
# Checks that the firmware image:
#   1. Compiles successfully with ESP-IDF (target: esp32c6)
#   2. Produces a non-trivially-sized .bin
#   3. Passes esptool.py image_info (validates image header/checksums)
#   4. Contains expected ELF symbols
#
# Secrets are handled automatically by cmake/generate_secrets.cmake —
# if sops or the encrypted file is unavailable, placeholder headers are
# generated in the build tree.
#
# Requires the ESP-IDF environment to be active:
#   nix develop --command bash tests/test_build_freezer.sh
#
# Exit codes: 0 = all checks passed, non-zero = failure.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEVICE_DIR="$REPO_ROOT/devices/freezer-temp-sensor"
BUILD_DIR="$DEVICE_DIR/build"
BINARY="$BUILD_DIR/freezer_temp_sensor.bin"
ELF="$BUILD_DIR/freezer_temp_sensor.elf"

MIN_BINARY_SIZE=131072   # 128 KiB

PASS=0
FAIL=0

pass() { echo "[PASS] $1"; ((PASS++)) || true; }
fail() { echo "[FAIL] $1"; ((FAIL++)) || true; }

echo "=== Build verification: freezer-temp-sensor ==="
echo ""

# ---- 1. Ensure idf.py is available ----------------------------------------
if ! command -v idf.py &>/dev/null; then
    echo "ERROR: idf.py not found. Run inside the Nix devShell:"
    echo "  nix develop --command bash tests/test_build_freezer.sh"
    exit 1
fi
echo "IDF version: $(idf.py --version 2>&1 | head -1)"
echo ""

# ---- 2. Run the build ------------------------------------------------------
# CMake will auto-generate placeholder secrets if sops is unavailable.
echo "Building $DEVICE_DIR ..."
if idf.py -C "$DEVICE_DIR" build 2>&1; then
    pass "idf.py build succeeded"
else
    fail "idf.py build failed"
fi

# ---- 3. Binary existence ---------------------------------------------------
if [[ -f "$BINARY" ]]; then
    pass "firmware binary exists: $BINARY"
else
    fail "firmware binary not found: $BINARY"
fi

# ---- 4. Binary size sanity check -------------------------------------------
if [[ -f "$BINARY" ]]; then
    SIZE=$(stat -c%s "$BINARY")
    if [[ "$SIZE" -ge "$MIN_BINARY_SIZE" ]]; then
        pass "binary size ${SIZE} bytes >= minimum ${MIN_BINARY_SIZE} bytes"
    else
        fail "binary size ${SIZE} bytes is suspiciously small (min ${MIN_BINARY_SIZE})"
    fi
fi

# ---- 5. esptool image_info --------------------------------------------------
if [[ -f "$BINARY" ]]; then
    echo ""
    echo "--- esptool.py image_info ---"
    if esptool.py --chip esp32c6 image_info "$BINARY" 2>&1; then
        pass "esptool.py image_info: image structure valid"
    else
        fail "esptool.py image_info: image validation failed"
    fi
fi

# ---- 6. ELF symbol checks (RISC-V toolchain for ESP32-C6) ------------------
if [[ -f "$ELF" ]]; then
    echo ""
    echo "--- ELF symbol checks ---"
    # Capture nm output once to avoid SIGPIPE under set -o pipefail when grep -q
    # exits early after finding its first match.
    NM_OUT=$(riscv32-esp-elf-nm "$ELF" 2>/dev/null)
    for sym in app_main ot_manager_init ot_manager_deinit ha_client_init ha_post; do
        if grep -q " T ${sym}\b\| t ${sym}\b" <<< "$NM_OUT"; then
            pass "symbol present: $sym"
        else
            fail "symbol missing: $sym"
        fi
    done
fi

# ---- Summary ---------------------------------------------------------------
echo ""
echo "==============================="
echo "Build tests: $PASS passed, $FAIL failed"
echo "==============================="

[[ "$FAIL" -eq 0 ]]
