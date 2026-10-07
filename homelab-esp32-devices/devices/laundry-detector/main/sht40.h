#pragma once

#include <stdbool.h>
#include <stdint.h>

typedef struct {
    int sda_gpio;
    int scl_gpio;
    void *i2c_bus;  /* If non-NULL, reuse this I2C bus handle */
} sht40_config_t;

typedef struct {
    float temperature_c;
    float humidity_pct;
} sht40_reading_t;

/**
 * Initialize the SHT40 sensor over I2C (address 0x44).
 */
bool sht40_init(const sht40_config_t *cfg);

/**
 * Read temperature (°C) and relative humidity (%).
 * Issues a high-precision measurement command and reads the result.
 */
bool sht40_read(sht40_reading_t *out);

/**
 * Reset internal state (for testing).
 */
void sht40_reset(void);
