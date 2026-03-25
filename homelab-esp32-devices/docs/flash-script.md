# Flash Script

`scripts/flash.py` is the repo-wide tool for building, flashing, and monitoring ESP32 devices.

Run `./scripts/flash.py --help` for the full option reference.

---

## Prerequisites

- `idf.py` on your `PATH` — enter the Nix devshell first (see below)
- For USB method: udev rule for ESP32-C6 USB-JTAG (see `docs/flashing-workarounds.md`)
- For esp-prog method: FT2232-based esp-prog connected via USB

### Running inside the Nix devshell

`idf.py` is provided by the Nix flake. You must be inside the devshell for it to be
on your `PATH`.

**Interactive shell** — enter once, then run flash commands normally:
```bash
nix develop
./scripts/flash.py --matter-name "Freezer Sensor" devices/freezer-temp-sensor
```

**One-shot** — run without entering an interactive shell:
```bash
nix develop --command ./scripts/flash.py --matter-name "Freezer Sensor" devices/freezer-temp-sensor
```

---

## Hardware Setup

### USB (default)

The board's USB connector plugs directly into your machine. The ESP32-C6 presents a
CDC-ACM serial device at `/dev/ttyACM0`.

```
Board USB ──► /dev/ttyACM0  (flash + monitor)
```

### esp-prog

The esp-prog has an FT2232H chip with two channels. Connect both the esp-prog and the
board's UART pins (TX/RX):

```
esp-prog Channel A (ttyUSB0) ──► flash via esptool
esp-prog Channel B (ttyUSB1) ──► serial monitor
```

---

## Common Workflows

### Flash a Matter device (USB)

```bash
./scripts/flash.py --matter-name "Freezer Sensor" devices/freezer-temp-sensor
```

Builds (incremental via Ninja), flashes, then starts the serial monitor. The Matter QR
code and manual pairing code appear in the monitor output on first boot.

### Flash a Matter device (esp-prog)

```bash
./scripts/flash.py --method esp-prog --matter-name "Freezer Sensor" devices/freezer-temp-sensor
```

Flashes via `/dev/ttyUSB0`, monitors via `/dev/ttyUSB1`.

### Flash a non-Matter device

```bash
./scripts/flash.py devices/led-blinker
./scripts/flash.py devices/garage-opener
```

No `--matter-name` needed or allowed.

### Erase flash before flashing

Clears NVS, commissioning data, and all stored state. Required when re-commissioning a
Matter device or recovering from a corrupted NVS partition.

```bash
./scripts/flash.py --erase --matter-name "Freezer Sensor" devices/freezer-temp-sensor
```

### Flash without opening monitor

```bash
./scripts/flash.py --no-monitor --matter-name "Freezer Sensor" devices/freezer-temp-sensor
```

### Override detected serial port

```bash
./scripts/flash.py --port /dev/ttyACM1 --matter-name "Freezer Sensor" devices/freezer-temp-sensor
```

---

## Matter Device Name

`--matter-name` sets `CONFIG_DEVICE_PRODUCT_NAME` at build time. It is baked into the
firmware and reported to Home Assistant via the Matter Basic Information cluster.

Changing the name on an already-commissioned device requires removing the device from HA
and re-commissioning after reflashing with the new name.

When `--matter-name` is provided, `sdkconfig` is deleted before building so the new name
is picked up from defaults. This triggers a full rebuild (same cost as a clean build).

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
- Or run with `sudo` as a one-off

**Name not showing in Home Assistant after reflash**
- HA caches the device name from commissioning time
- Remove the device from HA (Settings → Devices → Matter → Remove) and re-commission
