# flash.py Laundry-Detector Support — Implementation Tracker

## Features implemented

1. [x] VID/PID-based port detection — distinguish ESP-Prog-2 (303a:1002) from Feather (303a:1001)
2. [x] OpenOCD JTAG reset integration — wake device from deep sleep before flashing
3. [x] ESP-Prog-2 method — `--method esp-prog-2` uses VID/PID (not FT2232 ttyUSB)
4. [x] deep sleep detection — auto-detects from source code (esp_deep_sleep_start)
5. [x] Tests — 36 unit tests in scripts/test_flash.py, all passing
6. [x] QR code display after flash — reads from matter-pairing.json (static, no serial capture)
7. [x] Monitor reconnect on USB disconnect — monitor_with_reconnect() for deep sleep devices

## Test results

```
Ran 36 tests in 0.010s — OK
```

## Files changed
- scripts/flash.py — added find_port_by_vid_pid, device_uses_deep_sleep, jtag_reset_to_wake, esp-prog-2 method, get_pairing_info, print_pairing_info, monitor_with_reconnect
- scripts/test_flash.py — 36 unit tests
- devices/laundry-detector/matter-pairing.json — Matter pairing codes
- devices/freezer-temp-sensor/matter-pairing.json — Matter pairing codes
- docs/flash-script.md — updated with laundry-detector and esp-prog-2 docs

## TODO

- [x] Validate interrupt wake logic — LIS3DH INT1 on GPIO7 confirmed waking ESP32-C6 from deep sleep on vibration (HPF enabled to filter gravity)
