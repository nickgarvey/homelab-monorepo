/*
 * Simulated accelerometer for boards without a LIS2DW12 (the Feather used for development):
 * a 1 g reading on z, plus a 10 Hz shake while a simulated wash is on.
 */
#pragma once

#include <stdbool.h>

#include "vibration_monitor.h"

#ifdef __cplusplus
extern "C" {
#endif

void fake_accel_set_washing(bool on);
bool fake_accel_washing(void);

/* Fill n samples at sample_hz, continuing from the previous call. */
void fake_accel_generate(vm_sample_t *out, int n, int sample_hz);

#ifdef __cplusplus
}
#endif
