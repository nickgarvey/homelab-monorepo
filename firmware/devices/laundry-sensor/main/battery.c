#include "battery.h"

/* Per-cell mV -> percent, alkaline AA at a few mA. */
static const struct { uint16_t mv; uint8_t pct; } CURVE[] = {
    { 1550, 100 }, { 1450, 90 }, { 1350, 70 }, { 1250, 45 },
    { 1150, 20 },  { 1050, 5 },  { 1000, 0 },
};
#define CURVE_LEN (sizeof(CURVE) / sizeof(CURVE[0]))

uint32_t battery_mv_from_sense_mv(uint32_t sense_mv)
{
    return sense_mv * 2;
}

uint8_t battery_percent(uint32_t vbat_mv)
{
    uint32_t cell = vbat_mv / 2;
    if (cell >= CURVE[0].mv) return 100;
    for (unsigned i = 1; i < CURVE_LEN; i++) {
        if (cell >= CURVE[i].mv) {
            uint32_t hi_mv = CURVE[i - 1].mv, lo_mv = CURVE[i].mv;
            uint32_t hi_p = CURVE[i - 1].pct, lo_p = CURVE[i].pct;
            return (uint8_t)(lo_p + (hi_p - lo_p) * (cell - lo_mv) / (hi_mv - lo_mv));
        }
    }
    return 0;
}

battery_level_t battery_level(uint8_t percent)
{
    if (percent <= 10) return BATTERY_LEVEL_CRITICAL;
    if (percent <= 25) return BATTERY_LEVEL_WARNING;
    return BATTERY_LEVEL_OK;
}
