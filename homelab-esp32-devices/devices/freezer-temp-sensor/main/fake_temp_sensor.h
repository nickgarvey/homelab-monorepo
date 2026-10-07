#pragma once

#include <stdbool.h>

/**
 * Fake temperature sensor that produces a sine wave.
 *
 * Oscillates between center - amplitude and center + amplitude
 * with a configurable period. Useful for testing firmware without
 * real hardware attached.
 */

typedef struct {
    float center;       /* midpoint temperature in celsius */
    float amplitude;    /* swing in celsius (+/-) */
    float period_sec;   /* one full cycle in seconds */
} fake_temp_config_t;

/**
 * Initialise the fake sensor with the given wave parameters.
 * Can be called again to reconfigure.
 */
void fake_temp_init(const fake_temp_config_t *cfg);

/**
 * Read the current fake temperature.
 * Uses elapsed time since init (or since the epoch counter was last reset).
 * Returns the temperature in *out_celsius and true on success.
 * Returns false only if fake_temp_init has not been called.
 */
bool fake_temp_read(float *out_celsius);

/**
 * Advance the internal clock by dt seconds.
 * Provided for unit testing so tests don't depend on wall time.
 */
void fake_temp_advance(float dt_sec);

/**
 * Reset internal elapsed time to zero.
 */
void fake_temp_reset_time(void);
