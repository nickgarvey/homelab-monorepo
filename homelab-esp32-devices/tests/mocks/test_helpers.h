#pragma once

#include <stdint.h>

/* http_mocks.c */
void        http_mock_set_response(int status, const char *body);
const char *http_mock_last_url(void);
const char *http_mock_last_auth(void);
const char *http_mock_last_body(void);
void        http_mock_reset(void);

/* neopixel_mocks.c */
void neopixel_mock_get_color(uint8_t *r, uint8_t *g, uint8_t *b);
