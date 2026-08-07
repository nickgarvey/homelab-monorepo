#!/usr/bin/env python3
"""Commission a Matter device via Home Assistant's WebSocket API.

Reads the pairing code from the device's matter-pairing.json and the HA
long-lived access token from secrets/ha.sops.yaml (decrypted via sops).

Usage (from repo root, inside nix develop):
  python3 scripts/commission.py devices/laundry-detector
  python3 scripts/commission.py devices/freezer-temp-sensor
  python3 scripts/commission.py --code "MT:Y.K9042C00KA0648G00"
"""

import argparse
import hashlib
import json
import os
import re
import secrets as secrets_mod
import socket
import struct
import subprocess
import sys
import base64
from pathlib import Path
from urllib.parse import urlparse

REPO_ROOT = Path(__file__).resolve().parent.parent


def die(msg: str) -> None:
    print(f"error: {msg}", file=sys.stderr)
    sys.exit(1)


def decrypt_sops(sops_file: Path) -> dict[str, str]:
    """Decrypt a SOPS YAML file and return key-value pairs."""
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


# ---------------------------------------------------------------------------
# Minimal WebSocket client (stdlib only, no external deps)
# ---------------------------------------------------------------------------

def _ws_connect(host: str, port: int, path: str, use_ssl: bool = False):
    """Open a WebSocket connection. Returns (sock, initial_data)."""
    if use_ssl:
        import ssl
        raw = socket.create_connection((host, port), timeout=30)
        ctx = ssl.create_default_context()
        # Relax strict X.509 checks — the private CA intermediate cert is
        # missing the optional Authority Key Identifier extension, which
        # OpenSSL 3.x rejects by default. The chain is still fully verified.
        ctx.verify_flags &= ~ssl.VERIFY_X509_STRICT
        sock = ctx.wrap_socket(raw, server_hostname=host)
    else:
        sock = socket.create_connection((host, port), timeout=30)

    # WebSocket handshake
    ws_key = base64.b64encode(secrets_mod.token_bytes(16)).decode()
    handshake = (
        f"GET {path} HTTP/1.1\r\n"
        f"Host: {host}:{port}\r\n"
        f"Upgrade: websocket\r\n"
        f"Connection: Upgrade\r\n"
        f"Sec-WebSocket-Key: {ws_key}\r\n"
        f"Sec-WebSocket-Version: 13\r\n"
        f"\r\n"
    )
    sock.sendall(handshake.encode())

    # Read HTTP response headers
    response = b""
    while b"\r\n\r\n" not in response:
        chunk = sock.recv(4096)
        if not chunk:
            die("WebSocket handshake failed: connection closed")
        response += chunk

    header_end = response.index(b"\r\n\r\n") + 4
    status_line = response[:response.index(b"\r\n")].decode()
    if "101" not in status_line:
        die(f"WebSocket handshake failed: {status_line}")

    # Any data after headers is the start of WebSocket frames
    leftover = response[header_end:]
    return sock, leftover


def _ws_send(sock, message: str):
    """Send a text WebSocket frame (client-to-server, masked)."""
    payload = message.encode("utf-8")
    frame = bytearray()
    frame.append(0x81)  # FIN + text opcode

    length = len(payload)
    if length < 126:
        frame.append(0x80 | length)  # mask bit set
    elif length < 65536:
        frame.append(0x80 | 126)
        frame.extend(struct.pack(">H", length))
    else:
        frame.append(0x80 | 127)
        frame.extend(struct.pack(">Q", length))

    mask = secrets_mod.token_bytes(4)
    frame.extend(mask)
    frame.extend(bytes(b ^ mask[i % 4] for i, b in enumerate(payload)))
    sock.sendall(frame)


def _ws_recv(sock, buf: bytearray) -> str:
    """Receive a complete text WebSocket frame. Returns the payload string."""
    # Ensure we have at least 2 bytes for the frame header
    while len(buf) < 2:
        chunk = sock.recv(4096)
        if not chunk:
            die("WebSocket connection closed unexpectedly")
        buf.extend(chunk)

    b0, b1 = buf[0], buf[1]
    masked = bool(b1 & 0x80)
    length = b1 & 0x7F
    offset = 2

    if length == 126:
        while len(buf) < 4:
            buf.extend(sock.recv(4096))
        length = struct.unpack(">H", buf[2:4])[0]
        offset = 4
    elif length == 127:
        while len(buf) < 10:
            buf.extend(sock.recv(4096))
        length = struct.unpack(">Q", buf[2:10])[0]
        offset = 10

    if masked:
        offset += 4

    total_needed = offset + length
    while len(buf) < total_needed:
        chunk = sock.recv(4096)
        if not chunk:
            die("WebSocket connection closed while reading frame")
        buf.extend(chunk)

    payload = buf[offset:offset + length]
    # Remove consumed frame from buffer
    del buf[:offset + length]

    opcode = b0 & 0x0F
    if opcode == 0x08:  # close frame
        die("WebSocket server sent close frame")
    if opcode == 0x09:  # ping — send pong
        pong = bytearray([0x8A, 0x80 | len(payload)])
        mask = secrets_mod.token_bytes(4)
        pong.extend(mask)
        pong.extend(bytes(b ^ mask[i % 4] for i, b in enumerate(payload)))
        sock.sendall(pong)
        return _ws_recv(sock, buf)  # recurse to get actual message

    return payload.decode("utf-8")


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(description="Commission a Matter device via HA")
    parser.add_argument("device_dir", nargs="?", type=Path,
                        help="Device directory (e.g. devices/laundry-detector)")
    parser.add_argument("--code", help="Override pairing code (QR or manual)")
    args = parser.parse_args()

    # Determine pairing code
    code = args.code
    if not code:
        if not args.device_dir:
            die("Provide a device directory or --code")
        pairing_file = args.device_dir / "matter-pairing.json"
        if not pairing_file.exists():
            die(f"No matter-pairing.json in {args.device_dir}")
        with open(pairing_file) as f:
            pairing = json.load(f)
        code = pairing.get("qr_code") or pairing.get("manual_code")
        if not code:
            die(f"No qr_code or manual_code in {pairing_file}")

    print(f"Pairing code: {code}")

    # Get HA connection info from SOPS
    ha_sops = REPO_ROOT / "secrets" / "ha.sops.yaml"
    if not ha_sops.exists():
        die(f"HA secrets not found: {ha_sops}")

    ha_secrets = decrypt_sops(ha_sops)
    base_url = ha_secrets.get("HA_BASE_URL")
    token = ha_secrets.get("HA_API_KEY")

    if not base_url:
        die("HA_BASE_URL not found in secrets/ha.sops.yaml")
    if not token:
        die("HA_API_KEY not found in secrets/ha.sops.yaml")

    parsed = urlparse(base_url)
    host = parsed.hostname
    port = parsed.port or (443 if parsed.scheme == "https" else 8123)
    use_ssl = parsed.scheme == "https"
    ws_path = "/api/websocket"

    print(f"Connecting to {host}:{port} ({'wss' if use_ssl else 'ws'})...")

    sock, leftover = _ws_connect(host, port, ws_path, use_ssl)
    buf = bytearray(leftover)

    try:
        # Step 1: receive auth_required
        msg = json.loads(_ws_recv(sock, buf))
        if msg.get("type") != "auth_required":
            die(f"Expected auth_required, got: {msg}")
        print(f"  HA version: {msg.get('ha_version', '?')}")

        # Step 2: authenticate
        _ws_send(sock, json.dumps({"type": "auth", "access_token": token}))
        msg = json.loads(_ws_recv(sock, buf))
        if msg.get("type") != "auth_ok":
            die(f"Authentication failed: {msg}")
        print("  Authenticated")

        # Step 3: send commission command
        print(f"  Commissioning with code: {code}")
        _ws_send(sock, json.dumps({
            "type": "matter/commission",
            "id": 1,
            "code": code,
        }))

        # Step 4: wait for result (can take 30-60s)
        sock.settimeout(120)
        msg = json.loads(_ws_recv(sock, buf))
        if msg.get("success"):
            print("\nCommissioning successful!")
        else:
            error = msg.get("error", {})
            print(f"\nCommissioning failed: {error.get('message', msg)}")
            sys.exit(1)

    finally:
        # Send WebSocket close frame
        try:
            close_frame = bytearray([0x88, 0x80, 0, 0, 0, 0])
            sock.sendall(close_frame)
        except Exception:
            pass
        sock.close()


if __name__ == "__main__":
    main()
