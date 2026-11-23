#include <string.h>

#include "unity.h"
#include "lis2dw12.h"

/* A register file standing in for the chip, with a log of every write. */
static uint8_t s_regs[256];
static struct { uint8_t reg, val; } s_writes[64];
static int s_nwrites;
static bool s_fail;
/* FIFO contents served at OUT_X_L, one 6-byte sample per read */
static uint8_t s_fifo[32][6];
static int s_fifo_len, s_fifo_pos;

static bool fake_read(void *ctx, uint8_t reg, uint8_t *buf, size_t len)
{
    (void)ctx;
    if (s_fail) return false;
    if (reg == 0x28 && s_fifo_pos < s_fifo_len) {
        memcpy(buf, s_fifo[s_fifo_pos++], len);
        return true;
    }
    memcpy(buf, &s_regs[reg], len);
    return true;
}

static bool fake_write(void *ctx, uint8_t reg, uint8_t value)
{
    (void)ctx;
    if (s_fail) return false;
    s_regs[reg] = value;
    if (s_nwrites < 64) {
        s_writes[s_nwrites].reg = reg;
        s_writes[s_nwrites].val = value;
        s_nwrites++;
    }
    return true;
}

static void fake_delay(uint32_t ms) { (void)ms; }

static const lis2dw12_bus_t BUS = { fake_read, fake_write, fake_delay, NULL };

static void reset_chip(void)
{
    memset(s_regs, 0, sizeof(s_regs));
    s_regs[0x0F] = LIS2DW12_WHO_AM_I_VALUE;
    s_nwrites = 0;
    s_fail = false;
    s_fifo_len = s_fifo_pos = 0;
}

static int index_of_write(uint8_t reg, uint8_t val)
{
    for (int i = 0; i < s_nwrites; i++) {
        if (s_writes[i].reg == reg && s_writes[i].val == val) return i;
    }
    return -1;
}

static void test_lis2dw12_init_rejects_wrong_id(void)
{
    reset_chip();
    s_regs[0x0F] = 0x33;
    TEST_ASSERT_FALSE(lis2dw12_init(&BUS));
}

static void test_lis2dw12_init_resets_then_disables_i2c(void)
{
    reset_chip();
    TEST_ASSERT_TRUE(lis2dw12_init(&BUS));
    int reset = index_of_write(0x21, 0x40);
    int ctrl2 = index_of_write(0x21, 0x0E);  /* BDU | IF_ADD_INC | I2C_DISABLE */
    TEST_ASSERT_TRUE(reset >= 0);
    TEST_ASSERT_TRUE(ctrl2 > reset);
}

static void test_lis2dw12_init_fails_on_bus_error(void)
{
    reset_chip();
    s_fail = true;
    TEST_ASSERT_FALSE(lis2dw12_init(&BUS));
}

static void test_lis2dw12_wake_threshold_rounds_up(void)
{
    TEST_ASSERT_EQUAL_UINT8(3, lis2dw12_wake_threshold_lsb(90));   /* 93.75 mg */
    TEST_ASSERT_EQUAL_UINT8(1, lis2dw12_wake_threshold_lsb(0));
    TEST_ASSERT_EQUAL_UINT8(1, lis2dw12_wake_threshold_lsb(31));
    TEST_ASSERT_EQUAL_UINT8(2, lis2dw12_wake_threshold_lsb(32));
    TEST_ASSERT_EQUAL_UINT8(63, lis2dw12_wake_threshold_lsb(5000));
}

static void test_lis2dw12_arm_wakeup_registers(void)
{
    reset_chip();
    lis2dw12_init(&BUS);
    s_nwrites = 0;
    TEST_ASSERT_TRUE(lis2dw12_arm_wakeup(90));
    TEST_ASSERT_EQUAL_HEX8(0x03, s_regs[0x34]);   /* WAKE_UP_THS */
    TEST_ASSERT_EQUAL_HEX8(0x20, s_regs[0x23]);   /* INT1_WU */
    TEST_ASSERT_EQUAL_HEX8(0x00, s_regs[0x24]);   /* nothing on INT2 */
    TEST_ASSERT_EQUAL_HEX8(0x10, s_regs[0x22]);   /* latched, push-pull, active high */
    TEST_ASSERT_EQUAL_HEX8(0x20, s_regs[0x3F]);   /* INTERRUPTS_ENABLE */
    TEST_ASSERT_EQUAL_HEX8(0x00, s_regs[0x2E]);   /* FIFO bypass */
    /* 12.5 Hz low-power mode 1, written last so the chip starts fully configured */
    TEST_ASSERT_EQUAL_HEX8(0x20, s_regs[0x20]);
    TEST_ASSERT_EQUAL_HEX8(0x20, s_writes[s_nwrites - 1].reg);
}

static void test_lis2dw12_start_stream_registers(void)
{
    reset_chip();
    lis2dw12_init(&BUS);
    s_nwrites = 0;
    TEST_ASSERT_TRUE(lis2dw12_start_stream(25));
    TEST_ASSERT_EQUAL_HEX8(0x00, s_regs[0x23]);   /* wake-up off INT1 */
    TEST_ASSERT_EQUAL_HEX8(0x02, s_regs[0x24]);   /* FIFO threshold on INT2 */
    TEST_ASSERT_EQUAL_HEX8(0xC0 | 25, s_regs[0x2E]);
    TEST_ASSERT_TRUE(index_of_write(0x2E, 0x00) >= 0);  /* emptied through bypass first */
    TEST_ASSERT_EQUAL_HEX8(0x40, s_regs[0x20]);   /* 50 Hz low-power mode 1 */
    TEST_ASSERT_EQUAL_HEX8(0x20, s_writes[s_nwrites - 1].reg);
}

static void test_lis2dw12_start_stream_clamps_threshold(void)
{
    reset_chip();
    lis2dw12_init(&BUS);
    lis2dw12_start_stream(40);
    TEST_ASSERT_EQUAL_HEX8(0xC0 | 31, s_regs[0x2E]);
}

static void test_lis2dw12_convert_scales_to_mg(void)
{
    /* +1 g on z: 16384 LSB at 0.061 mg/LSB = 999 mg; -0.5 g on x */
    uint8_t raw[6] = { 0x00, 0xE0, 0x00, 0x00, 0x00, 0x40 };
    lis2dw12_sample_t s = lis2dw12_convert(raw);
    TEST_ASSERT_INT_WITHIN(2, -500, s.x_mg);
    TEST_ASSERT_EQUAL_INT16(0, s.y_mg);
    TEST_ASSERT_INT_WITHIN(2, 1000, s.z_mg);
}

static void test_lis2dw12_read_fifo_reads_each_sample(void)
{
    reset_chip();
    lis2dw12_init(&BUS);
    s_fifo_len = 3;
    for (int i = 0; i < 3; i++) {
        uint8_t z = (uint8_t)(0x40 + i);
        uint8_t sample[6] = { 0, 0, 0, 0, 0, z };
        memcpy(s_fifo[i], sample, 6);
    }
    s_regs[0x2F] = 0x80 | 3;  /* FIFO_FTH flag plus 3 samples */
    lis2dw12_sample_t out[8];
    TEST_ASSERT_EQUAL_INT(3, lis2dw12_read_fifo(out, 8));
    TEST_ASSERT_TRUE(out[2].z_mg > out[0].z_mg);
}

static void test_lis2dw12_read_fifo_limits_to_buffer(void)
{
    reset_chip();
    lis2dw12_init(&BUS);
    s_regs[0x2F] = 20;
    lis2dw12_sample_t out[4];
    TEST_ASSERT_EQUAL_INT(4, lis2dw12_read_fifo(out, 4));
}

void run_lis2dw12_tests(void)
{
    RUN_TEST(test_lis2dw12_init_rejects_wrong_id);
    RUN_TEST(test_lis2dw12_init_resets_then_disables_i2c);
    RUN_TEST(test_lis2dw12_init_fails_on_bus_error);
    RUN_TEST(test_lis2dw12_wake_threshold_rounds_up);
    RUN_TEST(test_lis2dw12_arm_wakeup_registers);
    RUN_TEST(test_lis2dw12_start_stream_registers);
    RUN_TEST(test_lis2dw12_start_stream_clamps_threshold);
    RUN_TEST(test_lis2dw12_convert_scales_to_mg);
    RUN_TEST(test_lis2dw12_read_fifo_reads_each_sample);
    RUN_TEST(test_lis2dw12_read_fifo_limits_to_buffer);
}
