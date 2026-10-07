#include "boot_action.h"

boot_action_t determine_boot_action(wakeup_cause_t wakeup, bool commissioned)
{
    if (!commissioned)
        return BOOT_ACTION_WAIT_FOR_COMMISSION;

    switch (wakeup) {
    case WAKEUP_TIMER:
        return BOOT_ACTION_TIMER_REPORT;
    case WAKEUP_GPIO:
        return BOOT_ACTION_ACTIVE_MONITOR;
    default:
        return BOOT_ACTION_STARTUP_SLEEP;
    }
}
