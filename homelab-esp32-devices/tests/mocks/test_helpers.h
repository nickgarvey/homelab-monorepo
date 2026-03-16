#pragma once

#include <stdint.h>

/* http_mocks.c */
void        http_mock_set_response(int status, const char *body);
void        http_mock_set_perform_result(esp_err_t result);
const char *http_mock_last_url(void);
const char *http_mock_last_auth(void);
const char *http_mock_last_body(void);
void        http_mock_reset(void);

/* neopixel_mocks.c */
void neopixel_mock_get_color(uint8_t *r, uint8_t *g, uint8_t *b);

/* ds18b20_mocks.c */
void ds18b20_mock_set_bus_init_result(esp_err_t result);
void ds18b20_mock_set_temperature(float t);
void ds18b20_mock_reset(void);

/* system_mocks.c */
extern int g_deep_sleep_count;
void system_mock_reset(void);
