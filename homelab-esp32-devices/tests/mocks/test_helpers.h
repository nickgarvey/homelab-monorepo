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
int  ds18b20_mock_get_bus_init_count(void);
void ds18b20_mock_reset(void);

/* i2c_mocks.c */
void    i2c_mock_set_register(uint16_t dev_addr, uint8_t reg, uint8_t value);
uint8_t i2c_mock_get_register(uint16_t dev_addr, uint8_t reg);
void    i2c_mock_set_bus_init_result(esp_err_t result);
void    i2c_mock_set_transmit_receive_result(esp_err_t result);
void    i2c_mock_set_transmit_result(esp_err_t result);
uint8_t i2c_mock_last_write_reg(void);
uint8_t i2c_mock_last_write_val(void);
int     i2c_mock_get_write_count(void);
int     i2c_mock_get_bus_init_count(void);
void    i2c_mock_reset(void);

/* system_mocks.c */
extern int g_deep_sleep_count;
extern int g_vTaskDelay_count;
extern int g_vTaskDelay_max;
extern esp_sleep_source_t g_mock_wakeup_cause;
void system_mock_reset(void);
