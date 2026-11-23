/*
 * Decides whether the washer is running from accelerometer batches.
 *
 * Time is split into one-second buckets; a bucket is active if any batch fed during that
 * second had a peak at or above threshold_mg. The washer is Running once at least
 * bucket_threshold of the last VM_WINDOW_SECONDS buckets are active, and Stopped again once the
 * count has stayed below bucket_threshold for stop_delay_ms (which rides out fill and soak
 * pauses). The monitor is idle (nothing to watch, the accelerometer can go back to waiting for
 * a wake-up) when the washer is Stopped and no bucket in the window is active.
 */
#pragma once

#include <stdbool.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

#define VM_WINDOW_SECONDS 120

typedef struct {
    uint16_t threshold_mg;
    uint16_t bucket_threshold;
    uint32_t stop_delay_ms;
} vibration_monitor_config_t;

typedef struct {
    int16_t x_mg, y_mg, z_mg;
} vm_sample_t;

void vibration_monitor_init(const vibration_monitor_config_t *cfg);

/* Largest distance of any sample from the batch mean, in mg. The mean stands in for gravity
 * and the sensor's offset, so the result is the vibration whatever the board's orientation. */
uint16_t vibration_monitor_batch_peak_mg(const vm_sample_t *samples, int n);

/* Record a batch peak in the current bucket. */
void vibration_monitor_feed(uint16_t peak_mg);

/* Advance time. Commits a bucket for every whole second passed. */
void vibration_monitor_tick(uint32_t elapsed_ms);

bool vibration_monitor_is_running(void);
bool vibration_monitor_is_idle(void);
uint16_t vibration_monitor_active_buckets(void);

#ifdef __cplusplus
}
#endif
