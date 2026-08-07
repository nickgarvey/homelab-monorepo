#pragma once

#include <stdbool.h>
#include <stdint.h>

/* Rolling window of 1-second buckets.  Each bucket is true if any sample
 * in that second exceeded the vibration threshold. */
#define VM_WINDOW_SECONDS 120

typedef struct {
    uint16_t threshold_mg;          /* acceleration threshold for "vibrating" */
    uint32_t quiet_timeout_ms;      /* ms without vibration before sleeping */
    uint32_t report_interval_ms;    /* ms between reports to Matter */
    uint16_t bucket_threshold;      /* # of true buckets in window to be "running" */
} vibration_monitor_config_t;

typedef enum {
    VM_STATE_STARTUP,
    VM_STATE_SLEEPING,
    VM_STATE_ACTIVE,
} vibration_monitor_state_t;

void vibration_monitor_init(const vibration_monitor_config_t *cfg);

/** Feed a single acceleration sample magnitude in mg (gravity-subtracted). */
void vibration_monitor_feed_sample(uint16_t magnitude_mg);

/** Advance internal timers by elapsed_ms. */
void vibration_monitor_tick(uint32_t elapsed_ms);

vibration_monitor_state_t vibration_monitor_get_state(void);

/** Count of true (vibrating) buckets in the rolling window. */
uint16_t vibration_monitor_get_active_buckets(void);

/** True if active buckets in the rolling window exceed bucket_threshold. */
bool vibration_monitor_is_running(void);

/** True when a new report interval has elapsed. Resets flag after read. */
bool vibration_monitor_report_ready(void);

/** True when quiet timeout has elapsed in ACTIVE state. */
bool vibration_monitor_should_sleep(void);

/** Transition to ACTIVE state (called on vibration wake). */
void vibration_monitor_notify_wake(void);

/** Reset all state (for testing). */
void vibration_monitor_reset(void);
