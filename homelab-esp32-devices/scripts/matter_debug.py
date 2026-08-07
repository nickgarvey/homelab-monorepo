#!/usr/bin/env python3
"""Read Matter device diagnostics from Home Assistant.

Queries the HA REST + WebSocket APIs to show raw cluster attribute data
for a device, useful for debugging without serial access.

Usage (from repo root, inside nix develop):
  python3 scripts/matter_debug.py laundry-detector
  python3 scripts/matter_debug.py --watch laundry-detector
"""

import argparse
import json
import ssl
import subprocess
import sys
import time
from pathlib import Path
from urllib.request import Request, urlopen

REPO_ROOT = Path(__file__).resolve().parent.parent


def die(msg: str) -> None:
    print(f"error: {msg}", file=sys.stderr)
    sys.exit(1)


def decrypt_sops(sops_file: Path) -> dict[str, str]:
    result = subprocess.run(
        ["sops", "--decrypt", str(sops_file)],
        capture_output=True, text=True,
    )
    if result.returncode != 0:
        die(f"sops decrypt failed: {result.stderr.strip()}")
    pairs = {}
    for line in result.stdout.splitlines():
        line = line.strip()
        if not line or line.startswith("data:") or line == "|":
            continue
        if "=" in line:
            key, _, value = line.partition("=")
            pairs[key.strip()] = value.strip()
        elif ": " in line:
            key, _, value = line.partition(": ")
            pairs[key.strip()] = value.strip()
    return pairs


def ha_get(url: str, token: str) -> dict:
    ctx = ssl.create_default_context()
    ctx.verify_flags &= ~ssl.VERIFY_X509_STRICT
    req = Request(url, headers={
        "Authorization": f"Bearer {token}",
        "Content-Type": "application/json",
    })
    with urlopen(req, context=ctx) as resp:
        return json.loads(resp.read())


def get_laundry_states(base_url: str, token: str, device_name: str) -> dict:
    """Get all entity states for a device."""
    states = ha_get(f"{base_url}/api/states", token)
    name_slug = device_name.replace("-", "_")
    return {
        s["entity_id"]: {
            "state": s["state"],
            "updated": s["last_updated"],
            "attrs": s.get("attributes", {}),
        }
        for s in states
        if name_slug in s["entity_id"]
    }


def print_states(states: dict) -> None:
    for eid, info in sorted(states.items()):
        unit = info["attrs"].get("unit_of_measurement", "")
        friendly = info["attrs"].get("friendly_name", "")
        ts = info["updated"].split("T")[1][:8] if "T" in info["updated"] else ""
        print(f"  {friendly:40s} {info['state']:>10s} {unit:5s}  ({ts})")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__,
        formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("device", help="device name (e.g. laundry-detector)")
    parser.add_argument("--watch", "-w", action="store_true",
                        help="poll every 30s")
    args = parser.parse_args()

    ha_sops = REPO_ROOT / "secrets" / "ha.sops.yaml"
    if not ha_sops.exists():
        die("secrets/ha.sops.yaml not found")
    secrets = decrypt_sops(ha_sops)
    base_url = secrets.get("HA_BASE_URL", "").rstrip("/")
    token = secrets.get("HA_API_KEY", "")
    if not base_url or not token:
        die("HA_BASE_URL or HA_API_KEY missing from secrets")

    while True:
        states = get_laundry_states(base_url, token, args.device)
        if not states:
            print(f"No entities found for '{args.device}'")
        else:
            print(f"\n{'='*60}")
            print(f"  {args.device}  ({time.strftime('%H:%M:%S')})")
            print(f"{'='*60}")
            print_states(states)

        if not args.watch:
            break
        try:
            time.sleep(30)
        except KeyboardInterrupt:
            break


if __name__ == "__main__":
    main()
