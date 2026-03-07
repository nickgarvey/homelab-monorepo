# Flashing Workarounds for ESP32-C6 on NixOS

Four issues came up when flashing and monitoring the ESP32-C6 Feather for the first time. Documented here so they don't need to be debugged again.

---

## 1. USB JTAG requires a udev rule for non-root access

### Symptom

OpenOCD fails with:

```
Error: libusb_open() failed with LIBUSB_ERROR_ACCESS
Error: esp_usb_jtag: could not find or open device!
```

The `/dev/bus/usb/003/XXX` node for the device is owned `root:root` with `660` permissions, so normal users cannot open it via libusb.

### Root cause

No udev rule is installed for Espressif's USB JTAG device (VID `303a`, PID `1001`). The OpenOCD package ships a rule file at:

```
$IDF_PATH/../openocd-esp32/share/openocd/contrib/60-openocd.rules
```

but this file lives in the Nix store and is not automatically installed into `/etc/udev/rules.d/`.

### Permanent fix (NixOS)

Add this to `configuration.nix`:

```nix
services.udev.extraRules = ''
  # Espressif USB JTAG/serial debug unit (ESP32-C6, ESP32-H2, etc.)
  ATTRS{idVendor}=="303a", ATTRS{idProduct}=="1001", MODE="660", GROUP="dialout", TAG+="uaccess"
'';
```

Then rebuild and switch:

```bash
sudo nixos-rebuild switch
```

The `TAG+="uaccess"` line grants access to the logged-in seat user automatically via systemd-logind, so the GROUP entry is a fallback for non-seat sessions.

### Temporary fix (survives until next plug/unplug)

```bash
sudo chmod o+rw /dev/bus/usb/$(lsusb -d 303a:1001 | grep -oP 'Bus \K[0-9]+'):$(lsusb -d 303a:1001 | grep -oP 'Device \K[0-9]+')
```

---

## 2. Cursor IDE holds `/dev/ttyACM0` exclusively

### Symptom

`idf.py flash` (or `esptool.py` directly) fails with:

```
A fatal error occurred: Could not open /dev/ttyACM0, the port is busy or doesn't exist.
([Errno 16] could not open port /dev/ttyACM0: [Errno 16] Device or resource busy: '/dev/ttyACM0')
```

This happens even though no terminal has the port open and ModemManager is inactive.

### Root cause

The Cursor IDE (`helium` process) opens `/dev/ttyACM0` automatically when the device is plugged in, likely via its built-in serial monitor feature or an ESP-IDF extension. It holds the port open for the lifetime of the IDE session without reading from it:

```bash
# Confirm: find which process holds the port
for pid in $(ls /proc | grep '^[0-9]'); do
  for fd in $(ls /proc/$pid/fd 2>/dev/null); do
    t=$(readlink /proc/$pid/fd/$fd 2>/dev/null)
    [ "$t" = "/dev/ttyACM0" ] && echo "PID $pid fd $fd comm=$(cat /proc/$pid/comm 2>/dev/null)"
  done
done 2>/dev/null
```

The `fdinfo` entry shows `O_RDWR | O_NOCTTY` with no activity on the fd (confirmed via `strace`).

### Workaround

Inject a `close()` call into the holding process via GDB. This only closes that one file descriptor — the IDE continues running normally.

```bash
# Install gdb temporarily
nix-shell -p gdb --run "echo ready"

# Find the PID and fd (see scan above, typically fd 102 in helium)
PID=<helium pid>
FD=<fd number, e.g. 102>

sudo /nix/store/.../gdb-*/bin/gdb --batch \
  -ex "attach $PID" \
  -ex "call (int)close($FD)" \
  -ex "detach" \
  -ex "quit"
```

The return value `$1 = 0` confirms the close succeeded. The port is then immediately usable by esptool.py or OpenOCD.

After the device is unplugged and replugged, Cursor will claim the port again and this procedure needs to be repeated. The permanent fix is to disable whatever Cursor feature is doing this (the ESP-IDF VS Code extension's "Auto monitor" setting, if installed).

### Preferred monitor command

For this board, `idf.py monitor` is more reliable than raw `cat /dev/ttyACM0`, especially right after reset:

```bash
cd devices/led-blinker
nix develop ../../ --command idf.py -p /dev/ttyACM0 monitor
```

This avoids a few cases where attaching with `cat` too late makes it look like the device is silent, when it is really just not emitting logs at that exact moment.

---

## 3. OpenOCD JTAG reset puts ESP32-C6 into ROM download mode

### Symptom

After flashing via OpenOCD JTAG, the device does not run the application. Instead, the serial port outputs:

```
ESP-ROM:esp32c6-20220919
Build:Sep 19 2022
rst:0x18 (JTAG_CPU),boot:0x4 (DOWNLOAD(USB/UART0/SDIO_FEI_FEO))
Saved PC:0x20000844
waiting for download
```

### Root cause

The OpenOCD `reset run` command issues a JTAG CPU reset (`rst:0x18`). On the ESP32-C6, this type of reset re-samples the boot strapping pins. With the USB JTAG interface still active at reset time, the chip reads boot mode `0x4` (DOWNLOAD mode) instead of `0x0` (normal SPI flash boot).

A secondary consequence: if you then re-run `program_esp` against a device already in download mode, OpenOCD reports incorrect flash mappings (smaller sizes) because it is reading the ROM's download-mode view of flash, not the app's. The flash is written to the wrong logical context.

### Workaround

Do not use OpenOCD to reset the device after flashing. Instead, flash all three binaries using `esptool.py` over the CDC-ACM serial port using the serial download protocol. The ROM download mode that JTAG reset accidentally triggered is exactly what `esptool.py` needs to connect. After writing, `esptool.py --after hard_reset` resets the chip via the RTS line, which produces a clean power-on-equivalent boot that runs the application.

```bash
BUILD=devices/led-blinker/build
ESPTOOL_PY=$IDF_PATH/components/esptool_py/esptool/esptool.py

python $ESPTOOL_PY \
  --chip esp32c6 \
  -p /dev/ttyACM0 \
  -b 460800 \
  --before default_reset \
  --after hard_reset \
  write_flash \
  --flash_mode dio --flash_freq 80m --flash_size 4MB \
  0x0  $BUILD/bootloader/bootloader.bin \
  0x8000 $BUILD/partition_table/partition-table.bin \
  0x10000 $BUILD/led_blinker.bin
```

This is also just `idf.py -p /dev/ttyACM0 flash` if the port is available.

### Alternative: avoid JTAG reset entirely

Use OpenOCD only for initial programming with `program_esp ... exit` (no `reset`), then trigger a reset externally (press the RST button, or power cycle). The application will boot normally because there is no active JTAG connection holding the chip in a special state at reset time.

---

## 4. Flash size mismatch warning from a stale `sdkconfig`

### Symptom

The boot log shows:

```text
W (...) spi_flash: Detected size(4096k) larger than the size in the binary image header(2048k). Using the size in the binary image header.
```

This is a warning, not an immediate functional failure, but it means the image header does not match the module's real flash size.

### Root cause

The Adafruit ESP32-C6 Feather module has **4 MB** of flash, but the project was initially built with a **2 MB** image header.

For `devices/led-blinker`, changing only `sdkconfig.defaults` was not enough once `sdkconfig` already existed. ESP-IDF kept using the values recorded in `sdkconfig`, so the generated image and `build/flash_args` continued to use:

```text
--flash_size 2MB
```

### Fix

Set the flash size to 4 MB in both the defaults and the active config:

```text
CONFIG_ESPTOOLPY_FLASHSIZE_4MB=y
CONFIG_ESPTOOLPY_FLASHSIZE="4MB"
```

If `sdkconfig` already exists, either:

1. update `sdkconfig` directly, or
2. delete/regenerate it so the new defaults are applied cleanly

After rebuilding, confirm the generated flash arguments now say:

```text
--flash_size 4MB
```

On this board, the correct module size is 4 MB, so this warning should not appear once the new image has been flashed.

---

## Recommended flashing flow (after the udev rule is in place)

1. Ensure `/dev/ttyACM0` is free (check with `fuser /dev/ttyACM0` or the scan above; close the Cursor fd if needed).
2. Use `idf.py -p /dev/ttyACM0 flash` from inside `nix develop`. This uses esptool.py's serial download protocol, which works reliably.
3. Use `idf.py -p /dev/ttyACM0 monitor` to watch output.

OpenOCD is only needed for debugging (breakpoints, register inspection). For day-to-day flashing, stick to the serial path.
