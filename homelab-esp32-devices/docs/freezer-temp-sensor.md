# Freezer Temperature Sensor

ESP32-C6 based Matter temperature sensor for freezer monitoring.

---

## Hardware

- **MCU:** ESP32-C6FH4 (QFN32, rev v0.2)
- **Sensor:** DS18B20 digital thermometer on a wire probe, connected to GPIO 5
- **Debug LED:** NeoPixel on GPIO 8
- **Radio:** 802.15.4 (Thread) — native radio, no external module
- **Connectivity:** Thread → Matter → Home Assistant

## Firmware Variants

| Variant | Nix Package | Sensor | Report Interval |
|---|---|---|---|
| Production | `freezer-temp-sensor` | Real DS18B20 | 60 seconds |
| Development | `freezer-temp-sensor-fake` | Sine wave generator | 5 seconds |

Switch between them at flash time with `--fake-sensor` flag — no code changes needed.

---

## DS18B20 Reader (`ds18b20_reader.c`)

The 1-Wire bus and DS18B20 device handle are initialized once on first read and reused for all subsequent reads. This is critical because:

- **ESP32-C6 has limited RMT channels** (used by the 1-Wire bus driver)
- Allocating a new bus each read cycle exhausts RMT channels after one successful read
- All subsequent reads fail with `rmt_tx_register_to_group: no free tx channels`

The `ds18b20_reader_reset()` function is provided for testing — it clears the cached handle so the next read re-initializes.

---

## Matter Integration

### Endpoints

| Endpoint | Cluster | Description |
|---|---|---|
| 1 | TemperatureMeasurement | Temperature in 0.01°C units |

### Reporting Behavior

The firmware updates the Matter attribute every 60 seconds, but Home Assistant may not show updates that frequently. Matter subscription reporting only sends data when:

- The value changes by more than the configured delta, OR
- The max reporting interval expires (typically 5–15 minutes)

If the temperature is stable, HA may only update every few minutes even though the sensor reads every 60s.

### Pairing

On boot, the firmware prints a QR code and manual pairing code to serial:

```
SetupQRCode: [MT:Y.K90AFN00KA0648G00]
Manual pairing code: [34970112332]
```

### CHIPProjectConfig.h Overrides

| Define | Value | Why |
|---|---|---|
| `CHIP_DEVICE_CONFIG_DEVICE_PRODUCT_NAME` | from Kconfig | Shows in HA device info |
| `CHIP_DEVICE_CONFIG_DEVICE_VENDOR_NAME` | from Kconfig | Shows in HA device info |
| `CHIP_DEVICE_CONFIG_DEFAULT_DEVICE_HARDWARE_VERSION_STRING` | `"v1.0"` | Overrides SDK default `"TEST_VERSION"` |

---

## Thread Credentials

Thread network credentials are injected at build time from SOPS-encrypted secrets (`secrets/thread_auth.sops.yaml`). The Nix build generates a placeholder when `sops` is not available (CI/test builds).

The firmware calls `inject_thread_dataset()` at startup to join the Thread network without commissioning the Thread layer through Matter.

---

## Debug LED Colors

| Color | Meaning |
|---|---|
| White | Boot / initialization |
| Blue | Reading sensor |
| Green | Successful read + Matter update |
| Amber | Waiting for commissioning |
| Red | Error (read failure or Matter update failure) |
