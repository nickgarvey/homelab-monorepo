#!/usr/bin/env bash
# Builds fw_v09_dmgsplit.bin: AYANEO's Pocket DMG controller firmware v09 with
# the mouse-mode patch (see README.md). Usage: ./build.sh [out-dir]
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
out=$(realpath -m "${1:-$here/out}")
mkdir -p "$out"
stock_url=https://ayaneo-1305909189.cos.accelerate.myqcloud.com/android/AR02/ControllerFirmwareUpdate/dfu_update_b.txt
stock_sha=50e9ce8cb93e12b5216d46932f1adf31e8355c6b9c1cc3cd782f6a910bb32ed1
patched_sha=ef7cfdc9b8e17e1ded974d972dfe30b7c20f8c5bc6d3e808dfc8c6dc9ff6cee9
base=$((0x0800C000))

[ -f "$out/fw_v09_stock.bin" ] || curl -fsSL -o "$out/fw_v09_stock.bin" "$stock_url"
echo "$stock_sha  $out/fw_v09_stock.bin" | sha256sum -c --quiet

# Each snippet is assembled with its section starting at <origin>; <offset>/<len>
# pick the bytes that land at <address> in the image.
#        name   origin      address     offset                  len
patches="enter  0x08015bc0  0x08015c1c  $((0x08015c1c-0x08015bc0)) 24
         exit   0x08015b36  0x08015b36  0                       10
         b1     0x0801bdc0  0x0801bdc0  0                       2
         c      0x08016bc8  0x08016bc8  0                       32
         cave   0x08015c34  0x08015c34  0                       18
         tramp  0x08015c34  0x08016e68  $((0x08016e68-0x08015c34)) 4"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cp "$out/fw_v09_stock.bin" "$work/fw.bin"
while read -r name _origin addr off len; do
  nix shell nixpkgs#gcc-arm-embedded -c sh -c \
    "arm-none-eabi-as -mcpu=cortex-m4 -o '$work/$name.o' '$here/$name.s' && arm-none-eabi-objcopy -O binary '$work/$name.o' '$work/$name.raw'"
  dd if="$work/$name.raw" of="$work/fw.bin" bs=1 skip="$off" count="$len" \
    seek=$((addr - base)) conv=notrunc status=none
done <<<"$patches"
# 0x0801be04: beq -> nop (bf00)
printf '\x00\xbf' | dd of="$work/fw.bin" bs=1 seek=$((0x0801be04 - base)) conv=notrunc status=none

echo "$patched_sha  $work/fw.bin" | sha256sum -c --quiet
cp "$work/fw.bin" "$out/fw_v09_dmgsplit.bin"
echo "$out/fw_v09_dmgsplit.bin"
