#include "lis2dw12.h"

#include <string.h>

/* Registers (DS11811 section 8) */
#define REG_WHO_AM_I     0x0F
#define REG_CTRL1        0x20
#define REG_CTRL2        0x21
#define REG_CTRL3        0x22
#define REG_CTRL4_INT1   0x23
#define REG_CTRL5_INT2   0x24
#define REG_CTRL6        0x25
#define REG_OUT_X_L      0x28
#define REG_FIFO_CTRL    0x2E
#define REG_FIFO_SAMPLES 0x2F
#define REG_WAKE_UP_THS  0x34
#define REG_WAKE_UP_DUR  0x35
#define REG_ALL_INT_SRC  0x3B
#define REG_CTRL7        0x3F

/* CTRL1: ODR[7:4], MODE[3:2] (00 = low-power), LP_MODE[1:0] (00 = low-power mode 1) */
#define CTRL1_ODR_12_5HZ_LP1 0x20
#define CTRL1_ODR_50HZ_LP1   0x40
/* CTRL2 */
#define CTRL2_SOFT_RESET     0x40
#define CTRL2_BDU            0x08
#define CTRL2_IF_ADD_INC     0x04
#define CTRL2_I2C_DISABLE    0x02
/* CTRL3 */
#define CTRL3_LIR            0x10
/* CTRL4_INT1 / CTRL5_INT2 */
#define CTRL4_INT1_WU        0x20
#define CTRL5_INT2_FTH       0x02
/* CTRL7 */
#define CTRL7_INTERRUPTS_ENABLE 0x20
/* FIFO_CTRL: FMODE[7:5] */
#define FIFO_MODE_BYPASS     0x00
#define FIFO_MODE_CONTINUOUS 0xC0

static lis2dw12_bus_t s_bus;
static bool s_ready;

static bool wr(uint8_t reg, uint8_t v)
{
    return s_bus.write(s_bus.ctx, reg, v);
}

static bool rd(uint8_t reg, uint8_t *buf, size_t len)
{
    return s_bus.read(s_bus.ctx, reg, buf, len);
}

bool lis2dw12_init(const lis2dw12_bus_t *bus)
{
    s_bus = *bus;
    s_ready = false;

    uint8_t id = 0;
    if (!rd(REG_WHO_AM_I, &id, 1) || id != LIS2DW12_WHO_AM_I_VALUE) {
        return false;
    }
    if (!wr(REG_CTRL2, CTRL2_SOFT_RESET)) {
        return false;
    }
    s_bus.delay_ms(5);
    if (!wr(REG_CTRL2, CTRL2_BDU | CTRL2_IF_ADD_INC | CTRL2_I2C_DISABLE)) {
        return false;
    }
    s_ready = true;
    return true;
}

uint8_t lis2dw12_wake_threshold_lsb(uint16_t threshold_mg)
{
    /* 1 LSB = full scale / 64 = 31.25 mg at +-2 g. Work in 1/4 mg to stay in integers. */
    uint32_t lsb = ((uint32_t)threshold_mg * 4 + 124) / 125;
    if (lsb < 1) lsb = 1;
    if (lsb > 63) lsb = 63;
    return (uint8_t)lsb;
}

bool lis2dw12_arm_wakeup(uint16_t threshold_mg)
{
    if (!s_ready) return false;
    return wr(REG_CTRL1, 0x00)                          /* power down while reconfiguring */
        && wr(REG_FIFO_CTRL, FIFO_MODE_BYPASS)
        && wr(REG_CTRL5_INT2, 0x00)
        && wr(REG_CTRL6, 0x00)                          /* +-2 g, low noise off */
        && wr(REG_CTRL3, CTRL3_LIR)                     /* latched, push-pull, active high */
        && wr(REG_WAKE_UP_THS, lis2dw12_wake_threshold_lsb(threshold_mg))
        && wr(REG_WAKE_UP_DUR, 0x00)
        && wr(REG_CTRL4_INT1, CTRL4_INT1_WU)
        && wr(REG_CTRL7, CTRL7_INTERRUPTS_ENABLE)
        && lis2dw12_clear_interrupts()
        && wr(REG_CTRL1, CTRL1_ODR_12_5HZ_LP1);
}

bool lis2dw12_start_stream(uint8_t fifo_threshold)
{
    if (!s_ready) return false;
    if (fifo_threshold < 1) fifo_threshold = 1;
    if (fifo_threshold > LIS2DW12_FIFO_DEPTH - 1) fifo_threshold = LIS2DW12_FIFO_DEPTH - 1;
    return wr(REG_CTRL1, 0x00)
        && wr(REG_CTRL4_INT1, 0x00)
        && wr(REG_CTRL3, 0x00)                          /* FIFO threshold is a level, not latched */
        && lis2dw12_clear_interrupts()
        && wr(REG_FIFO_CTRL, FIFO_MODE_BYPASS)          /* bypass empties the FIFO */
        && wr(REG_FIFO_CTRL, FIFO_MODE_CONTINUOUS | fifo_threshold)
        && wr(REG_CTRL5_INT2, CTRL5_INT2_FTH)
        && wr(REG_CTRL1, CTRL1_ODR_50HZ_LP1);
}

lis2dw12_sample_t lis2dw12_convert(const uint8_t raw[6])
{
    /* Left-justified 16-bit: 2 g / 32768 = 0.061 mg per LSB whatever the resolution. */
    lis2dw12_sample_t s;
    int16_t x = (int16_t)(raw[0] | (raw[1] << 8));
    int16_t y = (int16_t)(raw[2] | (raw[3] << 8));
    int16_t z = (int16_t)(raw[4] | (raw[5] << 8));
    s.x_mg = (int16_t)((int32_t)x * 61 / 1000);
    s.y_mg = (int16_t)((int32_t)y * 61 / 1000);
    s.z_mg = (int16_t)((int32_t)z * 61 / 1000);
    return s;
}

int lis2dw12_read_fifo(lis2dw12_sample_t *out, int max)
{
    if (!s_ready) return -1;
    uint8_t status = 0;
    if (!rd(REG_FIFO_SAMPLES, &status, 1)) return -1;
    int n = status & 0x3F;
    if (n > max) n = max;
    for (int i = 0; i < n; i++) {
        uint8_t raw[6];
        if (!rd(REG_OUT_X_L, raw, sizeof(raw))) return -1;
        out[i] = lis2dw12_convert(raw);
    }
    return n;
}

bool lis2dw12_clear_interrupts(void)
{
    uint8_t src = 0;
    return rd(REG_ALL_INT_SRC, &src, 1);
}
