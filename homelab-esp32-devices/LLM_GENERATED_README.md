# homelab-esp32-devices

Monorepo for ESP32-based homelab devices. All devices target Home Assistant via HTTPS.

## Repo Layout

```
homelab-esp32-devices/
├── common/                    # Shared ESP-IDF component (used by all devices)
│   ├── CMakeLists.txt
│   ├── idf_component.yml
│   ├── include/
│   │   ├── wifi_manager.h     # WiFi init + connection status
│   │   ├── ha_client.h        # Home Assistant HTTPS POST/GET
│   │   └── neopixel.h         # WS2812 color API (no status semantics)
│   └── src/
│       ├── wifi_manager.c
│       ├── ha_client.c
│       └── neopixel.c
├── devices/
│   └── garage-opener/         # featheresp32-s2, controls cover.garage_door via HA
├── tests/
│   ├── CMakeLists.txt         # Native host build (gcc + Unity, no hardware needed)
│   ├── mocks/                 # ESP-IDF stub headers and mock implementations
│   ├── test_wifi_manager.c
│   ├── test_ha_client.c
│   ├── test_neopixel.c
│   ├── test_runner.c
│   ├── test_build.sh          # Firmware build verification for garage-opener
│   └── run_all.sh             # Runs unit tests + build verification
├── flake.nix                  # Nix devShell: esp-idf-full + gcc + cmake + ninja
└── PROMPT.md                  # Original project spec
```

## Device Overview

| Device | Board | Power | HA Integration | Status |
|--------|-------|-------|----------------|--------|
| `garage-opener` | featheresp32-s2 | Wired | `cover.garage_door` toggle + state poll | Active |
| `vibration-sensor` | featheresp32-c6 | Battery | Vibration events → HA sensor | Planned |
| `temperature-sensor` | featheresp32-c6 | Wired | Temperature readings → HA sensor | Planned |

## Common Component API

### `wifi_manager`

```c
// Connect to WiFi. Blocks until connected or retries exhausted.
void wifi_manager_init(const char *ssid, const char *password, int max_retries);

// Returns true if the station has an active IP address.
bool wifi_manager_connected(void);
```

### `ha_client`

```c
// Configure once at startup. ca_pem is a NULL-terminated PEM string.
void ha_client_init(const char *base_url, const char *api_key, const char *ca_pem);

// Returns HTTP status code or -1 on transport error.
int ha_post(const char *path, const char *json_body);
int ha_get(const char *path, char *out_buf, int out_cap);
```

### `neopixel`

```c
// power_pin: GPIO that enables LED power rail, or -1 if not present.
void neopixel_init(int gpio_pin, int power_pin);

// Set the onboard WS2812 color. No status semantics — caller decides meaning.
void neopixel_set(uint8_t r, uint8_t g, uint8_t b);
```

## Building a Device

Enter the dev environment, then build from the device directory:

```bash
nix develop
idf.py -C devices/garage-opener build
```

Flash (requires connected board):

```bash
idf.py -C devices/garage-opener flash monitor
```

## Running Tests

All tests (unit + build verification):

```bash
nix develop --command bash tests/run_all.sh
```

Unit tests only (no hardware, no ESP-IDF toolchain needed beyond gcc/cmake):

```bash
nix develop --command bash -c "cmake -S tests -B tests/build && cmake --build tests/build && ./tests/build/run_tests"
```

Build verification only (requires ESP-IDF, builds garage-opener and validates the image):

```bash
nix develop --command bash tests/test_build.sh
```

## Secrets

Each device has `include/secrets/ha_key.h` which is gitignored. Copy the example and fill in your token:

```bash
cp devices/garage-opener/include/secrets/ha_key.h.example \
   devices/garage-opener/include/secrets/ha_key.h
# edit ha_key.h and replace the placeholder with your real token
```

The `test_build.sh` script auto-generates a placeholder key if the file is absent, so CI does not require real credentials.

## Adding a New Device

1. Create `devices/<name>/` with the following structure:

```
devices/<name>/
├── CMakeLists.txt
├── sdkconfig.defaults
├── include/secrets/ha_key.h.example
└── main/
    ├── CMakeLists.txt
    ├── idf_component.yml
    └── main.c
```

2. `CMakeLists.txt` — add `common` to the component search path:

```cmake
cmake_minimum_required(VERSION 3.16)
list(APPEND EXTRA_COMPONENT_DIRS "${CMAKE_CURRENT_SOURCE_DIR}/../../common")
include($ENV{IDF_PATH}/tools/cmake/project.cmake)
project(<name>)
```

3. `main/CMakeLists.txt` — declare sources and require `common`:

```cmake
idf_component_register(
    SRCS "main.c"
    INCLUDE_DIRS "." "${CMAKE_SOURCE_DIR}/include"
    REQUIRES common
)
```

4. `sdkconfig.defaults` — set the target chip:

```
CONFIG_IDF_TARGET="esp32c6"   # or esp32s2, etc.
```

5. Call the common APIs in `main.c`:

```c
#include "wifi_manager.h"
#include "ha_client.h"
#include "neopixel.h"

void app_main(void) {
    neopixel_init(/* gpio */ 8, /* power_pin */ -1);
    wifi_manager_init("MySSID", "MyPass", 5);
    ha_client_init("https://homeassistant.home.arpa:8123", HA_API_KEY, ca_pem_start);
    // device-specific logic here
}
```

6. To add the device to build verification, extend `tests/test_build.sh` with a new build block following the same pattern as the garage-opener section.

## Conventions

- **Status-to-color mapping** belongs in the device, not in `common/neopixel`. `neopixel_set` is a raw color API.
- **Device-specific HA entity IDs and endpoints** are defined as macros in the device's `main.c`.
- **Secrets** (`ha_key.h`) are always gitignored. Each device provides a `.example` file.
- **Common** only grows when two or more devices share the same logic verbatim.
- **Tests** for `common/` logic use native GCC + Unity mocks (no flashing). Hardware-specific behavior is verified by `test_build.sh`.
