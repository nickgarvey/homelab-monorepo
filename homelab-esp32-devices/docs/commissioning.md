# Matter Commissioning over Thread

This repo uses **on-network commissioning** — Thread credentials are embedded at
flash time so devices join the Thread network automatically.  The commissioner
(Home Assistant) discovers them via mDNS through the border router and commissions
over IP.  No Bluetooth is required.

## How it works

```
flash.py --thread
    │
    ├── sops --decrypt secrets/thread.sops.yaml
    │       → THREAD_DATASET_TLV=0e08...
    │
    ├── nvs_partition_gen.py
    │       → NVS binary (namespace "thread", key "dataset_tlv", hex2bin)
    │
    ├── esptool write_flash  (firmware)
    └── esptool write_flash 0x9000  (NVS partition with Thread creds)
```

On boot the firmware:

1. Initialises NVS and the Matter stack (which starts OpenThread).
2. Calls `provision_thread_from_nvs()` — reads the dataset TLV blob from NVS
   namespace `thread` / key `dataset_tlv`.
3. Provisions Thread via `ThreadStackMgr().SetThreadProvision()` +
   `SetThreadEnabled(true)`.
4. The device joins the Thread network (typically child → router in ~25 s).
5. Registers with the border router's SRP server → discoverable via mDNS.
6. The commissioner (HA) discovers the device on-network and commissions over IP.

## Prerequisites

- **Thread border router** running on HA (e.g. the SkyConnect or a separate OTBR).
- **Thread operational dataset** stored in `secrets/thread.sops.yaml` (SOPS-encrypted
  with age).  The dataset hex TLV comes from the border router — see
  [Getting the Thread dataset](#getting-the-thread-dataset) below.
- **Matter Server addon** on HA with `enable_test_net_dcl: true` (our devices use
  the esp-matter test VID `0xFFF1`).
- **sops** and **age** available (provided by `nix develop`).

## Flashing with Thread credentials

```bash
# Enter the Nix dev shell (provides sops, esptool, python3, etc.)
nix develop

# Flash with --thread to embed Thread credentials, --erase for a clean start
python3 scripts/flash.py --erase --thread devices/laundry-detector
python3 scripts/flash.py --erase --thread devices/freezer-temp-sensor
```

The `--thread` flag:
1. Decrypts `secrets/thread.sops.yaml` via `sops --decrypt`.
2. Generates an NVS partition with the Thread dataset TLV.
3. Flashes the NVS partition at offset `0x9000` after the main firmware.

Without `--thread`, the device still boots and starts Matter but will not have
Thread credentials.  It falls back to BLE commissioning (CHIPoBLE).

## Commissioning from Home Assistant

After flashing with `--thread`, the device joins Thread and becomes discoverable
automatically.  To commission:

1. Open the HA UI → **Settings → Devices & Services → Add Integration → Matter**.
2. Enter the QR code from the serial output or `matter-pairing.json`:

   | Device            | QR Code                    | Manual Code   |
   |-------------------|----------------------------|---------------|
   | laundry-detector  | `MT:Y.K90AFN00KA0648G00`   | `34970112332` |
   | freezer-temp-sensor | `MT:Y.K90AFN00KA0648G00` | `34970112332` |

3. HA discovers the device on the Thread network via mDNS and commissions it.
   This takes ~10 seconds.

Alternatively, commission via the HA WebSocket API:

```json
{"type": "matter/commission", "code": "MT:Y.K90AFN00KA0648G00"}
```

Or via chip-tool (see `docs/chip-tool.md`):

```bash
chip-tool pairing code-thread <node-id> hex:$DATASET MT:Y.K90AFN00KA0648G00 \
    --bypass-attestation-verifier true --storage-directory /tmp/chip-tool-storage
```

## Getting the Thread dataset

The Thread operational dataset is a hex-encoded TLV blob that contains the network
name, channel, PAN ID, network key, and other parameters.

### From the HA OpenThread Border Router addon

```bash
# SSH into HA
ssh root@homeassistant.local

# Get the active dataset hex
ha addons info core_openthread_border_router
# Or via the REST API:
curl -s http://core-openthread-border-router:8081/node/dataset/active | python3 -c \
    "import sys,json; print(json.load(sys.stdin)['TLVs'])"
```

### Updating the SOPS secret

```bash
# Inside nix develop:
echo "THREAD_DATASET_TLV: <hex-tlv>" > /tmp/thread-plain.yaml
sops --encrypt --age "$(grep recipient secrets/thread.sops.yaml | head -1 | awk '{print $NF}'),$(grep recipient secrets/thread.sops.yaml | tail -1 | awk '{print $NF}')" \
    /tmp/thread-plain.yaml > secrets/thread.sops.yaml
rm /tmp/thread-plain.yaml
```

Or edit in-place (sops opens your `$EDITOR`):

```bash
sops secrets/thread.sops.yaml
```

## Troubleshooting

### Device doesn't join Thread

Check serial output for:
```
I (xxx) laundry: Found Thread dataset in NVS (N bytes), provisioning...
I (xxx) laundry: Thread provisioned from NVS — device will join network
```

If you see `No thread NVS namespace — skipping Thread provisioning`, the NVS
partition wasn't flashed.  Re-run with `--thread --erase`.

If Thread provisioning succeeds but the device doesn't attach, verify:
- The Thread dataset matches the active network (channel, PAN ID, network key).
- The border router is running and reachable.
- The device is within radio range of another Thread router/border router.

### Commissioner can't find the device

After joining Thread, the device registers with the SRP server on the border
router.  The border router then proxies mDNS so the commissioner can discover
it.  This can take 5–15 seconds.

Verify the device is advertising:
```bash
avahi-browse -r _matter._tcp
```

### "Already commissioned" / no new entities

If the device was previously commissioned, `--erase` clears the NVS (including
fabric data).  The device will be in an uncommissioned state after erasing.

### Test attestation rejected

Our devices use the esp-matter test VID (`0xFFF1`) and test DAC.  The Matter
Server addon must have `enable_test_net_dcl: true` in its configuration to
accept test attestation.  Production commissioners (Apple Home, Google Home)
will reject these devices.
