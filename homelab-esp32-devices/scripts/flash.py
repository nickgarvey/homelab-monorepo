#!/usr/bin/env python3
"""Flash and monitor ESP32 devices in this repo.

Supports USB (direct USB-Serial/JTAG) and esp-prog (FT2232 UART) flash methods.

Build behaviour:
  Runs `nix build .#<device>` which dispatches to the remote builder (desktop-nixos)
  on first build and uses the binary cache thereafter.  The built firmware is flashed
  directly from the Nix store.  Falls back to a local `idf.py build` for devices that
  don't have a Nix package yet.

Examples:
  ./scripts/flash.py devices/freezer-temp-sensor
  ./scripts/flash.py --method esp-prog devices/freezer-temp-sensor
  ./scripts/flash.py --erase devices/freezer-temp-sensor
  ./scripts/flash.py devices/led-blinker
  ./scripts/flash.py --no-monitor devices/led-blinker
"""

import argparse
import glob
import subprocess
import sys
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parent.parent


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


def has_nix_package(device_dir: Path) -> bool:
    """Return True if the device has a matching nix package in the flake."""
    result = subprocess.run(
        ["nix", "eval", f".#packages.x86_64-linux.{device_dir.name}", "--apply", "x: true"],
        capture_output=True, text=True, cwd=REPO_ROOT,
    )
    return result.returncode == 0


def build(device_dir: Path) -> Path:
    """Build firmware; return the directory containing flash artifacts."""
    if has_nix_package(device_dir):
        # Nix build: dispatches to remote builder; result is cached.
        package = f".#{device_dir.name}"
        print(f"Building via nix: {package}")
        result = subprocess.run(
            ["nix", "build", package, "--no-link", "--print-out-paths"],
            capture_output=False, text=True, cwd=REPO_ROOT,
        )
        if result.returncode != 0:
            sys.exit(result.returncode)
        out = subprocess.check_output(
            ["nix", "build", package, "--no-link", "--print-out-paths"],
            text=True, cwd=REPO_ROOT,
        ).strip()
        return Path(out)

    # Fallback: local idf.py build (for devices without a nix package yet)
    run(["idf.py", "-C", str(device_dir), "build"])
    return device_dir / "build"


def flash(device_dir: Path, build_dir: Path, flash_port: str, erase: bool) -> None:
    """Flash the firmware to the device using the pre-built artifacts in build_dir."""
    base = ["idf.py", "-C", str(device_dir), "-B", str(build_dir), "-p", flash_port]
    if erase:
        run(base + ["erase-flash", "flash"])
    else:
        run(base + ["flash"])


def monitor(device_dir: Path, monitor_port: str) -> None:
    """Start idf.py monitor (blocks until Ctrl+C)."""
    run(["idf.py", "-C", str(device_dir), "-p", monitor_port, "monitor"])


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

    args = parser.parse_args()

    # Resolve device directory relative to repo root if not absolute.
    device_dir = Path(args.device_dir)
    if not device_dir.is_absolute():
        device_dir = REPO_ROOT / device_dir
    device_dir = device_dir.resolve()

    if not device_dir.is_dir():
        die(f"device directory not found: {device_dir}")

    print(f"Device:  {device_dir.name}")
    print(f"Method:  {args.method}")
    print()

    build_dir = build(device_dir)

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

    flash(device_dir, build_dir, flash_port, args.erase)

    if not args.no_monitor:
        monitor(device_dir, monitor_port)


if __name__ == "__main__":
    main()
