#include "status_led.h"
#include "neopixel.h"

#define LED_DIM 24

void status_led_init(int gpio_pin, int power_pin)
{
    neopixel_init(gpio_pin, power_pin);
}

void status_led_set(status_led_state_t state)
{
    switch (state) {
    case STATUS_STARTUP:
        neopixel_set(LED_DIM, LED_DIM, LED_DIM);
        break;
    case STATUS_CONNECTED:
        neopixel_set(0, LED_DIM, 0);
        break;
    case STATUS_BATTERY_LOW:
        neopixel_set(LED_DIM, 0, 0);
        break;
    case STATUS_ACTIVE:
        neopixel_set(0, 0, LED_DIM);       /* dark blue */
        break;
    case STATUS_ACTIVE_VIBRATING:
        neopixel_set(0, LED_DIM, LED_DIM);  /* cyan */
        break;
    case STATUS_UNCOMMISSIONED:
        neopixel_set(LED_DIM, LED_DIM, 0);
        break;
    case STATUS_OFF:
    case STATUS_SLEEPING:
    default:
        neopixel_set(0, 0, 0);
        break;
    }
}
