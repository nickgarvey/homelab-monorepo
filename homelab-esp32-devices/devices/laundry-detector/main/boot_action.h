#pragma once

#include <stdbool.h>

typedef enum {
    WAKEUP_FRESH_BOOT = 0,
    WAKEUP_TIMER      = 2,
    WAKEUP_GPIO       = 7,
} wakeup_cause_t;

typedef enum {
    BOOT_ACTION_WAIT_FOR_COMMISSION,  /* Not paired — stay awake for pairing */
    BOOT_ACTION_TIMER_REPORT,         /* Timer wake, commissioned — report and sleep */
    BOOT_ACTION_ACTIVE_MONITOR,       /* GPIO wake, commissioned — vibration monitoring */
    BOOT_ACTION_STARTUP_SLEEP,        /* Fresh boot, commissioned — show status and sleep */
} boot_action_t;

/**
 * Determine what the device should do after booting.
 *
 * @param wakeup       How the device woke up
 * @param commissioned Whether the device is paired to a Matter fabric
 * @return The action to take
 */
boot_action_t determine_boot_action(wakeup_cause_t wakeup, bool commissioned);
