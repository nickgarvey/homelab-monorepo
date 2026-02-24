#!/usr/bin/env bash
#
# Build verification test for devices/garage-opener.
#
# Checks that the firmware image:
#   1. Compiles successfully with ESP-IDF
#   2. Produces a non-trivially-sized .bin
#   3. Passes esptool.py image_info (validates image header, app descriptor,
#      segment checksums)
#
# Requires the ESP-IDF environment to be active (run via: nix develop --command
# bash tests/test_build.sh, or from within the flake devShell).
#
# Exit codes: 0 = all checks passed, non-zero = failure.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEVICE_DIR="$REPO_ROOT/devices/garage-opener"
BUILD_DIR="$DEVICE_DIR/build"
SECRETS_FILE="$DEVICE_DIR/include/secrets/ha_key.h"
BINARY="$BUILD_DIR/garage_door_opener.bin"

# Minimum expected binary size in bytes. A minimal ESP32-S2 IDF app is
# several hundred KB; anything smaller suggests a truncated or empty image.
MIN_BINARY_SIZE=131072   # 128 KiB

PASS=0
FAIL=0

pass() { echo "[PASS] $1"; ((PASS++)) || true; }
fail() { echo "[FAIL] $1"; ((FAIL++)) || true; }

echo "=== Build verification: garage-opener ==="
echo ""

# ---- 1. Ensure idf.py is available ----------------------------------------
if ! command -v idf.py &>/dev/null; then
    echo "ERROR: idf.py not found. Run inside the Nix devShell:"
    echo "  nix develop --command bash tests/test_build.sh"
    exit 1
fi
echo "IDF version: $(idf.py --version 2>&1 | head -1)"
echo ""

# ---- 2. Provide a placeholder secrets file if not present ------------------
CREATED_SECRETS=false
if [[ ! -f "$SECRETS_FILE" ]]; then
    echo "No secrets/ha_key.h found; creating build-time placeholder."
    mkdir -p "$(dirname "$SECRETS_FILE")"
    cat > "$SECRETS_FILE" <<'EOF'
#ifndef SECRETS_H
#define SECRETS_H
/* Placeholder key inserted by test_build.sh for build verification only. */
const char* HA_API_KEY = "build-test-placeholder-key";
#endif
EOF
    CREATED_SECRETS=true
fi

# ---- 3. Run the build -------------------------------------------------------
echo "Building $DEVICE_DIR ..."
if idf.py -C "$DEVICE_DIR" build 2>&1; then
    pass "idf.py build succeeded"
else
    fail "idf.py build failed"
fi

# ---- 4. Binary existence ---------------------------------------------------
if [[ -f "$BINARY" ]]; then
    pass "firmware binary exists: $BINARY"
else
    fail "firmware binary not found: $BINARY"
fi

# ---- 5. Binary size sanity check -------------------------------------------
if [[ -f "$BINARY" ]]; then
    SIZE=$(stat -c%s "$BINARY")
    if [[ "$SIZE" -ge "$MIN_BINARY_SIZE" ]]; then
        pass "binary size ${SIZE} bytes >= minimum ${MIN_BINARY_SIZE} bytes"
    else
        fail "binary size ${SIZE} bytes is suspiciously small (min ${MIN_BINARY_SIZE})"
    fi
fi

# ---- 6. esptool image_info --------------------------------------------------
if [[ -f "$BINARY" ]]; then
    echo ""
    echo "--- esptool.py image_info ---"
    if esptool.py --chip esp32s2 image_info "$BINARY" 2>&1; then
        pass "esptool.py image_info: image structure valid"
    else
        fail "esptool.py image_info: image validation failed"
    fi
fi

# ---- 7. ELF symbol checks --------------------------------------------------
ELF="$BUILD_DIR/garage_door_opener.elf"
if [[ -f "$ELF" ]]; then
    echo ""
    echo "--- ELF symbol checks ---"
    for sym in app_main wifi_manager_init ha_client_init neopixel_init neopixel_set wifi_manager_connected; do
        if xtensa-esp32s2-elf-nm "$ELF" 2>/dev/null | grep -q " T $sym\b\| t $sym\b"; then
            pass "symbol present: $sym"
        else
            fail "symbol missing: $sym"
        fi
    done
fi

# ---- Cleanup ---------------------------------------------------------------
if [[ "$CREATED_SECRETS" == true ]]; then
    rm -f "$SECRETS_FILE"
fi

# ---- Summary ---------------------------------------------------------------
echo ""
echo "==============================="
echo "Build tests: $PASS passed, $FAIL failed"
echo "==============================="

[[ "$FAIL" -eq 0 ]]
