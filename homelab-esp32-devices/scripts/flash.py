#!/usr/bin/env python3
"""Flash and monitor ESP32 devices in this repo.

Supports USB (direct USB-Serial/JTAG) and esp-prog (FT2232 UART) flash methods.

Build behaviour:
  Runs `nix build .#<device>` which dispatches to the remote builder (desktop-nixos)
  on first build and uses the binary cache thereafter.  The firmware is flashed
  directly from the Nix store output using esptool.py (no idf.py at flash time).

Usage (from repo root, inside nix develop):
  nix develop --command python3 scripts/flash.py devices/freezer-temp-sensor
  nix develop --command python3 scripts/flash.py --fake-sensor devices/freezer-temp-sensor
  nix develop --command python3 scripts/flash.py --method esp-prog devices/freezer-temp-sensor
  nix develop --command python3 scripts/flash.py --erase devices/freezer-temp-sensor
  nix develop --command python3 scripts/flash.py devices/garage-opener
  nix develop --command python3 scripts/flash.py --no-monitor devices/garage-opener
"""

import argparse
import csv
import glob
import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parent.parent

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


def detect_port(method: str) -> tuple[str, str]:
    """Return (flash_port, monitor_port) for the given method.

    For 'usb' both ports are the same ttyACM device.
    For 'esp-prog' flash=first ttyUSB, monitor=second ttyUSB (FT2232 channels).
    """
    if method == "usb":
        candidates = sorted(glob.glob("/dev/ttyACM*"))
        if not candidates:
            die("no /dev/ttyACM* device found — is the board plugged in via USB?")
        port = candidates[0]
        return port, port
    else:  # esp-prog
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
    """Decrypt a SOPS YAML file and return KEY=VALUE pairs as a dict."""
    result = subprocess.run(
        ["sops", "--decrypt", str(sops_file)],
        capture_output=True, text=True,
    )
    if result.returncode != 0:
        die(f"sops decrypt failed: {result.stderr.strip()}")

    pairs = {}
    for line in result.stdout.splitlines():
        line = line.strip()
        if "=" in line and not line.startswith("data:"):
            key, _, value = line.partition("=")
            pairs[key.strip()] = value.strip()
    return pairs


def generate_nvs_partition(device_dir: Path, build_dir: Path) -> Path | None:
    """Generate an NVS binary partition from SOPS secrets for garage-opener.

    Returns the path to the generated .bin, or None if device has no secrets.
    """
    sops_file = device_dir / "secrets" / "garage.sops.yaml"
    if not sops_file.exists():
        return None

    print("Decrypting secrets from SOPS...")
    secrets = decrypt_sops(sops_file)

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


def flash(build_dir: Path, flash_port: str, erase: bool) -> None:
    """Flash firmware using esptool.py, reading offsets from flasher_args.json."""
    args_file = build_dir / "flasher_args.json"
    if not args_file.exists():
        die(f"flasher_args.json not found in {build_dir}")

    with open(args_file) as f:
        fargs = json.load(f)

    extra = fargs["extra_esptool_args"]
    base = [
        "esptool.py",
        "--chip", extra["chip"],
        "-p", flash_port,
        "--before", extra["before"],
        "--after", extra["after"],
    ]

    if erase:
        run(base + ["erase_flash"])

    write_cmd = base + ["write_flash"] + fargs["write_flash_args"]
    for offset, relpath in fargs["flash_files"].items():
        write_cmd += [offset, str(build_dir / relpath)]

    run(write_cmd)


def flash_nvs(nvs_bin: Path, flash_port: str, chip: str) -> None:
    """Flash the NVS partition binary to the NVS offset."""
    nvs_offset = "0x9000"  # Matches default partition table
    base = [
        "esptool.py",
        "--chip", chip,
        "-p", flash_port,
        "--before", "default_reset",
        "--after", "hard_reset",
        "write_flash", nvs_offset, str(nvs_bin),
    ]
    run(base)


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
        choices=["usb", "esp-prog"],
        default="usb",
        help="flash interface: 'usb' (default) for USB-Serial/JTAG, 'esp-prog' for FT2232 UART",
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

    # Generate and flash NVS secrets partition for devices that need it
    nvs_bin = generate_nvs_partition(device_dir, build_dir)

    flash(build_dir, flash_port, args.erase)

    if nvs_bin:
        # Read chip type from flasher_args.json
        with open(build_dir / "flasher_args.json") as f:
            chip = json.load(f)["extra_esptool_args"]["chip"]
        flash_nvs(nvs_bin, flash_port, chip)
        # Clean up NVS binary (contains secrets)
        nvs_bin.unlink()
        nvs_bin.parent.rmdir()

    if not args.no_monitor:
        monitor(build_dir, monitor_port)


if __name__ == "__main__":
    main()
