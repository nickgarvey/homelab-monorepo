# chip-tool — Matter Commissioning & Debug CLI

`chip-tool` is the official Matter CLI for commissioning devices, reading attributes,
and debugging the Matter stack. We build it from source via Nix at
`/home/ngarvey/homelab/homelab-nixpkgs/chip-tool`.

## Quick start

```bash
# Build (one-time, ~20 min)
cd ~/homelab/homelab-nixpkgs/chip-tool && nix build

# The binary lives at:
result/bin/chip-tool
```

All commands below assume `chip-tool` is on your PATH or you use the full path.

## Storage directory

chip-tool persists fabric keys and KVS in `/tmp` by default. Use
`--storage-directory` to isolate sessions:

```bash
mkdir -p /tmp/chip-tool-storage
chip-tool ... --storage-directory /tmp/chip-tool-storage
```

Clean it between commissioning attempts to avoid stale state:

```bash
rm -rf /tmp/chip-tool-storage && mkdir -p /tmp/chip-tool-storage
```

## Discover commissionable devices

```bash
# mDNS discovery (finds devices already on the network)
chip-tool discover commissionables

# Output includes hostname, IP, port, discriminator, commissioning mode
```

## Commission a Thread device (BLE → Thread)

### Prerequisites

1. **Thread operational dataset** — hex-encoded TLV from the border router.
   Stored in `secrets/thread-info.txt` (the `Active dataset TLVs` line).

2. **Setup pin code** and **discriminator** — from the device's QR payload.
   For the laundry-detector: pin=`20202021`, discriminator=`3840`.
   (These are the esp-matter test defaults matching QR code `MT:Y.K90AFN00KA0648G00`.)

3. **Clean BlueZ state** — stale BLE connections from previous attempts block
   reconnection. Before commissioning:

   ```bash
   sudo systemctl restart bluetooth
   rm -rf /tmp/chip-tool-storage && mkdir -p /tmp/chip-tool-storage
   ```

### Commission

```bash
DATASET="<hex-encoded active dataset TLVs>"

chip-tool pairing ble-thread \
    <node-id> \
    hex:$DATASET \
    <setup-pin-code> \
    <discriminator> \
    --bypass-attestation-verifier true \
    --timeout 240 \
    --storage-directory /tmp/chip-tool-storage
```

Example for laundry-detector:

```bash
DATASET="0e080000000000010000000300000f4a0300001935060004001fffe00208ee05a9d2c8031b890708fdb923a9d342bb3d05109adbf9f1e55a8db015f808106a06dab4030e68612d7468726561642d35353037010255070410e017a5c81cd05bb05bd689c8848c33a50c0402a0f7f8"

chip-tool pairing ble-thread 2 hex:$DATASET 20202021 3840 \
    --bypass-attestation-verifier true \
    --timeout 240 \
    --storage-directory /tmp/chip-tool-storage
```

The commissioning flow:
1. BLE scan → finds device by discriminator
2. PASE (password-authenticated session) over BLE
3. CASE (certificate-authenticated session) over BLE
4. Thread credentials provisioned → device joins Thread network
5. mDNS/SRP resolution → chip-tool discovers device on Thread via border router
6. Operational CASE session over Thread/IP
7. CommissioningComplete sent → device is commissioned

### Important: `--timeout 240`

After the device joins Thread, it must register with the SRP server on the
border router. The border router then proxies mDNS so chip-tool can discover
the device. This can take 5-15 seconds. The default timeout is too short —
use at least 120s.

## Read attributes

```bash
# Read operational state (endpoint 1)
chip-tool operationalstate read operational-state <node-id> 1 \
    --storage-directory /tmp/chip-tool-storage

# Read battery percentage
chip-tool powersource read bat-percent-remaining <node-id> 1 \
    --storage-directory /tmp/chip-tool-storage

# Read temperature
chip-tool temperaturemeasurement read measured-value <node-id> 1 \
    --storage-directory /tmp/chip-tool-storage
```

Note: deep-sleep devices are only reachable when awake (during active
monitoring or the commissioning wait period).

## Open commissioning window (multi-fabric)

To add the device to a second fabric (e.g. Home Assistant) after chip-tool
commissioning:

```bash
chip-tool pairing open-commissioning-window <node-id> 1 300 1000 3840 \
    --storage-directory /tmp/chip-tool-storage
```

Then pair from HA within 5 minutes (300s window).

## Unpair

```bash
chip-tool pairing unpair <node-id> \
    --storage-directory /tmp/chip-tool-storage
```

## Troubleshooting

### BLE connection timeout

```
FAIL: ConnectDevice: Operation was cancelled (19)
Device connection failed: CHIP Error 0x000000AC: Internal error
```

**Cause**: Stale BlueZ connection state from a previous attempt.

**Fix**:
```bash
sudo systemctl restart bluetooth
rm -rf /tmp/chip-tool-storage && mkdir -p /tmp/chip-tool-storage
```

### mDNS resolution timeout after Thread join

```
Timeout waiting for mDNS resolution.
OperationalSessionSetup: operational discovery failed
```

**Cause**: Device hasn't registered with SRP yet, or border router hasn't
started proxying mDNS.

**Fix**: Increase `--timeout` to 240. Verify the border router is working
by checking `avahi-browse -r _matter._tcp`.

### "Discovered device does not have an open commissioning window"

The device is already commissioned or not in commissioning mode. Either:
- Erase flash and reflash: `flash.py --erase devices/<device>`
- Open a commissioning window from the existing commissioner

### eth0 "Network is unreachable" warnings

```
Warning: Attempt to mDNS broadcast failed on eth0
```

This is harmless — it just means the eth0 interface has no IPv6. mDNS
succeeds on wlp192s0 (WiFi) which is what matters.
