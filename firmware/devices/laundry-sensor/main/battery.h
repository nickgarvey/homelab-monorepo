/*
 * 2xAA battery level from the 10M/10M divider on VBAT_SENSE.
 */
#pragma once

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/* Matter PowerSource BatChargeLevelEnum */
typedef enum {
    BATTERY_LEVEL_OK = 0,
    BATTERY_LEVEL_WARNING = 1,
    BATTERY_LEVEL_CRITICAL = 2,
} battery_level_t;

/* Battery voltage from the voltage at the divider midpoint. */
uint32_t battery_mv_from_sense_mv(uint32_t sense_mv);

/* Remaining charge of two alkaline AA cells in series, 0..100, from a curve of cell voltage
 * under light load. The TPS63900 runs down to 1.8 V, so 2.0 V (1.0 V a cell) counts as empty. */
uint8_t battery_percent(uint32_t vbat_mv);

battery_level_t battery_level(uint8_t percent);

#ifdef __cplusplus
}
#endif
