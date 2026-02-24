#include "neopixel.h"

#include "driver/gpio.h"
#include "led_strip.h"
#include "esp_log.h"

static const char *TAG = "neopixel";

static led_strip_handle_t s_led_strip;

void neopixel_init(int gpio_pin, int power_pin)
{
    if (power_pin >= 0) {
        gpio_config_t pwr_cfg = {
            .pin_bit_mask = (1ULL << power_pin),
            .mode = GPIO_MODE_OUTPUT,
        };
        ESP_ERROR_CHECK(gpio_config(&pwr_cfg));
        gpio_set_level(power_pin, 1);
    }

    led_strip_config_t strip_config = {
        .strip_gpio_num = gpio_pin,
        .max_leds = 1,
        .led_pixel_format = LED_PIXEL_FORMAT_GRB,
        .led_model = LED_MODEL_WS2812,
        .flags.invert_out = false,
    };
    led_strip_rmt_config_t rmt_config = {
        .clk_src = RMT_CLK_SRC_DEFAULT,
        .resolution_hz = 10 * 1000 * 1000,
        .flags.with_dma = false,
    };
    ESP_ERROR_CHECK(led_strip_new_rmt_device(&strip_config, &rmt_config, &s_led_strip));
    led_strip_clear(s_led_strip);
    ESP_LOGI(TAG, "NeoPixel initialized on GPIO %d", gpio_pin);
}

void neopixel_set(uint8_t r, uint8_t g, uint8_t b)
{
    led_strip_set_pixel(s_led_strip, 0, r, g, b);
    led_strip_refresh(s_led_strip);
    ESP_LOGI(TAG, "LED -> r=%d g=%d b=%d", r, g, b);
}
