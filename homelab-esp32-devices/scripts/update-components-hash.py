#!/usr/bin/env python3
"""Re-pin a components FOD hash in flake.nix.

Run this after changing idf_component.yml or updating dependencies.lock
(e.g. after `idf.py update-dependencies`).  The script triggers a Nix
build of the components-only derivation with a fake hash, extracts the
correct hash from the error output, and patches flake.nix in place.

Usage:
    ./scripts/update-components-hash.py freezer-temp-sensor
    ./scripts/update-components-hash.py garage-opener
"""

import re
import subprocess
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
FLAKE = REPO_ROOT / "flake.nix"

# Map package name → marker comment in flake.nix
DEVICES = {
    "freezer-temp-sensor": "# freezer-components",
    "garage-opener": "# garage-components",
}


def main() -> None:
    if len(sys.argv) != 2 or sys.argv[1] not in DEVICES:
        print(f"Usage: {sys.argv[0]} <device>", file=sys.stderr)
        print(f"Devices: {', '.join(DEVICES)}", file=sys.stderr)
        sys.exit(1)

    device = sys.argv[1]
    marker = DEVICES[device]
    package = f".#{device}-components"

    print(f"Building {package} with fake hash to discover real hash...")
    text = FLAKE.read_text()

    pattern = r'(outputHash\s*=\s*)"[^"]*"(\s*;[^\n]*' + re.escape(marker) + r')'
    if not re.search(pattern, text):
        print(f"error: could not find outputHash line with marker '{marker}' in flake.nix",
              file=sys.stderr)
        sys.exit(1)

    # Replace existing hash with the Nix fake hash so the build always fails
    # and prints the correct hash.
    patched = re.sub(
        pattern,
        r'\1"sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="\2',
        text,
    )

    FLAKE.write_text(patched)

    result = subprocess.run(
        ["nix", "build", package, "--no-link"],
        capture_output=True, text=True, cwd=REPO_ROOT,
    )

    match = re.search(r"got:\s*(sha256-[A-Za-z0-9+/=]+)", result.stderr)
    if not match:
        if result.returncode == 0:
            print("Build succeeded — hash was already correct (or fake hash matched).")
            FLAKE.write_text(text)  # restore
            return
        print("error: could not extract hash from Nix output", file=sys.stderr)
        print("stderr:", result.stderr[-2000:], file=sys.stderr)
        FLAKE.write_text(text)  # restore
        sys.exit(1)

    new_hash = match.group(1)
    updated = re.sub(
        pattern,
        rf'\1"{new_hash}"\2',
        FLAKE.read_text(),
    )
    FLAKE.write_text(updated)
    print(f"Updated outputHash to {new_hash}")
    print("Commit flake.nix and dependencies.lock together.")


if __name__ == "__main__":
    main()
