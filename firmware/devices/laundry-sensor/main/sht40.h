/*
 * Sensirion SHT40 temperature and humidity sensor (I2C address 0x44).
 * The driver only formats the traffic; the caller supplies the bus.
 */
#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct {
    bool (*transmit)(void *ctx, const uint8_t *buf, size_t len);
    bool (*receive)(void *ctx, uint8_t *buf, size_t len);
    void (*delay_ms)(uint32_t ms);
    void *ctx;
} sht40_bus_t;

typedef struct {
    float temperature_c;
    float humidity_pct;
} sht40_reading_t;

void sht40_init(const sht40_bus_t *bus);

/* High-precision measurement (about 9 ms). */
bool sht40_read(sht40_reading_t *out);

/* Check both CRCs and convert a 6-byte reply. */
bool sht40_parse(const uint8_t raw[6], sht40_reading_t *out);

uint8_t sht40_crc8(uint8_t msb, uint8_t lsb);

#ifdef __cplusplus
}
#endif
