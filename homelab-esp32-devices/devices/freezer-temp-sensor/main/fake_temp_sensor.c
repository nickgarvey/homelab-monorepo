#include "fake_temp_sensor.h"
#include <stdbool.h>
#include <math.h>

#ifndef M_PI
#define M_PI 3.14159265358979323846
#endif

static fake_temp_config_t s_cfg;
static float s_elapsed_sec;
static bool  s_initialised;

void fake_temp_init(const fake_temp_config_t *cfg)
{
    s_cfg = *cfg;
    s_elapsed_sec = 0.0f;
    s_initialised = true;
}

bool fake_temp_read(float *out_celsius)
{
    if (!s_initialised) {
        return false;
    }
    float phase = (2.0f * (float)M_PI * s_elapsed_sec) / s_cfg.period_sec;
    *out_celsius = s_cfg.center + s_cfg.amplitude * sinf(phase);
    return true;
}

void fake_temp_advance(float dt_sec)
{
    s_elapsed_sec += dt_sec;
}

void fake_temp_reset_time(void)
{
    s_elapsed_sec = 0.0f;
}
