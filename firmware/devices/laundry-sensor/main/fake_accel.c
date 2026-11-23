#include "fake_accel.h"

#include <math.h>

#define SHAKE_HZ 10.0f
#define SHAKE_MG 200.0f

static bool s_washing;
static unsigned long s_sample;

void fake_accel_set_washing(bool on)
{
    s_washing = on;
}

bool fake_accel_washing(void)
{
    return s_washing;
}

void fake_accel_generate(vm_sample_t *out, int n, int sample_hz)
{
    for (int i = 0; i < n; i++, s_sample++) {
        float t = (float)s_sample / (float)sample_hz;
        float shake = s_washing ? SHAKE_MG * sinf(2.0f * (float)M_PI * SHAKE_HZ * t + 0.3f) : 0.0f;
        out[i].x_mg = (int16_t)shake;
        out[i].y_mg = (int16_t)(shake / 2);
        out[i].z_mg = 1000;
    }
}
