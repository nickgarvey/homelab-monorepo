# Laundry Sensor

Battery-powered washer sensor on the board in `hardware/laundry-sensor/`: reports Running or
Stopped, plus temperature, humidity and battery, to Home Assistant over Matter/Thread.

---

## Hardware

| Function | Part | GPIO |
|---|---|---|
| MCU, Thread radio | ESP32-C6-MINI-1 | — |
| Accelerometer | ST LIS2DW12, 4-wire SPI (mode 3, 1 MHz) | CS 18, SCLK 19, MOSI 20, MISO 21 |
| Wake-up interrupt | LIS2DW12 INT1 | 0 |
| FIFO interrupt | LIS2DW12 INT2 | 1 |
| Temperature, humidity | Sensirion SHT40, I2C 0x44 | SDA 22, SCL 23 |
| Battery | 10M/10M divider from VBAT, 100 nF | 2 (ADC1) |
| LED | red, 5.1k | 14 |
| USER button | to GND, 100k pull-up | 3 |
| Power | 2xAA, TPS63900 buck-boost to 3.3 V | — |

## Firmware variants

| Variant | Nix package | Hardware |
|---|---|---|
| Production | `laundry-sensor` | the PCB |
| Development | `laundry-sensor-feather` | bare Adafruit ESP32-C6 Feather; accelerometer, SHT40 and battery simulated |

`flash.py --fake-sensor devices/laundry-sensor` flashes the Feather build. On the Feather the
BOOT button starts and stops a simulated wash (1 blink: started, 2: stopped); the red LED is on
GPIO 15.

```
nix develop .#esp32 --command python3 scripts/flash.py --fake-sensor --thread --erase devices/laundry-sensor
nix develop .#esp32 --command python3 scripts/flash.py --thread --erase devices/laundry-sensor
```

`--thread --erase` writes the Thread dataset from `secrets/thread.sops.yaml` (see
[commissioning.md](commissioning.md)); without `--erase` the dataset already in NVS is kept.

---

## Power

The board stays attached to Thread as a sleepy end device: a short-idle-time Matter ICD that
polls its parent every 5 s and otherwise sits in automatic light sleep. It sends its own reports
as soon as something changes and at least every 5 minutes, so Home Assistant keeps it available.
The settings are in `sdkconfig.defaults` (`CONFIG_ICD_*`, `CONFIG_PM_ENABLE`).

While a USB host is attached the chip does not light-sleep
(`CONFIG_USJ_NO_AUTO_LS_ON_CONNECTION`), so logs and flashing work. Measure current on
batteries through the PPK2 header (J2), with USB unplugged.

## Washer detection

1. **Armed** (between washes): LIS2DW12 in low-power mode 1 at 12.5 Hz, about 1 uA, with its
   wake-up interrupt on INT1 at `CONFIG_VIBRATION_THRESHOLD_MG` (rounded up to 31.25 mg steps).
2. **Monitoring**: on a wake-up the accelerometer streams at 50 Hz into its FIFO and raises INT2
   every 25 samples. For each batch the firmware takes the largest distance of any sample from
   the batch mean (gravity and the sensor offset drop out, whatever the orientation) and marks
   the current second as vibrating if it reaches the threshold.
3. **Running** once `CONFIG_BUCKET_THRESHOLD` of the last 120 seconds were vibrating.
   **Stopped** once the count has stayed below that for `CONFIG_STOP_DELAY_MIN` minutes, which
   rides out fill and soak pauses. A Running-to-Stopped change also sends the Operational State
   cluster's OperationCompletion event.
4. When the washer is Stopped and the last 120 seconds were quiet, the accelerometer goes back
   to armed.

Every 10 s of monitoring the log prints the batch peak and the active-second count, which is the
data for tuning the thresholds against a real wash.

| Setting | Default |
|---|---|
| `CONFIG_VIBRATION_THRESHOLD_MG` | 90 |
| `CONFIG_BUCKET_THRESHOLD` | 15 (of 120 s) |
| `CONFIG_STOP_DELAY_MIN` | 10 |
| `CONFIG_ENV_REPORT_INTERVAL_MIN` | 15 |

## Matter

One endpoint, device type Laundry Washer (0x0073):

| Cluster | Content |
|---|---|
| Operational State | Stopped / Running; Start, Stop, Pause and Resume are rejected |
| Power Source (battery) | BatPercentRemaining, BatVoltage, BatChargeLevel (Warning at 25 %, Critical at 10 %) |
| Temperature Measurement | SHT40, 0.01 °C |
| Relative Humidity Measurement | SHT40, 0.01 % |

Battery percent comes from a curve for alkaline AA cells (`battery.c`); 2.0 V (1.0 V a cell)
counts as empty, above the TPS63900's 1.8 V minimum.

## Button and LED

| Action | Result |
|---|---|
| Short press (PCB) | 1-4 blinks, one per quarter of battery left |
| Short press (Feather) | start or stop the simulated wash |
| Hold 10 s | fast blinks, then factory reset (fabrics erased; re-commission) |
| Slow blink every 3 s | not commissioned yet |
| 3 blinks | Identify from Home Assistant |

## Tests

`tests/test_lis2dw12.c`, `tests/test_vibration_monitor.c` and `tests/test_sht40_battery.c` run
on the host (`nix develop .#esp32 --command bash tests/run_all.sh`). The drivers take their
bus as callbacks, so the tests check register traffic without ESP-IDF mocks.
