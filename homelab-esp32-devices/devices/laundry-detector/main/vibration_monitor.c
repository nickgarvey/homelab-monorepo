#include "vibration_monitor.h"
#include <string.h>

static vibration_monitor_config_t s_cfg;
static vibration_monitor_state_t s_state;

/* Rolling window of 1-second buckets.  Each entry is true if at least one
 * sample in that second exceeded the vibration threshold. */
static bool s_buckets[VM_WINDOW_SECONDS];
static uint16_t s_bucket_idx;          /* current write position in ring */
static bool s_current_bucket_active;   /* any vibration in current second? */

static uint32_t s_ms_in_current_bucket;
static uint32_t s_quiet_elapsed_ms;
static uint32_t s_report_elapsed_ms;
static bool s_report_ready;

void vibration_monitor_init(const vibration_monitor_config_t *cfg)
{
    s_cfg = *cfg;
    s_state = VM_STATE_STARTUP;
    memset(s_buckets, 0, sizeof(s_buckets));
    s_bucket_idx = 0;
    s_current_bucket_active = false;
    s_ms_in_current_bucket = 0;
    s_quiet_elapsed_ms = 0;
    s_report_elapsed_ms = 0;
    s_report_ready = false;
}

void vibration_monitor_feed_sample(uint16_t magnitude_mg)
{
    if (magnitude_mg >= s_cfg.threshold_mg) {
        s_current_bucket_active = true;
        s_quiet_elapsed_ms = 0;
    }
}

void vibration_monitor_tick(uint32_t elapsed_ms)
{
    s_ms_in_current_bucket += elapsed_ms;
    s_quiet_elapsed_ms += elapsed_ms;
    s_report_elapsed_ms += elapsed_ms;

    /* Advance to next bucket every 1000ms */
    while (s_ms_in_current_bucket >= 1000) {
        s_ms_in_current_bucket -= 1000;

        /* Commit current bucket */
        s_buckets[s_bucket_idx] = s_current_bucket_active;
        s_bucket_idx = (s_bucket_idx + 1) % VM_WINDOW_SECONDS;

        /* Clear the next bucket (it's about to be overwritten next time) */
        s_buckets[s_bucket_idx] = false;
        s_current_bucket_active = false;
    }

    /* Report readiness */
    if (s_state == VM_STATE_ACTIVE &&
        s_report_elapsed_ms >= s_cfg.report_interval_ms) {
        s_report_elapsed_ms = 0;
        s_report_ready = true;
    }
}

vibration_monitor_state_t vibration_monitor_get_state(void)
{
    return s_state;
}

uint16_t vibration_monitor_get_active_buckets(void)
{
    uint16_t count = 0;
    for (uint16_t i = 0; i < VM_WINDOW_SECONDS; i++) {
        if (s_buckets[i])
            count++;
    }
    return count;
}

bool vibration_monitor_is_running(void)
{
    return vibration_monitor_get_active_buckets() >= s_cfg.bucket_threshold;
}

bool vibration_monitor_report_ready(void)
{
    if (s_report_ready) {
        s_report_ready = false;
        return true;
    }
    return false;
}

bool vibration_monitor_should_sleep(void)
{
    if (s_state != VM_STATE_ACTIVE)
        return false;
    return s_quiet_elapsed_ms > s_cfg.quiet_timeout_ms;
}

void vibration_monitor_notify_wake(void)
{
    s_state = VM_STATE_ACTIVE;
    s_quiet_elapsed_ms = 0;
}

void vibration_monitor_reset(void)
{
    s_state = VM_STATE_STARTUP;
    memset(s_buckets, 0, sizeof(s_buckets));
    s_bucket_idx = 0;
    s_current_bucket_active = false;
    s_ms_in_current_bucket = 0;
    s_quiet_elapsed_ms = 0;
    s_report_elapsed_ms = 0;
    s_report_ready = false;
}
