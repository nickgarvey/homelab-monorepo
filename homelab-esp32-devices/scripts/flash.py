#!/usr/bin/env python3
"""Flash and monitor ESP32 devices in this repo.

Supports USB (direct USB-Serial/JTAG), esp-prog (FT2232 UART), and
esp-prog-2 (ESP-Prog-2 JTAG+UART) flash methods.

Build behaviour:
  Runs `nix build .#<device>` which dispatches to the remote builder (desktop-nixos)
  on first build and uses the binary cache thereafter.  The firmware is flashed
  directly from the Nix store output using esptool.py (no idf.py at flash time).

Usage (from repo root, inside nix develop):
  nix develop --command python3 scripts/flash.py devices/freezer-temp-sensor
  nix develop --command python3 scripts/flash.py --fake-sensor devices/freezer-temp-sensor
  nix develop --command python3 scripts/flash.py --method esp-prog devices/freezer-temp-sensor
  nix develop --command python3 scripts/flash.py --method esp-prog-2 devices/laundry-detector
  nix develop --command python3 scripts/flash.py --erase devices/freezer-temp-sensor
  nix develop --command python3 scripts/flash.py devices/garage-opener
  nix develop --command python3 scripts/flash.py --no-monitor devices/garage-opener
"""

import argparse
import csv
import glob
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parent.parent

# USB VID/PID constants for Espressif devices
VID_ESPRESSIF = "303a"
PID_ESP32_USB_JTAG = "1001"   # Built-in USB-Serial/JTAG on ESP32-C6/S3
PID_ESP_PROG_2 = "1002"       # ESP-Prog-2 programmer
PID_ESP32_S2_CDC = "0002"     # Built-in USB-CDC on ESP32-S2

SYSFS_TTY_BASE = Path("/sys/class/tty")

# Offset of the `nvs` partition in the default partition table.
NVS_OFFSET = "0x9000"

# Mapping from SOPS key names to (NVS namespace, NVS key) for garage-opener.
# NVS keys must be ≤15 chars.
GARAGE_NVS_KEYS = {
    "WIFI_SSID":     ("garage", "wifi_ssid"),
    "WIFI_PASSWORD":  ("garage", "wifi_pass"),
    "HA_BASE_URL":    ("garage", "ha_url"),
    "HA_ENTITY_ID":   ("garage", "ha_entity"),
    "HA_API_KEY":     ("garage", "ha_key"),
}


def die(msg: str) -> None:
    print(f"error: {msg}", file=sys.stderr)
    sys.exit(1)


def run(cmd: list[str], **kwargs) -> None:
    """Run a command, raising SystemExit on failure."""
    print(f"+ {' '.join(cmd)}", flush=True)
    result = subprocess.run(cmd, **kwargs)
    if result.returncode != 0:
        sys.exit(result.returncode)


def _by_id_path_for(tty_name: str) -> str | None:
    """Return the /dev/serial/by-id/ symlink targeting a given tty, or None."""
    by_id = Path("/dev/serial/by-id")
    if not by_id.is_dir():
        return None
    target_dev = Path(f"/dev/{tty_name}").resolve()
    for link in by_id.iterdir():
        if link.resolve() == target_dev:
            return str(link)
    return None


def find_port_by_vid_pid(vid: str, pid: str,
                         sysfs_base: Path | None = None) -> str | None:
    """Find /dev/ttyACM* device matching USB VID:PID via sysfs.

    Walks up the sysfs device tree from each ttyACM to find the USB device
    node with idVendor/idProduct. Prefers a stable /dev/serial/by-id/ path
    when available, falling back to /dev/ttyACM*.
    """
    base = sysfs_base or SYSFS_TTY_BASE
    if not base.exists():
        return None

    for tty_dir in sorted(base.iterdir()):
        name = tty_dir.name
        if not name.startswith("ttyACM"):
            continue

        device_link = tty_dir / "device"
        if not device_link.exists():
            continue

        # Walk up from the device symlink target to find idVendor
        p = device_link.resolve()
        while p != p.parent:
            vid_file = p / "idVendor"
            pid_file = p / "idProduct"
            if vid_file.exists() and pid_file.exists():
                try:
                    dev_vid = vid_file.read_text().strip()
                    dev_pid = pid_file.read_text().strip()
                    if dev_vid == vid and dev_pid == pid:
                        return _by_id_path_for(name) or f"/dev/{name}"
                except OSError:
                    pass
                break  # found a USB device node but didn't match
            p = p.parent

    return None


def device_uses_deep_sleep(device_dir: Path) -> bool:
    """Check if a device uses deep sleep (needs JTAG wake before flash)."""
    main_cpp = device_dir / "main" / "main.cpp"
    main_c = device_dir / "main" / "main.c"

    for src in [main_cpp, main_c]:
        if src.exists():
            try:
                content = src.read_text()
                return "esp_deep_sleep_start" in content
            except OSError:
                pass
    return False


def jtag_reset_to_wake() -> None:
    """Use OpenOCD to JTAG-reset the target, waking it from deep sleep.

    Non-fatal: if openocd is not found or the reset fails, we continue
    (the device might already be awake or might not need JTAG).
    """
    openocd = shutil.which("openocd")
    if not openocd:
        print("openocd not found, skipping JTAG reset")
        return

    cmd = [openocd]

    # Pass scripts path if available (nix store layout)
    scripts_dir = os.environ.get("OPENOCD_SCRIPTS", "")
    # Work around malformed env var (e.g. "...scriptsexport" instead of "...scripts")
    if scripts_dir and not Path(scripts_dir).is_dir():
        scripts_dir = ""
    if not scripts_dir:
        # Try to infer from openocd binary path (nix convention)
        ocd_dir = Path(openocd).resolve().parent.parent
        candidate = ocd_dir / "share" / "openocd" / "scripts"
        # Also check the esp32 variant path
        esp_candidate = ocd_dir / "openocd-esp32" / "share" / "openocd" / "scripts"
        if candidate.is_dir():
            scripts_dir = str(candidate)
        elif esp_candidate.is_dir():
            scripts_dir = str(esp_candidate)

    if scripts_dir:
        cmd += ["-s", scripts_dir]

    cmd += [
        "-f", "board/esp32c6-bridge.cfg",
        "-c", "gdb port disabled",
        "-c", "tcl port disabled",
        "-c", "telnet port disabled",
        "-c", "init; reset run; shutdown",
    ]

    print("JTAG reset to wake device from deep sleep...")
    print(f"+ {' '.join(cmd)}", flush=True)
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        print(f"  JTAG reset failed (non-fatal): {result.stderr.splitlines()[-1] if result.stderr else 'unknown'}")
    else:
        print("  JTAG reset OK")


def detect_port(method: str) -> tuple[str, str]:
    """Return (flash_port, monitor_port) for the given method.

    For 'usb' both ports are the same ttyACM device.
    For 'esp-prog' flash=first ttyUSB, monitor=second ttyUSB (FT2232 channels).
    For 'esp-prog-2' uses VID/PID to find the ESP-Prog-2 UART port.
    """
    if method == "usb":
        # Try VID/PID match first (ESP32-C6/S3 built-in USB-Serial/JTAG)
        port = find_port_by_vid_pid(VID_ESPRESSIF, PID_ESP32_USB_JTAG)
        if port:
            return port, port

        # Fall back to first ttyACM that is NOT the ESP-Prog-2
        candidates = sorted(glob.glob("/dev/ttyACM*"))
        esp_prog2 = find_port_by_vid_pid(VID_ESPRESSIF, PID_ESP_PROG_2)
        filtered = [c for c in candidates if c != esp_prog2]
        if filtered:
            return filtered[0], filtered[0]
        if not candidates:
            die("no /dev/ttyACM* device found — is the board plugged in via USB?\n"
                "  If the device is in deep sleep, connect an ESP-Prog-2 for JTAG wake.")
        # Only ESP-Prog-2 is present — device may still be asleep
        die(f"only ESP-Prog-2 found at {esp_prog2} — target board not on USB.\n"
            "  The device may be in deep sleep. Try shaking it (vibration wake)\n"
            "  or check the JTAG connection for the ESP-Prog-2.")

    elif method == "esp-prog-2":
        port = find_port_by_vid_pid(VID_ESPRESSIF, PID_ESP_PROG_2)
        if not port:
            die("ESP-Prog-2 not found (expected USB 303a:1002) — is it plugged in?")
        return port, port

    else:  # esp-prog (legacy FT2232)
        candidates = sorted(glob.glob("/dev/ttyUSB*"))
        if len(candidates) < 2:
            die(
                f"esp-prog requires two /dev/ttyUSB* ports (FT2232 channels A+B), "
                f"found: {candidates or 'none'}"
            )
        return candidates[0], candidates[1]


def build(package_name: str) -> Path:
    """Build firmware via nix; return the store path containing flash artifacts."""
    package = f".#{package_name}"
    print(f"Building via nix: {package}")
    result = subprocess.run(
        ["nix", "build", package, "--no-link", "--print-out-paths"],
        capture_output=True, text=True, cwd=REPO_ROOT,
    )
    if result.returncode != 0:
        # Show nix build output on failure
        print(result.stderr, file=sys.stderr, end="")
        sys.exit(result.returncode)
    return Path(result.stdout.strip().splitlines()[-1])


def decrypt_sops(sops_file: Path) -> dict[str, str]:
    """Decrypt a SOPS YAML file and return key-value pairs as a dict.

    Supports two formats:
    - YAML with a `data:` block containing KEY=VALUE lines (garage-opener style)
    - Standard YAML with KEY: VALUE top-level entries (thread.sops.yaml style)
    """
    result = subprocess.run(
        ["sops", "--decrypt", str(sops_file)],
        capture_output=True, text=True,
    )
    if result.returncode != 0:
        die(f"sops decrypt failed: {result.stderr.strip()}")

    pairs = {}
    for line in result.stdout.splitlines():
        line = line.strip()
        # Skip YAML structural lines
        if not line or line.startswith("data:") or line == "|":
            continue
        # Try KEY=VALUE format first (garage-opener style)
        if "=" in line:
            key, _, value = line.partition("=")
            pairs[key.strip()] = value.strip()
        # Try YAML KEY: VALUE format
        elif ": " in line:
            key, _, value = line.partition(": ")
            pairs[key.strip()] = value.strip()
    return pairs


def generate_nvs_partition(device_dir: Path, build_dir: Path) -> Path | None:
    """Generate an NVS binary partition from SOPS secrets for garage-opener.

    Returns the path to the generated .bin, or None if device has no secrets.
    Reads from secrets/wifi.sops.yaml and secrets/ha.sops.yaml.
    """
    wifi_sops = REPO_ROOT / "secrets" / "wifi.sops.yaml"
    ha_sops = REPO_ROOT / "secrets" / "ha.sops.yaml"

    if not wifi_sops.exists() or not ha_sops.exists():
        return None

    device_name = device_dir.name
    if device_name != "garage-opener":
        return None

    print("Decrypting secrets from SOPS...")
    secrets = {}
    secrets.update(decrypt_sops(wifi_sops))
    secrets.update(decrypt_sops(ha_sops))

    # Verify all required keys are present
    missing = [k for k in GARAGE_NVS_KEYS if k not in secrets]
    if missing:
        die(f"SOPS file missing required keys: {', '.join(missing)}")

    # Generate CSV for nvs_partition_gen.py
    # Format: key,type,encoding,value
    tmpdir = tempfile.mkdtemp(prefix="nvs_")
    csv_path = Path(tmpdir) / "nvs.csv"
    bin_path = Path(tmpdir) / "nvs.bin"

    with open(csv_path, "w", newline="") as f:
        writer = csv.writer(f)
        writer.writerow(["key", "type", "encoding", "value"])

        # Write namespace header
        writer.writerow(["garage", "namespace", "", ""])

        # Write each secret
        for sops_key, (_, nvs_key) in GARAGE_NVS_KEYS.items():
            writer.writerow([nvs_key, "data", "string", secrets[sops_key]])

    # Get NVS partition size from partition table
    nvs_size = "0x6000"  # 24K — matches default partition table

    idf_path = os.environ.get("IDF_PATH", "")
    nvs_gen = Path(idf_path) / "components" / "nvs_flash" / "nvs_partition_generator" / "nvs_partition_gen.py"
    if not nvs_gen.exists():
        die(f"nvs_partition_gen.py not found at {nvs_gen} — is IDF_PATH set?")

    run(["python3", str(nvs_gen), "generate", str(csv_path), str(bin_path), nvs_size])

    # Clean up CSV (contains plaintext secrets)
    csv_path.unlink()

    print(f"NVS partition: {bin_path}")
    return bin_path


def generate_thread_nvs_partition(build_dir: Path, sops_file: Path) -> Path | None:
    """Generate an NVS binary partition with Thread network credentials.

    Decrypts the SOPS file to get THREAD_DATASET_TLV (hex-encoded operational
    dataset), then creates an NVS partition with namespace "thread" and key
    "dataset_tlv" using hex2bin encoding.

    Returns the path to the generated .bin, or None if sops_file doesn't exist.
    """
    if not sops_file.exists():
        die(f"Thread SOPS file not found: {sops_file}")

    print("Decrypting Thread credentials from SOPS...")
    secrets = decrypt_sops(sops_file)

    if "THREAD_DATASET_TLV" not in secrets:
        die("SOPS file missing required key: THREAD_DATASET_TLV")

    dataset_hex = secrets["THREAD_DATASET_TLV"]

    # Generate CSV for nvs_partition_gen.py
    tmpdir = tempfile.mkdtemp(prefix="nvs_thread_")
    csv_path = Path(tmpdir) / "nvs.csv"
    bin_path = Path(tmpdir) / "nvs.bin"

    with open(csv_path, "w", newline="") as f:
        writer = csv.writer(f)
        writer.writerow(["key", "type", "encoding", "value"])
        writer.writerow(["thread", "namespace", "", ""])
        writer.writerow(["dataset_tlv", "data", "hex2bin", dataset_hex])

    nvs_size = "0x6000"  # 24K — matches default partition table

    idf_path = os.environ.get("IDF_PATH", "")
    nvs_gen = Path(idf_path) / "components" / "nvs_flash" / "nvs_partition_generator" / "nvs_partition_gen.py"
    if not nvs_gen.exists():
        die(f"nvs_partition_gen.py not found at {nvs_gen} — is IDF_PATH set?")

    run(["python3", str(nvs_gen), "generate", str(csv_path), str(bin_path), nvs_size])

    # Clean up CSV (contains plaintext secrets)
    csv_path.unlink()

    print(f"Thread NVS partition: {bin_path}")
    return bin_path


def flash(build_dir: Path, flash_port: str, erase: bool,
          deep_sleep: bool = False,
          extra_images: list[tuple[str, Path]] | None = None,
          in_bootloader: bool = False) -> None:
    """Flash firmware using esptool.py, reading offsets from flasher_args.json.

    extra_images is a list of (offset, path) pairs — NVS partitions and the like
    — written in the same esptool invocation as the firmware. Writing them
    separately would work on a UART adapter but not on a part with native USB
    (ESP32-S2/S3): esptool resets the chip when it finishes, and between
    invocations the CDC port belongs to firmware that may not be running yet, so
    the next invocation finds no port to open.

    If deep_sleep is True, a post-flash USB disconnect (esptool exit code 2
    with "Serial data stream stopped") is treated as success — the device
    rebooted into the app and then entered deep sleep before esptool could
    cleanly close the connection.
    """
    args_file = build_dir / "flasher_args.json"
    if not args_file.exists():
        die(f"flasher_args.json not found in {build_dir}")

    with open(args_file) as f:
        fargs = json.load(f)

    extra = fargs["extra_esptool_args"]
    # A device already sitting in the ROM download loader must not be reset first:
    # on a native-USB part the DTR/RTS toggle drops it off the bus and the port
    # disappears mid-command. This is the recovery path after a full erase, where
    # there is no firmware left to re-enter the bootloader on its own.
    before = "no_reset" if in_bootloader else extra["before"]
    base = [
        "esptool.py",
        "--chip", extra["chip"],
        "-p", flash_port,
        "--before", before,
        "--after", extra["after"],
    ]

    write_cmd = base + ["write_flash"]
    if erase:
        # --erase-all rather than a preceding `erase_flash` run: that was a second
        # esptool invocation, and its trailing hard reset left a native-USB part
        # with an erased flash and therefore no firmware to enumerate the CDC
        # port. The following write_flash then died with "Could not open
        # /dev/ttyACM0". Doing it inside one invocation keeps the connection.
        write_cmd += ["--erase-all"]
    write_cmd += fargs["write_flash_args"]
    for offset, relpath in fargs["flash_files"].items():
        write_cmd += [offset, str(build_dir / relpath)]
    for offset, image in (extra_images or []):
        write_cmd += [offset, str(image)]

    print(f"+ {' '.join(write_cmd)}", flush=True)
    result = subprocess.run(write_cmd, capture_output=True, text=True)
    sys.stdout.write(result.stdout)
    sys.stderr.write(result.stderr)

    if result.returncode != 0:
        output = result.stdout + result.stderr
        # Deep-sleep devices reboot after flash and drop USB before esptool
        # can cleanly close. If all data was written (100%) this is fine.
        if deep_sleep and "Serial data stream stopped" in output and "(100 %)" in output:
            print("(USB disconnected after flash — device rebooted into deep sleep, flash OK)")
        else:
            # On a native-USB part (ESP32-S2/S3) the CDC port belongs to whatever
            # firmware is running. Once the flash is erased there is nothing left
            # to provide it, so esptool cannot talk to the chip and no amount of
            # retrying helps — the download loader has to be entered by hand.
            if not in_bootloader and (
                "Could not open" in output
                or "No serial data received" in output
                or "Failed to connect" in output
            ):
                print(
                    "\nThe chip is not reachable over USB. If a previous flash was\n"
                    "interrupted the flash may be erased, leaving no firmware to\n"
                    "enumerate the port. Put it into the download loader by hand:\n"
                    "  hold BOOT, tap RESET, release BOOT\n"
                    "then re-run with --in-bootloader.",
                    file=sys.stderr,
                )
            sys.exit(result.returncode)


def monitor(build_dir: Path, monitor_port: str) -> None:
    """Start esp_idf_monitor with ELF for address decoding (blocks until Ctrl+C)."""
    elfs = list(build_dir.glob("*.elf"))
    if not elfs:
        die(f"no .elf file found in {build_dir}")
    elf = elfs[0]

    # Read chip target from flasher_args.json for the --target flag
    args_file = build_dir / "flasher_args.json"
    target = None
    if args_file.exists():
        with open(args_file) as f:
            fargs = json.load(f)
        target = fargs.get("extra_esptool_args", {}).get("chip")

    cmd = ["python3", "-m", "esp_idf_monitor", "-p", monitor_port]
    if target:
        cmd += ["--target", target]
    cmd.append(str(elf))

    run(cmd)


def get_pairing_info(device_dir: Path) -> dict[str, str | None]:
    """Get Matter pairing codes for a device.

    Reads from <device_dir>/matter-pairing.json if it exists, otherwise
    returns None (non-Matter device or unknown codes).

    matter-pairing.json format:
        {"qr_code": "MT:...", "manual_code": "12345678901"}
    """
    pairing_file = device_dir / "matter-pairing.json"
    if pairing_file.exists():
        try:
            with open(pairing_file) as f:
                data = json.load(f)
            return {
                "qr_code": data.get("qr_code"),
                "manual_code": data.get("manual_code"),
            }
        except (json.JSONDecodeError, OSError):
            pass
    return {"qr_code": None, "manual_code": None}


def print_pairing_info(info: dict[str, str | None]) -> None:
    """Print Matter pairing info in a prominent box."""
    if not info["qr_code"] and not info["manual_code"]:
        return
    print()
    print("=" * 50)
    print("  Matter Pairing Info")
    print("=" * 50)
    if info["qr_code"]:
        print(f"  QR Code:     {info['qr_code']}")
    if info["manual_code"]:
        print(f"  Manual Code: {info['manual_code']}")
    print("=" * 50)
    print()


def _raw_serial_monitor(port: str) -> None:
    """Minimal serial monitor using pyserial — works without a TTY on stdin."""
    import serial  # pyserial, available in the nix devShell

    while True:
        try:
            with serial.Serial(port, 115200, timeout=1) as ser:
                while True:
                    line = ser.readline()
                    if line:
                        sys.stdout.buffer.write(line)
                        sys.stdout.buffer.flush()
        except serial.SerialException:
            return  # port gone — caller handles reconnect
        except KeyboardInterrupt:
            return


def monitor_with_reconnect(build_dir: Path, monitor_port: str,
                           elf_path: Path | None = None,
                           target: str | None = None) -> None:
    """Run esp_idf_monitor in a loop, reconnecting after USB disconnect.

    Deep-sleep devices drop USB when sleeping. This wrapper detects the
    disconnect (monitor exits with non-zero), waits for the port to
    reappear, then restarts the monitor.

    Falls back to a raw serial reader when stdin is not a TTY (e.g. when
    run from a background process or piped).
    """
    if elf_path is None:
        elfs = list(build_dir.glob("*.elf"))
        if not elfs:
            die(f"no .elf file found in {build_dir}")
        elf_path = elfs[0]

    if target is None:
        args_file = build_dir / "flasher_args.json"
        if args_file.exists():
            with open(args_file) as f:
                fargs = json.load(f)
            target = fargs.get("extra_esptool_args", {}).get("chip")

    use_raw = not sys.stdin.isatty()

    if use_raw:
        print("(stdin is not a TTY — using raw serial monitor, no address decoding)")

    while True:
        if use_raw:
            _raw_serial_monitor(monitor_port)
        else:
            cmd = ["python3", "-m", "esp_idf_monitor", "-p", monitor_port]
            if target:
                cmd += ["--target", target]
            cmd.append(str(elf_path))
            try:
                print(f"+ {' '.join(cmd)}", flush=True)
                result = subprocess.run(cmd)
                if result.returncode == 0:
                    return  # clean exit
            except KeyboardInterrupt:
                return

        # Monitor exited with error — likely USB disconnect
        print(f"\nMonitor disconnected (port may have gone away).")
        print(f"Waiting for {monitor_port} to reappear...", flush=True)

        # Wait for port to come back
        while True:
            try:
                time.sleep(2)
            except KeyboardInterrupt:
                return

            # Check if the port is back via VID/PID
            port = find_port_by_vid_pid(VID_ESPRESSIF, PID_ESP32_USB_JTAG)
            if port:
                monitor_port = port
                print(f"Port reappeared at {port}, reconnecting...")
                break


def main() -> None:
    parser = argparse.ArgumentParser(
        description=__doc__,
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument(
        "device_dir",
        metavar="<device-dir>",
        help="path to device project directory (e.g. devices/freezer-temp-sensor)",
    )
    parser.add_argument(
        "-m", "--method",
        choices=["usb", "esp-prog", "esp-prog-2"],
        default="usb",
        help="flash interface: 'usb' (default) for USB-Serial/JTAG, "
             "'esp-prog' for FT2232 UART, 'esp-prog-2' for ESP-Prog-2",
    )
    parser.add_argument(
        "-p", "--port",
        metavar="PORT",
        help="override serial port (auto-detected if omitted)",
    )
    parser.add_argument(
        "-e", "--erase",
        action="store_true",
        help="erase flash before flashing (clears NVS and commissioning data)",
    )
    parser.add_argument(
        "--no-monitor",
        action="store_true",
        help="skip serial monitor after flash",
    )
    parser.add_argument(
        "--fake-sensor",
        action="store_true",
        help="flash the fake sine-wave sensor build (freezer-temp-sensor only)",
    )
    parser.add_argument(
        "--thread",
        action="store_true",
        help="embed Thread network credentials from SOPS into NVS partition",
    )
    parser.add_argument(
        "--in-bootloader",
        action="store_true",
        help="device is already in the ROM download loader (hold BOOT, tap RESET); "
             "skips the reset esptool would otherwise do, which knocks a "
             "native-USB chip off the bus. Use to recover after an interrupted erase.",
    )

    args = parser.parse_args()

    # Resolve device directory relative to repo root if not absolute.
    device_dir = Path(args.device_dir)
    if not device_dir.is_absolute():
        device_dir = REPO_ROOT / device_dir
    device_dir = device_dir.resolve()

    if not device_dir.is_dir():
        die(f"device directory not found: {device_dir}")

    if args.fake_sensor and device_dir.name != "freezer-temp-sensor":
        die("--fake-sensor is only valid for freezer-temp-sensor")

    package_name = device_dir.name
    if args.fake_sensor:
        package_name = "freezer-temp-sensor-fake"

    print(f"Device:  {device_dir.name}")
    print(f"Method:  {args.method}")
    if args.fake_sensor:
        print(f"Sensor:  fake (sine-wave)")
    print()

    build_dir = build(package_name)
    print(f"Output:  {build_dir}")
    print()

    # If device uses deep sleep, JTAG-reset it so USB re-enumerates.
    if device_uses_deep_sleep(device_dir):
        jtag_reset_to_wake()
        time.sleep(3)  # wait for USB re-enumeration after JTAG reset

    # Resolve port only when we need to flash (device must be plugged in by now).
    if args.port:
        flash_port = args.port
        monitor_port = args.port
    else:
        flash_port, monitor_port = detect_port(args.method)

    print(f"Flash:   {flash_port}")
    if not args.no_monitor:
        print(f"Monitor: {monitor_port}")
    print()

    # Generate the NVS secrets partition. Unconditional: generate_nvs_partition
    # only returns a binary for garage-opener, which is not a Matter device, so
    # there is no fabric/commissioning data in its NVS to clobber — the region
    # holds nothing but the SOPS-derived secrets this rebuilds from scratch.
    # Gating it on --erase (as the Matter devices below need) meant a plain flash
    # silently left the device unprovisioned, booting straight to
    # "NVS open failed ... flash secrets partition first".
    nvs_bin = generate_nvs_partition(device_dir, build_dir)

    # Generate Thread NVS partition if --thread flag is set.
    # Only flash NVS secrets partitions when --erase is also set, because writing
    # the NVS partition image overwrites the entire NVS region (including Matter
    # fabric/commissioning data).  Thread credentials persist across reboots so
    # they only need to be provisioned once on a clean flash.
    thread_nvs_bin = None
    if args.thread and args.erase:
        thread_sops = REPO_ROOT / "secrets" / "thread.sops.yaml"
        thread_nvs_bin = generate_thread_nvs_partition(build_dir, thread_sops)
    elif args.thread:
        print("Note: --thread without --erase skips NVS flash (credentials persist in NVS)")

    # NVS images go into the same esptool invocation as the firmware rather than
    # a follow-up one, so the write cannot land on a port that disappeared when
    # the firmware write reset the chip. See flash() for the full reasoning.
    extra_images = [(NVS_OFFSET, b) for b in (nvs_bin, thread_nvs_bin) if b]

    uses_deep_sleep = device_uses_deep_sleep(device_dir)
    flash(build_dir, flash_port, args.erase, deep_sleep=uses_deep_sleep,
          extra_images=extra_images, in_bootloader=args.in_bootloader)

    # Clean up NVS binaries (they contain plaintext secrets)
    for tmp_bin in (nvs_bin, thread_nvs_bin):
        if tmp_bin:
            tmp_bin.unlink()
            tmp_bin.parent.rmdir()

    # Display Matter pairing codes
    info = get_pairing_info(device_dir)
    print_pairing_info(info)

    if not args.no_monitor:
        # Re-detect port after flash — device reset may re-enumerate USB
        if not args.port:
            new_port = find_port_by_vid_pid(VID_ESPRESSIF, PID_ESP32_USB_JTAG)
            if new_port and new_port != monitor_port:
                print(f"Port changed after flash: {monitor_port} → {new_port}")
                monitor_port = new_port

        if uses_deep_sleep:
            monitor_with_reconnect(build_dir, monitor_port)
        else:
            monitor(build_dir, monitor_port)


if __name__ == "__main__":
    main()
