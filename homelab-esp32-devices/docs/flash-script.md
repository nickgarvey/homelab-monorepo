# Flash Script

`scripts/flash.py` builds firmware via Nix and flashes it using `esptool.py`.

Run `nix develop --command python3 scripts/flash.py --help` for the full option reference.

---

## Prerequisites

- Enter the Nix devshell first (`nix develop`) — it provides `esptool.py` and `esp_idf_monitor`
- For USB method: udev rule for ESP32-C6 USB-JTAG (see `docs/flashing-workarounds.md`)
- For esp-prog method: FT2232-based esp-prog connected via USB

---

## Architecture

Build and flash are decoupled:

1. **Build** — `nix build .#<package>` produces a read-only store path containing the `.bin`, `.elf`, and `flasher_args.json`
2. **Flash** — `esptool.py write_flash` reads offsets from `flasher_args.json` and writes binaries directly from the Nix store (no `idf.py` at flash time, no writable build dir needed)
3. **Monitor** — `esp_idf_monitor` attaches to serial with the `.elf` for address decoding

Nix builds are dispatched to the remote builder (`desktop-nixos`) and cached. Rebuilds only happen when firmware source files change — `scripts/`, `tests/`, and `docs/` are excluded from the source hash via `cleanSrc` in `flake.nix`.

---

## Common Workflows

All commands assume you are at the repo root.

### Flash a device (USB)

```bash
nix develop --command python3 scripts/flash.py devices/freezer-temp-sensor
```

Builds, flashes, then opens serial monitor. The Matter QR code and manual pairing code appear in the monitor on boot.

### Flash without monitor

```bash
nix develop --command python3 scripts/flash.py --no-monitor devices/freezer-temp-sensor
```

### Flash with fake temperature sensor

```bash
nix develop --command python3 scripts/flash.py --fake-sensor devices/freezer-temp-sensor
```

Builds the `freezer-temp-sensor-fake` Nix package which uses a sine-wave generator instead of the real DS18B20. Useful for testing Matter integration without hardware.

### Flash via esp-prog

```bash
nix develop --command python3 scripts/flash.py --method esp-prog devices/freezer-temp-sensor
```

Flashes via `/dev/ttyUSB0`, monitors via `/dev/ttyUSB1`.

### Erase flash before flashing

Clears NVS, commissioning data, and all stored state. Required when re-commissioning a Matter device or recovering from corrupted NVS.

```bash
nix develop --command python3 scripts/flash.py --erase devices/freezer-temp-sensor
```

### Override serial port

```bash
nix develop --command python3 scripts/flash.py --port /dev/ttyACM1 devices/freezer-temp-sensor
```

---

## Nix Packages

| Package | Description |
|---|---|
| `freezer-temp-sensor` | Real DS18B20 temperature sensor firmware |
| `freezer-temp-sensor-fake` | Fake sine-wave sensor (no hardware needed) |
| `garage-opener` | Garage door opener firmware |

Both freezer variants are produced by `makeFreezerFirmware` in `flake.nix` with a `useFakeSensor` parameter.

---

## Troubleshooting

**`no /dev/ttyACM* device found`**
- Check the USB cable is plugged in
- Verify the udev rule is installed (`docs/flashing-workarounds.md`)
- Run `sudo dmesg | tail` to see if the device was detected

**`esp-prog requires two /dev/ttyUSB* ports`**
- Check the esp-prog is plugged in and both FT2232 channels are enumerated
- Run `ls /dev/ttyUSB*` to confirm

**`Permission denied: /dev/ttyACM0`**
- Add your user to the `dialout` group: `sudo usermod -aG dialout $USER` (re-login required)

**Stale entities in Home Assistant after reflash**
- HA caches endpoint/cluster data from commissioning time
- Removing an endpoint from firmware won't remove the HA entity automatically
- Delete the stale entity in HA: Settings > Devices > find entity > delete
- For name changes or major restructuring: remove device from HA and re-commission
