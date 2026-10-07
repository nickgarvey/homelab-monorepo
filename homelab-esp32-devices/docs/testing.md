# Testing

## Unit Tests (host)

Host-compiled tests using [Unity](https://github.com/ThrowTheSwitch/Unity). No ESP32 hardware needed.

```bash
nix flake check        # runs all checks including unit tests
nix build .#tests      # build and run tests explicitly
nix log .#tests        # view test output from the last build
```

### Structure

| File | Tests |
|---|---|
| `test_wifi_manager.c` | WiFi manager init and state |
| `test_ha_client.c` | Home Assistant REST API client |
| `test_neopixel.c` | NeoPixel LED driver |
| `test_openthread_manager.c` | OpenThread lifecycle |
| `test_fake_temp_sensor.c` | Fake sine-wave temperature generator |
| `test_ds18b20_reader.c` | Real DS18B20 reader (init-once, read-many) |

### Mocks

All ESP-IDF and hardware dependencies are mocked in `tests/mocks/`:

- `esp_idf_mocks.h` — core ESP-IDF types (`esp_err_t`, `ESP_OK`, FreeRTOS stubs)
- `ds18b20_mocks.c` — 1-Wire bus and DS18B20 sensor stubs with controllable state
- `test_helpers.h` — declarations for mock control functions

Mock control functions follow the pattern `<module>_mock_set_<thing>()` and `<module>_mock_reset()`.

### Writing Tests for Hardware Drivers

Hardware init functions that allocate limited resources (RMT channels, SPI buses, I2C ports) must be called once and reused. To enforce this in tests:

1. Add a call counter to the mock (e.g., `s_bus_init_count` in `ds18b20_mocks.c`)
2. Expose it via `<module>_mock_get_<thing>_count()`
3. Write a test that calls the function N times and asserts the init count is 1

Example from `test_ds18b20_reader.c`:

```c
void test_ds18b20_reader_inits_bus_once(void)
{
    ds18b20_mock_reset();
    ds18b20_reader_reset();
    ds18b20_mock_set_temperature(25.0f);

    float t;
    ds18b20_reader_read(5, &t);
    ds18b20_reader_read(5, &t);
    ds18b20_reader_read(5, &t);

    TEST_ASSERT_EQUAL_INT(1, ds18b20_mock_get_bus_init_count());
}
```

---

## Firmware Verification Checks

Also run as part of `nix flake check`. These build each firmware and validate the output:

- Binary exists and is non-trivially sized (>128 KiB)
- `esptool.py image_info` validates the image header and checksum
- Expected ELF symbols are present (e.g., `app_main`, `ds18b20_reader_read`)

View results:

```bash
nix log .#checks.x86_64-linux.freezer-temp-sensor
nix log .#checks.x86_64-linux.garage-opener
```

---

## Adding a New Test

1. Create `tests/test_<name>.c` with test functions and a `run_<name>_tests()` entry point
2. Add the source file to `tests/CMakeLists.txt`
3. Declare and call `run_<name>_tests()` in `tests/test_runner.c`
4. If testing device-specific code, add the source to CMakeLists and ensure mock headers exist
