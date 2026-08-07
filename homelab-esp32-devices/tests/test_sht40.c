#include "unity.h"
#include "esp_idf_mocks.h"
#include "test_helpers.h"
#include "sht40.h"

#define SHT40_ADDR  0x44

/* SHT40 CRC-8: polynomial 0x31, init 0xFF */
static uint8_t sht40_crc(uint8_t msb, uint8_t lsb)
{
    uint8_t crc = 0xFF;
    uint8_t data[2] = { msb, lsb };
    for (int i = 0; i < 2; i++) {
        crc ^= data[i];
        for (int b = 0; b < 8; b++) {
            if (crc & 0x80)
                crc = (crc << 1) ^ 0x31;
            else
                crc <<= 1;
        }
    }
    return crc;
}

static void setup(void)
{
    i2c_mock_reset();
    sht40_reset();
}

/* Helper: set mock response for SHT40 read (6 bytes at registers 0-5) */
static void mock_sht40_response(uint16_t raw_temp, uint16_t raw_hum)
{
    uint8_t t_msb = raw_temp >> 8;
    uint8_t t_lsb = raw_temp & 0xFF;
    uint8_t h_msb = raw_hum >> 8;
    uint8_t h_lsb = raw_hum & 0xFF;

    i2c_mock_set_register(SHT40_ADDR, 0, t_msb);
    i2c_mock_set_register(SHT40_ADDR, 1, t_lsb);
    i2c_mock_set_register(SHT40_ADDR, 2, sht40_crc(t_msb, t_lsb));
    i2c_mock_set_register(SHT40_ADDR, 3, h_msb);
    i2c_mock_set_register(SHT40_ADDR, 4, h_lsb);
    i2c_mock_set_register(SHT40_ADDR, 5, sht40_crc(h_msb, h_lsb));
}

/* ---- Init tests -------------------------------------------------------- */

static void test_sht40_init_success(void)
{
    setup();
    sht40_config_t cfg = { .sda_gpio = 19, .scl_gpio = 18 };
    TEST_ASSERT_TRUE(sht40_init(&cfg));
}

static void test_sht40_init_with_shared_bus(void)
{
    setup();
    /* Simulate passing an existing bus handle */
    sht40_config_t cfg = { .sda_gpio = 19, .scl_gpio = 18, .i2c_bus = (void *)1 };
    TEST_ASSERT_TRUE(sht40_init(&cfg));
    /* Should NOT create a new bus */
    TEST_ASSERT_EQUAL_INT(0, i2c_mock_get_bus_init_count());
}

static void test_sht40_init_fails_i2c_error(void)
{
    setup();
    i2c_mock_set_bus_init_result(ESP_FAIL);
    sht40_config_t cfg = { .sda_gpio = 19, .scl_gpio = 18 };
    TEST_ASSERT_FALSE(sht40_init(&cfg));
}

/* ---- Read tests -------------------------------------------------------- */

static void test_sht40_read_25c_50pct(void)
{
    setup();
    sht40_config_t cfg = { .sda_gpio = 19, .scl_gpio = 18 };
    sht40_init(&cfg);

    /* Raw temp for 25°C: T = -45 + 175 * raw / 65535
     * 25 = -45 + 175 * raw / 65535  →  raw = 70 * 65535 / 175 = 26214
     * Raw hum for 50%: RH = -6 + 125 * raw / 65535
     * 50 = -6 + 125 * raw / 65535  →  raw = 56 * 65535 / 125 = 29360 */
    mock_sht40_response(26214, 29360);

    sht40_reading_t reading;
    TEST_ASSERT_TRUE(sht40_read(&reading));
    TEST_ASSERT_FLOAT_WITHIN(0.5f, 25.0f, reading.temperature_c);
    TEST_ASSERT_FLOAT_WITHIN(0.5f, 50.0f, reading.humidity_pct);
}

static void test_sht40_read_0c_0pct(void)
{
    setup();
    sht40_config_t cfg = { .sda_gpio = 19, .scl_gpio = 18 };
    sht40_init(&cfg);

    /* Raw temp for 0°C: 0 = -45 + 175 * raw / 65535  →  raw = 45 * 65535 / 175 = 16852 */
    /* Raw hum for 0%: 0 = -6 + 125 * raw / 65535  →  raw = 6 * 65535 / 125 = 3145 */
    mock_sht40_response(16852, 3145);

    sht40_reading_t reading;
    TEST_ASSERT_TRUE(sht40_read(&reading));
    TEST_ASSERT_FLOAT_WITHIN(0.5f, 0.0f, reading.temperature_c);
    TEST_ASSERT_FLOAT_WITHIN(0.5f, 0.0f, reading.humidity_pct);
}

static void test_sht40_read_fails_before_init(void)
{
    setup();
    sht40_reading_t reading;
    TEST_ASSERT_FALSE(sht40_read(&reading));
}

static void test_sht40_read_fails_bad_crc(void)
{
    setup();
    sht40_config_t cfg = { .sda_gpio = 19, .scl_gpio = 18 };
    sht40_init(&cfg);

    /* Set response with bad CRC */
    i2c_mock_set_register(SHT40_ADDR, 0, 0x66);
    i2c_mock_set_register(SHT40_ADDR, 1, 0x66);
    i2c_mock_set_register(SHT40_ADDR, 2, 0xFF); /* wrong CRC */
    i2c_mock_set_register(SHT40_ADDR, 3, 0x00);
    i2c_mock_set_register(SHT40_ADDR, 4, 0x00);
    i2c_mock_set_register(SHT40_ADDR, 5, 0x00);

    sht40_reading_t reading;
    TEST_ASSERT_FALSE(sht40_read(&reading));
}

static void test_sht40_humidity_clamped(void)
{
    setup();
    sht40_config_t cfg = { .sda_gpio = 19, .scl_gpio = 18 };
    sht40_init(&cfg);

    /* Raw hum 0xFFFF → RH = -6 + 125 * 65535/65535 = 119% → should clamp to 100 */
    mock_sht40_response(26214, 0xFFFF);

    sht40_reading_t reading;
    TEST_ASSERT_TRUE(sht40_read(&reading));
    TEST_ASSERT_FLOAT_WITHIN(0.1f, 100.0f, reading.humidity_pct);
}

/* ---- Reset test -------------------------------------------------------- */

static void test_sht40_reset_clears_state(void)
{
    setup();
    sht40_config_t cfg = { .sda_gpio = 19, .scl_gpio = 18 };
    sht40_init(&cfg);
    sht40_reset();

    sht40_reading_t reading;
    TEST_ASSERT_FALSE(sht40_read(&reading));

    /* Re-init should work */
    TEST_ASSERT_TRUE(sht40_init(&cfg));
}

/* ---- Suite entry point ------------------------------------------------- */

void run_sht40_tests(void)
{
    RUN_TEST(test_sht40_init_success);
    RUN_TEST(test_sht40_init_with_shared_bus);
    RUN_TEST(test_sht40_init_fails_i2c_error);
    RUN_TEST(test_sht40_read_25c_50pct);
    RUN_TEST(test_sht40_read_0c_0pct);
    RUN_TEST(test_sht40_read_fails_before_init);
    RUN_TEST(test_sht40_read_fails_bad_crc);
    RUN_TEST(test_sht40_humidity_clamped);
    RUN_TEST(test_sht40_reset_clears_state);
}
