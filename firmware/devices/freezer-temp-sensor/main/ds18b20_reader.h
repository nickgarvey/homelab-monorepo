#pragma once
#include <stdbool.h>

/**
 * Read temperature from the DS18B20 sensor.
 *
 * On first call, initializes the 1-Wire bus and DS18B20 device handle.
 * Subsequent calls reuse the existing handle (no RMT channel re-allocation).
 *
 * @param gpio_num  GPIO pin for the 1-Wire data line
 * @param out_celsius  Output: temperature in degrees Celsius
 * @return true on success, false on failure
 */
bool ds18b20_reader_read(int gpio_num, float *out_celsius);

/**
 * Reset internal state (for testing). Next read will re-initialize the bus.
 */
void ds18b20_reader_reset(void);
