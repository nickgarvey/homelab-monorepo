#include <string.h>

#include "unity.h"
#include "sht40.h"
#include "battery.h"

/* ---- SHT40 ------------------------------------------------------------- */

static uint8_t s_sent[4];
static size_t s_sent_len;
static uint8_t s_reply[6];
static bool s_bus_ok;

static bool fake_transmit(void *ctx, const uint8_t *buf, size_t len)
{
    (void)ctx;
    memcpy(s_sent, buf, len);
    s_sent_len = len;
    return s_bus_ok;
}

static bool fake_receive(void *ctx, uint8_t *buf, size_t len)
{
    (void)ctx;
    memcpy(buf, s_reply, len);
    return s_bus_ok;
}

static void fake_delay(uint32_t ms) { (void)ms; }

static void set_reply(uint16_t t, uint16_t h)
{
    s_reply[0] = t >> 8; s_reply[1] = t & 0xFF; s_reply[2] = sht40_crc8(s_reply[0], s_reply[1]);
    s_reply[3] = h >> 8; s_reply[4] = h & 0xFF; s_reply[5] = sht40_crc8(s_reply[3], s_reply[4]);
}

static void test_sht40_crc_matches_datasheet_example(void)
{
    TEST_ASSERT_EQUAL_HEX8(0x92, sht40_crc8(0xBE, 0xEF));  /* SHT4x datasheet: 0xBEEF -> 0x92 */
}

static void test_sht40_read_sends_high_precision_command(void)
{
    sht40_bus_t bus = { fake_transmit, fake_receive, fake_delay, NULL };
    sht40_init(&bus);
    s_bus_ok = true;
    set_reply(0x6666, 0x8000);
    sht40_reading_t r;
    TEST_ASSERT_TRUE(sht40_read(&r));
    TEST_ASSERT_EQUAL_size_t(1, s_sent_len);
    TEST_ASSERT_EQUAL_HEX8(0xFD, s_sent[0]);
    TEST_ASSERT_FLOAT_WITHIN(0.05f, 25.0f, r.temperature_c);
    TEST_ASSERT_FLOAT_WITHIN(0.05f, 56.5f, r.humidity_pct);
}

static void test_sht40_rejects_bad_crc(void)
{
    set_reply(0x6666, 0x8000);
    s_reply[5] ^= 0x01;
    sht40_reading_t r;
    TEST_ASSERT_FALSE(sht40_parse(s_reply, &r));
}

static void test_sht40_clamps_humidity(void)
{
    sht40_reading_t r;
    set_reply(0x6666, 0x0000);
    TEST_ASSERT_TRUE(sht40_parse(s_reply, &r));
    TEST_ASSERT_EQUAL_FLOAT(0.0f, r.humidity_pct);
    set_reply(0x6666, 0xFFFF);
    TEST_ASSERT_TRUE(sht40_parse(s_reply, &r));
    TEST_ASSERT_EQUAL_FLOAT(100.0f, r.humidity_pct);
}

static void test_sht40_read_fails_on_bus_error(void)
{
    sht40_bus_t bus = { fake_transmit, fake_receive, fake_delay, NULL };
    sht40_init(&bus);
    s_bus_ok = false;
    sht40_reading_t r;
    TEST_ASSERT_FALSE(sht40_read(&r));
}

/* ---- battery ----------------------------------------------------------- */

static void test_battery_sense_is_half_of_vbat(void)
{
    TEST_ASSERT_EQUAL_UINT32(3000, battery_mv_from_sense_mv(1500));
}

static void test_battery_percent_curve(void)
{
    TEST_ASSERT_EQUAL_UINT8(100, battery_percent(3200));
    TEST_ASSERT_EQUAL_UINT8(100, battery_percent(3100));
    TEST_ASSERT_EQUAL_UINT8(90, battery_percent(2900));
    TEST_ASSERT_EQUAL_UINT8(80, battery_percent(2800));   /* halfway between 90 and 70 */
    TEST_ASSERT_EQUAL_UINT8(45, battery_percent(2500));
    TEST_ASSERT_EQUAL_UINT8(0, battery_percent(2000));
    TEST_ASSERT_EQUAL_UINT8(0, battery_percent(1500));
}

static void test_battery_level_bands(void)
{
    TEST_ASSERT_EQUAL(BATTERY_LEVEL_OK, battery_level(26));
    TEST_ASSERT_EQUAL(BATTERY_LEVEL_WARNING, battery_level(25));
    TEST_ASSERT_EQUAL(BATTERY_LEVEL_WARNING, battery_level(11));
    TEST_ASSERT_EQUAL(BATTERY_LEVEL_CRITICAL, battery_level(10));
}

void run_sht40_battery_tests(void)
{
    RUN_TEST(test_sht40_crc_matches_datasheet_example);
    RUN_TEST(test_sht40_read_sends_high_precision_command);
    RUN_TEST(test_sht40_rejects_bad_crc);
    RUN_TEST(test_sht40_clamps_humidity);
    RUN_TEST(test_sht40_read_fails_on_bus_error);
    RUN_TEST(test_battery_sense_is_half_of_vbat);
    RUN_TEST(test_battery_percent_curve);
    RUN_TEST(test_battery_level_bands);
}
