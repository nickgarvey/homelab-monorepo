#pragma once

#include <stdbool.h>
#include <stdint.h>

typedef struct {
    int sda_gpio;
    int scl_gpio;
    void *i2c_bus;  /* If non-NULL, reuse this I2C bus handle instead of creating a new one */
} max17048_config_t;

/**
 * Initialize the MAX17048 fuel gauge over I2C.
 */
bool max17048_init(const max17048_config_t *cfg);

/**
 * Read battery voltage in volts.
 */
bool max17048_read_voltage(float *out_volts);

/**
 * Read battery state-of-charge percentage (0-100).
 */
bool max17048_read_soc(uint8_t *out_percent);

/**
 * Reset internal state (for testing).
 */
void max17048_reset(void);
