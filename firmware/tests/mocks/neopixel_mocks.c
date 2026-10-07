#include "esp_idf_mocks.h"
#include <stdlib.h>

/* ---- LED strip mock ---------------------------------------------------- */

struct LedStrip {
    uint8_t r, g, b;
};

static struct LedStrip s_strip_instance;
static uint8_t s_last_r, s_last_g, s_last_b;

esp_err_t led_strip_new_rmt_device(const led_strip_config_t *strip_config,
                                    const led_strip_rmt_config_t *rmt_config,
                                    led_strip_handle_t *ret_strip)
{
    (void)strip_config;
    (void)rmt_config;
    *ret_strip = (led_strip_handle_t)&s_strip_instance;
    return ESP_OK;
}

esp_err_t led_strip_set_pixel(led_strip_handle_t strip, uint32_t index,
                               uint32_t red, uint32_t green, uint32_t blue)
{
    (void)index;
    struct LedStrip *s = (struct LedStrip *)strip;
    s->r = (uint8_t)red;
    s->g = (uint8_t)green;
    s->b = (uint8_t)blue;
    s_last_r = s->r;
    s_last_g = s->g;
    s_last_b = s->b;
    return ESP_OK;
}

esp_err_t led_strip_refresh(led_strip_handle_t strip) { (void)strip; return ESP_OK; }
esp_err_t led_strip_clear(led_strip_handle_t strip)   { (void)strip; return ESP_OK; }

/* ---- Test accessors ----------------------------------------------------- */
void neopixel_mock_get_color(uint8_t *r, uint8_t *g, uint8_t *b)
{
    *r = s_last_r;
    *g = s_last_g;
    *b = s_last_b;
}
