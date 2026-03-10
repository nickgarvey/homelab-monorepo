# ESP-Prog-2 Usage

The ESP-Prog-2 (VID `303a`, PID `1002`) is Espressif's USB JTAG/UART programmer. It exposes two interfaces over a single USB connection:

| Interface | Purpose |
|---|---|
| JTAG | Debugging via OpenOCD (breakpoints, register inspection, flashing) |
| UART | Serial flashing via esptool.py and serial monitor |

Both interfaces require a target ESP32 board connected to the programmer's header pins.

---

## Prerequisites

### udev rule (NixOS)

OpenOCD accesses the device via libusb directly (not the `/dev/ttyACM*` node), which requires a udev rule. This is handled by `modules/esp-prog-udev.nix` in the nix-configs repo. Without it you will see:

```
Error: libusb_open() failed with LIBUSB_ERROR_ACCESS
Error: esp_usb_jtag: could not find or open device!
```

After adding the module and rebuilding, the device is accessible without sudo.

### Dev shell

All commands below should be run inside `nix develop`:

```bash
nix develop
```

---

## Identify the ports

When plugged in, the ESP-Prog-2 appears as two `/dev/ttyACM*` nodes. Identify them:

```bash
udevadm info /dev/ttyACM0 | grep -E "ID_MODEL|ID_USB_INTERFACE_NUM"
udevadm info /dev/ttyACM1 | grep -E "ID_MODEL|ID_USB_INTERFACE_NUM"
```

`INTERFACE_NUM=00` is typically JTAG, `INTERFACE_NUM=02` is typically UART.

---

## Verify the programmer is connected (no target required)

Use OpenOCD with just the interface config. This confirms the USB connection works without needing a target board attached:

```bash
openocd \
  -s $OPENOCD_SCRIPTS \
  -f interface/esp_usb_bridge.cfg \
  -c "init; exit"
```

Expected output when working:

```
Info : esp_usb_jtag: serial (206EF1ABB748)
Info : esp_usb_jtag: Device found. Base speed 4800KHz, div range 1 to 1
```

The `LIBUSB_ERROR_TIMEOUT` errors that follow are expected — they indicate no target board is connected to the JTAG pins.

---

## Connect to a target via JTAG (OpenOCD)

Replace `<target>.cfg` with the appropriate target, e.g. `target/esp32c6.cfg`:

```bash
openocd \
  -s $OPENOCD_SCRIPTS \
  -f interface/esp_usb_bridge.cfg \
  -f target/<target>.cfg
```

OpenOCD will start a GDB server on port `3333` and a telnet interface on port `4444`.

### Useful OpenOCD one-liners

```bash
# Read chip info
openocd -s $OPENOCD_SCRIPTS \
  -f interface/esp_usb_bridge.cfg \
  -f target/<target>.cfg \
  -c "init; reset halt; esp chip_id; exit"

# Flash via JTAG
openocd -s $OPENOCD_SCRIPTS \
  -f interface/esp_usb_bridge.cfg \
  -f target/<target>.cfg \
  -c "program_esp <build_dir>/<app>.bin 0x10000 exit"
```

---

## Flash and monitor via UART (esptool / idf.py)

The UART port is used by esptool.py for serial flashing. Identify the UART port (typically the higher-numbered `/dev/ttyACM*`):

```bash
# Get chip info
esptool.py --port /dev/ttyACM1 chip_id

# Flash a project
idf.py -p /dev/ttyACM1 flash

# Monitor serial output
idf.py -p /dev/ttyACM1 monitor

# Flash and monitor in one step
idf.py -p /dev/ttyACM1 flash monitor
```

> If the port is busy, see `docs/flashing-workarounds.md` for the Cursor IDE port-hold fix.

---

## OPENOCD_SCRIPTS path note

The `$OPENOCD_SCRIPTS` environment variable is set by the dev shell but may have a trailing `export` appended due to a bug in the esp-idf Nix package. If `-s $OPENOCD_SCRIPTS` fails, use the explicit path:

```bash
-s $(openocd --version 2>&1; find /nix/store -path "*/openocd-esp32*/share/openocd/scripts" -type d 2>/dev/null | head -1)
```

Or hardcode it from the output of:

```bash
nix develop --command sh -c 'echo $OPENOCD_SCRIPTS'
```
