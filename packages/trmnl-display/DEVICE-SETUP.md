# Reconfiguring the TRMNL device's server URL

The TRMNL e-ink device stores its server URL on-device (ESP32 NVS, via the
Arduino `Preferences` library — namespace `data`, key `api_url`). It's set
once during WiFi pairing and is **not** something the server can push to the
device; it has to be rewritten through the device's own setup flow.

This firmware matches [skorokithakis/trmnl-firmware](https://github.com/skorokithakis/trmnl-firmware)
(`env:trmnl` build, ESP32-C3, native USB-Serial-JTAG — shows up as
`Espressif USB JTAG/serial debug unit`, vendor `303a:1001`).

## How the stored URL gets changed

- `saveApiServer()` (`lib/wificaptive/src/WifiCaptive.cpp`) is the only code
  path that writes the `api_url` preference.
- It's called by `WifiCaptive::startPortal()` after a client POSTs to the
  device's own captive-portal web server.
- A **long button-press** (~5+ seconds) on the device triggers
  `WifiCaptivePortal.resetSettings()` (wipes saved WiFi + `api_url`) and puts
  it into AP mode:
  - SSID: `TRMNL`, open (no password)
  - Gateway/web server: on this unit, `10.254.0.1` (the firmware's default of
    `4.3.2.1` may differ by build/version — check `ip neigh` / `arp` for the
    device's actual softAP MAC/IP after joining its network)
  - `MAX_CLIENTS = 1`, so only one client can join at a time

## Steps to point the device at a new server URL

1. **Long-press the button on the device** (~5s) until the screen shows a
   WiFi-setup screen. This wipes saved WiFi credentials too — you'll be
   resupplying them in the same request.
2. **Join the device's `TRMNL` WiFi network** from a laptop/desktop (a
   secondary WiFi radio works fine — no need to drop your main network
   connection if the machine has more than one interface).
3. **Find the device's gateway IP** (join the AP, then check
   `ip neigh show dev <iface>` or `arp -n` for the entry with the device's
   MAC — same as the USB serial number, plus 1 in the last octet for AP
   mode, e.g. serial `...AE:FC` → AP MAC `...AE:FD`).
4. **POST the new config** to the device:
   ```bash
   curl -X POST http://<device-ip>/connect \
     -H 'Content-Type: application/json' \
     -d '{"ssid":"<your-wifi-ssid>","pswd":"<your-wifi-password>","server":"http://trmnl-display.home.garvey.sh"}'
   ```
   A `200` response echoing `{"ssid":..., "mac":...}` means it was accepted.
   The device saves both the WiFi credentials and the new `api_url`, then
   reboots into normal operation and reconnects to home WiFi.
5. **Verify**: watch `dmesg`/`journalctl -k -f` for the device's USB
   JTAG/serial interface to reappear, confirm the e-ink screen shows live
   dashboard content, and/or check the `trmnl-display` server logs for a
   `/api/setup` hit from the device.
