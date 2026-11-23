#include "vibration_monitor.h"

#include <math.h>
#include <string.h>

static vibration_monitor_config_t s_cfg;
static bool s_buckets[VM_WINDOW_SECONDS];
static uint16_t s_idx;
static uint16_t s_active;            /* active buckets in the window */
static bool s_current;               /* current (uncommitted) second has vibration */
static uint32_t s_ms_in_bucket;
static bool s_running;
static uint32_t s_below_ms;          /* time Running with the count below the threshold */

void vibration_monitor_init(const vibration_monitor_config_t *cfg)
{
    s_cfg = *cfg;
    memset(s_buckets, 0, sizeof(s_buckets));
    s_idx = 0;
    s_active = 0;
    s_current = false;
    s_ms_in_bucket = 0;
    s_running = false;
    s_below_ms = 0;
}

uint16_t vibration_monitor_batch_peak_mg(const vm_sample_t *samples, int n)
{
    if (n <= 0) return 0;
    int32_t sx = 0, sy = 0, sz = 0;
    for (int i = 0; i < n; i++) {
        sx += samples[i].x_mg;
        sy += samples[i].y_mg;
        sz += samples[i].z_mg;
    }
    float mx = (float)sx / n, my = (float)sy / n, mz = (float)sz / n;
    float peak = 0.0f;
    for (int i = 0; i < n; i++) {
        float dx = samples[i].x_mg - mx, dy = samples[i].y_mg - my, dz = samples[i].z_mg - mz;
        float d = sqrtf(dx * dx + dy * dy + dz * dz);
        if (d > peak) peak = d;
    }
    if (peak > 65535.0f) peak = 65535.0f;
    return (uint16_t)(peak + 0.5f);
}

void vibration_monitor_feed(uint16_t peak_mg)
{
    if (peak_mg >= s_cfg.threshold_mg) {
        s_current = true;
    }
}

static void commit_second(void)
{
    if (s_buckets[s_idx]) s_active--;
    s_buckets[s_idx] = s_current;
    if (s_current) s_active++;
    s_idx = (uint16_t)((s_idx + 1) % VM_WINDOW_SECONDS);
    s_current = false;

    bool above = s_active >= s_cfg.bucket_threshold;
    if (!s_running) {
        if (above) {
            s_running = true;
            s_below_ms = 0;
        }
    } else if (above) {
        s_below_ms = 0;
    } else {
        s_below_ms += 1000;
        if (s_below_ms >= s_cfg.stop_delay_ms) {
            s_running = false;
            s_below_ms = 0;
        }
    }
}

void vibration_monitor_tick(uint32_t elapsed_ms)
{
    s_ms_in_bucket += elapsed_ms;
    while (s_ms_in_bucket >= 1000) {
        s_ms_in_bucket -= 1000;
        commit_second();
    }
}

bool vibration_monitor_is_running(void)
{
    return s_running;
}

bool vibration_monitor_is_idle(void)
{
    return !s_running && s_active == 0 && !s_current;
}

uint16_t vibration_monitor_active_buckets(void)
{
    return s_active;
}
