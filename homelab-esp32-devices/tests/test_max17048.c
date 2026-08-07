#include "unity.h"
#include "esp_idf_mocks.h"
#include "test_helpers.h"
#include "max17048.h"

#define MAX17048_ADDR    0x36
#define REG_VCELL        0x02
#define REG_SOC          0x04

static void setup(void)
{
    i2c_mock_reset();
    max17048_reset();
}

/* ---- Init tests -------------------------------------------------------- */

static void test_max17048_init_success(void)
{
    setup();
    max17048_config_t cfg = { .sda_gpio = 19, .scl_gpio = 18 };
    TEST_ASSERT_TRUE(max17048_init(&cfg));
}

static void test_max17048_init_fails_i2c_error(void)
{
    setup();
    i2c_mock_set_bus_init_result(ESP_FAIL);
    max17048_config_t cfg = { .sda_gpio = 19, .scl_gpio = 18 };
    TEST_ASSERT_FALSE(max17048_init(&cfg));
}

/* ---- Voltage tests ----------------------------------------------------- */

static void test_max17048_read_voltage(void)
{
    setup();
    max17048_config_t cfg = { .sda_gpio = 19, .scl_gpio = 18 };
    max17048_init(&cfg);

    /* VCELL register: 16-bit big-endian.
     * Value = raw * 78.125 uV.
     * Example: 0xD000 = 53248 → 53248 * 78.125e-6 = 4.16V */
    i2c_mock_set_register(MAX17048_ADDR, REG_VCELL, 0xD0);     /* high byte */
    i2c_mock_set_register(MAX17048_ADDR, REG_VCELL + 1, 0x00); /* low byte */

    float volts;
    TEST_ASSERT_TRUE(max17048_read_voltage(&volts));
    TEST_ASSERT_FLOAT_WITHIN(0.01f, 4.16f, volts);
}

/* ---- SOC tests --------------------------------------------------------- */

static void test_max17048_read_soc(void)
{
    setup();
    max17048_config_t cfg = { .sda_gpio = 19, .scl_gpio = 18 };
    max17048_init(&cfg);

    /* SOC register: high byte = integer %, low byte = 1/256 fraction */
    i2c_mock_set_register(MAX17048_ADDR, REG_SOC, 75);       /* 75% */
    i2c_mock_set_register(MAX17048_ADDR, REG_SOC + 1, 0x80); /* 0.5 fraction */

    uint8_t pct;
    TEST_ASSERT_TRUE(max17048_read_soc(&pct));
    TEST_ASSERT_EQUAL_UINT8(75, pct);
}

static void test_max17048_soc_clamped_to_100(void)
{
    setup();
    max17048_config_t cfg = { .sda_gpio = 19, .scl_gpio = 18 };
    max17048_init(&cfg);

    i2c_mock_set_register(MAX17048_ADDR, REG_SOC, 110);
    i2c_mock_set_register(MAX17048_ADDR, REG_SOC + 1, 0);

    uint8_t pct;
    TEST_ASSERT_TRUE(max17048_read_soc(&pct));
    TEST_ASSERT_EQUAL_UINT8(100, pct);
}

static void test_max17048_read_fails_before_init(void)
{
    setup();
    float volts;
    uint8_t pct;
    TEST_ASSERT_FALSE(max17048_read_voltage(&volts));
    TEST_ASSERT_FALSE(max17048_read_soc(&pct));
}

/* ---- Reset test -------------------------------------------------------- */

static void test_max17048_reset_clears_state(void)
{
    setup();
    max17048_config_t cfg = { .sda_gpio = 19, .scl_gpio = 18 };
    max17048_init(&cfg);
    max17048_reset();

    uint8_t pct;
    TEST_ASSERT_FALSE(max17048_read_soc(&pct));

    /* Re-init should work */
    TEST_ASSERT_TRUE(max17048_init(&cfg));
}

/* ---- Suite entry point ------------------------------------------------- */

void run_max17048_tests(void)
{
    RUN_TEST(test_max17048_init_success);
    RUN_TEST(test_max17048_init_fails_i2c_error);
    RUN_TEST(test_max17048_read_voltage);
    RUN_TEST(test_max17048_read_soc);
    RUN_TEST(test_max17048_soc_clamped_to_100);
    RUN_TEST(test_max17048_read_fails_before_init);
    RUN_TEST(test_max17048_reset_clears_state);
}
