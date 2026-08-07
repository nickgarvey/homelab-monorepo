#pragma once

#include <stdint.h>

typedef enum {
    STATUS_OFF,
    STATUS_STARTUP,         /* white */
    STATUS_CONNECTED,       /* green */
    STATUS_BATTERY_LOW,     /* red */
    STATUS_ACTIVE,          /* dark blue — monitoring, no vibration */
    STATUS_ACTIVE_VIBRATING,/* cyan/light blue — vibration detected */
    STATUS_UNCOMMISSIONED,  /* yellow — waiting for Matter pairing */
    STATUS_SLEEPING,        /* off (called before deep sleep) */
} status_led_state_t;

void status_led_init(int gpio_pin, int power_pin);
void status_led_set(status_led_state_t state);
