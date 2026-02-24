#pragma once

#include <stdint.h>

/**
 * Initialize the NeoPixel LED.
 *
 * @param gpio_pin   GPIO number connected to the data line of the WS2812
 * @param power_pin  GPIO number for the power enable pin, or -1 if not present
 */
void neopixel_init(int gpio_pin, int power_pin);

/**
 * Set the NeoPixel color.
 *
 * @param r  Red component   (0–255)
 * @param g  Green component (0–255)
 * @param b  Blue component  (0–255)
 */
void neopixel_set(uint8_t r, uint8_t g, uint8_t b);
