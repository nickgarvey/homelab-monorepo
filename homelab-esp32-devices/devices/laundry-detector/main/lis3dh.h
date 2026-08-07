#pragma once

#include <stdbool.h>
#include <stdint.h>

typedef struct {
    int sda_gpio;
    int scl_gpio;
    uint8_t addr;     /* 0x18 default */
} lis3dh_config_t;

typedef struct {
    int16_t x_mg;
    int16_t y_mg;
    int16_t z_mg;
} lis3dh_accel_t;

/**
 * Initialize the LIS3DH accelerometer over I2C.
 * Creates or reuses the I2C master bus.
 * Returns false if WHO_AM_I check fails or I2C error.
 */
bool lis3dh_init(const lis3dh_config_t *cfg);

/**
 * Read current acceleration in milligravities.
 */
bool lis3dh_read_accel(lis3dh_accel_t *out);

/**
 * Configure the LIS3DH to generate an interrupt on INT1 when
 * motion exceeds threshold_mg for duration_ms.
 */
bool lis3dh_configure_motion_interrupt(uint8_t threshold_mg, uint8_t duration_ms);

/**
 * Get the I2C bus handle created during init (for sharing with other devices).
 */
void *lis3dh_get_i2c_bus(void);

/**
 * Reset internal state (for testing).
 */
void lis3dh_reset(void);
