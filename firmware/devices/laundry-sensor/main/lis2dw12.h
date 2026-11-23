/*
 * ST LIS2DW12 accelerometer over 4-wire SPI (datasheet DS11811 Rev 9).
 *
 * Two operating modes:
 *   - armed:     low-power mode 1 at 12.5 Hz (about 1 uA), wake-up interrupt latched on INT1.
 *   - streaming: low-power mode 1 at 50 Hz, FIFO in continuous mode, FIFO threshold on INT2.
 *
 * The driver only formats register traffic; the caller supplies the bus.
 */
#pragma once

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

#define LIS2DW12_WHO_AM_I_VALUE 0x44
#define LIS2DW12_FIFO_DEPTH     32

typedef struct {
    /* Read len bytes starting at reg (auto-increment). Returns true on success. */
    bool (*read)(void *ctx, uint8_t reg, uint8_t *buf, size_t len);
    /* Write one register. Returns true on success. */
    bool (*write)(void *ctx, uint8_t reg, uint8_t value);
    void (*delay_ms)(uint32_t ms);
    void *ctx;
} lis2dw12_bus_t;

typedef struct {
    int16_t x_mg, y_mg, z_mg;
} lis2dw12_sample_t;

/* Check WHO_AM_I, soft-reset, then disable I2C (CS idles high through the internal pull-up,
 * which would otherwise leave the I2C interface listening on the SPI lines). */
bool lis2dw12_init(const lis2dw12_bus_t *bus);

/* Wake-up threshold register value for a threshold in mg at +-2 g (1 LSB = 31.25 mg),
 * rounded up, clamped to 1..63. */
uint8_t lis2dw12_wake_threshold_lsb(uint16_t threshold_mg);

/* Armed mode: 12.5 Hz low-power, wake-up on INT1 (latched, push-pull, active high). */
bool lis2dw12_arm_wakeup(uint16_t threshold_mg);

/* Streaming mode: 50 Hz low-power, FIFO continuous with the given threshold (1..31) on INT2. */
bool lis2dw12_start_stream(uint8_t fifo_threshold);

/* Read every sample in the FIFO into out (at most max). Returns the count, or -1 on bus error. */
int lis2dw12_read_fifo(lis2dw12_sample_t *out, int max);

/* Read ALL_INT_SRC, which clears a latched wake-up interrupt. */
bool lis2dw12_clear_interrupts(void);

/* Convert one 6-byte OUT_X_L..OUT_Z_H block to mg (+-2 g, left-justified data). */
lis2dw12_sample_t lis2dw12_convert(const uint8_t raw[6]);

#ifdef __cplusplus
}
#endif
