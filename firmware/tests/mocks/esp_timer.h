#pragma once
#include <stdint.h>

/* Host tests don't check timings; a constant clock is enough. */
static inline int64_t esp_timer_get_time(void) { return 0; }
