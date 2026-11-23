#include "sht40.h"

#define CMD_MEASURE_HIGH_PRECISION 0xFD

static sht40_bus_t s_bus;
static bool s_ready;

void sht40_init(const sht40_bus_t *bus)
{
    s_bus = *bus;
    s_ready = true;
}

uint8_t sht40_crc8(uint8_t msb, uint8_t lsb)
{
    /* CRC-8, polynomial 0x31, init 0xFF (SHT4x datasheet section 4.4) */
    uint8_t crc = 0xFF;
    uint8_t data[2] = { msb, lsb };
    for (int i = 0; i < 2; i++) {
        crc ^= data[i];
        for (int b = 0; b < 8; b++) {
            crc = (crc & 0x80) ? (uint8_t)((crc << 1) ^ 0x31) : (uint8_t)(crc << 1);
        }
    }
    return crc;
}

bool sht40_parse(const uint8_t raw[6], sht40_reading_t *out)
{
    if (sht40_crc8(raw[0], raw[1]) != raw[2] || sht40_crc8(raw[3], raw[4]) != raw[5]) {
        return false;
    }
    uint16_t t = (uint16_t)((raw[0] << 8) | raw[1]);
    uint16_t h = (uint16_t)((raw[3] << 8) | raw[4]);
    out->temperature_c = -45.0f + 175.0f * (float)t / 65535.0f;
    float rh = -6.0f + 125.0f * (float)h / 65535.0f;
    if (rh < 0.0f) rh = 0.0f;
    if (rh > 100.0f) rh = 100.0f;
    out->humidity_pct = rh;
    return true;
}

bool sht40_read(sht40_reading_t *out)
{
    if (!s_ready) return false;
    uint8_t cmd = CMD_MEASURE_HIGH_PRECISION;
    if (!s_bus.transmit(s_bus.ctx, &cmd, 1)) return false;
    s_bus.delay_ms(10);
    uint8_t raw[6];
    if (!s_bus.receive(s_bus.ctx, raw, sizeof(raw))) return false;
    return sht40_parse(raw, out);
}
