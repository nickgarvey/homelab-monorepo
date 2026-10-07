# Debugger Bring-Up: ESP32-C6 with ESP-Prog-2

How we got the freezer-temp-sensor firmware running and producing serial output using a second ESP32-C6 board and the ESP-Prog-2 debugger.

---

## Problem

The original ESP32-C6 board (MAC `58:E6:C5:E5:D1:A8`) with the DS18B20 sensor attached produced **zero serial output** after flashing. Despite `CONFIG_ESP_CONSOLE_USB_SERIAL_JTAG=y` and `CONFIG_OPENTHREAD_CONSOLE_ENABLE=n`, nothing appeared on `/dev/ttyACM0`.

## Solution

Attached an ESP-Prog-2 debugger to a second Adafruit ESP32-C6 Feather (MAC `58:E6:C5:E5:EF:1C`) without a temperature sensor. Added a fake sine-wave sensor option so the firmware runs without real hardware.

## Device setup

Two devices appear when both boards are connected:

| Port | Device | Interface | Purpose |
|------|--------|-----------|---------|
| `/dev/ttyACM0` | ESP32-C6 built-in USB-JTAG | `00` | Flashing and serial monitor |
| `/dev/ttyACM1` | ESP-Prog-2 | `00` | JTAG debugging (OpenOCD) |

Identify ports with:
```bash
udevadm info /dev/ttyACM0 | grep -E "ID_MODEL|ID_USB_INTERFACE_NUM"
```

## Fake temperature sensor

### Config option

`devices/freezer-temp-sensor/main/Kconfig.projbuild` adds a menu option:

```
CONFIG_USE_FAKE_TEMP_SENSOR=y
```

When enabled:
- Replaces DS18B20 reads with a sine wave: center=-18°C, amplitude=5°C, period=300s
- Shortens report interval from 60s to 5s for faster iteration
- Removes the `espressif__onewire_bus` and `espressif__ds18b20` component dependencies

### Unit tests

6 tests in `tests/test_fake_temp_sensor.c` verify the sine wave generator:
- Value at t=0 equals center
- Value at quarter period equals center + amplitude (peak)
- Value at three-quarter period equals center - amplitude (trough)
- Full period returns to center
- `reset_time()` resets to t=0
- Re-init resets elapsed time

All tests written red-green style against `fake_temp_sensor.c` / `.h`.

## Build and flash workflow

```bash
# Enter dev shell
nix develop

# Enable fake sensor in sdkconfig.defaults (already done):
#   CONFIG_USE_FAKE_TEMP_SENSOR=y

# Clean build (delete sdkconfig to pick up new Kconfig option)
rm -f devices/freezer-temp-sensor/sdkconfig
idf.py build

# Flash via the board's built-in USB (NOT the ESP-Prog-2)
idf.py -p /dev/ttyACM0 flash

# Monitor serial output
idf.py -p /dev/ttyACM0 monitor
```

Or capture programmatically with pyserial:
```python
import serial, time

ser = serial.Serial('/dev/ttyACM0', 115200, timeout=1)
# Reset board via RTS toggle
ser.setRTS(True); time.sleep(0.1); ser.setRTS(False)
time.sleep(0.1); ser.reset_input_buffer()

while True:
    line = ser.readline()
    if line:
        print(line.decode('utf-8', errors='replace'), end='')
```

## Expected serial output

```
I (10922) freezer: Temperature (fake): -16.45°C
E (10922) ha_client: POST /api/states/sensor.freezer_temperature failed
E (10922) freezer: HA POST failed
I (15922) freezer: Temperature (fake): -15.97°C
I (20922) freezer: Temperature (fake): -15.50°C
I (25922) freezer: Temperature (fake): -15.06°C
I (30922) freezer: Temperature (fake): -14.65°C
```

The temperature sine-waves between -23°C and -13°C over a 5-minute period. HA POST errors are expected when using placeholder credentials (no SOPS decrypt available).

## Key findings

1. **Flashing works via the board's built-in USB** (`ttyACM0`), not through the ESP-Prog-2. Use `idf.py -p /dev/ttyACM0 flash`.

2. **Serial output works** with `CONFIG_ESP_CONSOLE_USB_SERIAL_JTAG=y` and `CONFIG_OPENTHREAD_CONSOLE_ENABLE=n` in sdkconfig.defaults.

3. **The OpenThread join timeout adds ~30s to boot** (configured as `ot_manager_init(&creds, 30000)`). The first temperature reading appears after this timeout when no Thread network is available.

4. **RTS toggle resets the board** — pyserial's `setRTS(True/False)` triggers a hardware reset, useful for capturing the full boot sequence.

5. **The original board's lack of serial output** remains an open issue — possibly a hardware difference, damaged USB-JTAG peripheral, or a strapping pin configuration problem on that specific unit.

## Switching back to real sensor

To disable the fake sensor for deployment:

1. Remove `CONFIG_USE_FAKE_TEMP_SENSOR=y` from `sdkconfig.defaults`
2. Delete `sdkconfig` to force reconfigure
3. Rebuild: `idf.py build`

The real DS18B20 code path and 60-second report interval will be active.
