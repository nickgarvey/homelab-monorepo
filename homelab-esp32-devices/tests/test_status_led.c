#include "unity.h"
#include "esp_idf_mocks.h"
#include "test_helpers.h"
#include "status_led.h"

#define LED_DIM 24

static void setup(void)
{
    status_led_init(9, 20);
}

static void test_status_led_startup_is_white(void)
{
    setup();
    status_led_set(STATUS_STARTUP);
    uint8_t r, g, b;
    neopixel_mock_get_color(&r, &g, &b);
    TEST_ASSERT_EQUAL_UINT8(LED_DIM, r);
    TEST_ASSERT_EQUAL_UINT8(LED_DIM, g);
    TEST_ASSERT_EQUAL_UINT8(LED_DIM, b);
}

static void test_status_led_connected_is_green(void)
{
    setup();
    status_led_set(STATUS_CONNECTED);
    uint8_t r, g, b;
    neopixel_mock_get_color(&r, &g, &b);
    TEST_ASSERT_EQUAL_UINT8(0, r);
    TEST_ASSERT_EQUAL_UINT8(LED_DIM, g);
    TEST_ASSERT_EQUAL_UINT8(0, b);
}

static void test_status_led_battery_low_is_red(void)
{
    setup();
    status_led_set(STATUS_BATTERY_LOW);
    uint8_t r, g, b;
    neopixel_mock_get_color(&r, &g, &b);
    TEST_ASSERT_EQUAL_UINT8(LED_DIM, r);
    TEST_ASSERT_EQUAL_UINT8(0, g);
    TEST_ASSERT_EQUAL_UINT8(0, b);
}

static void test_status_led_active_is_blue(void)
{
    setup();
    status_led_set(STATUS_ACTIVE);
    uint8_t r, g, b;
    neopixel_mock_get_color(&r, &g, &b);
    TEST_ASSERT_EQUAL_UINT8(0, r);
    TEST_ASSERT_EQUAL_UINT8(0, g);
    TEST_ASSERT_EQUAL_UINT8(LED_DIM, b);
}

static void test_status_led_sleeping_is_off(void)
{
    setup();
    status_led_set(STATUS_SLEEPING);
    uint8_t r, g, b;
    neopixel_mock_get_color(&r, &g, &b);
    TEST_ASSERT_EQUAL_UINT8(0, r);
    TEST_ASSERT_EQUAL_UINT8(0, g);
    TEST_ASSERT_EQUAL_UINT8(0, b);
}

static void test_status_led_off_is_off(void)
{
    setup();
    status_led_set(STATUS_OFF);
    uint8_t r, g, b;
    neopixel_mock_get_color(&r, &g, &b);
    TEST_ASSERT_EQUAL_UINT8(0, r);
    TEST_ASSERT_EQUAL_UINT8(0, g);
    TEST_ASSERT_EQUAL_UINT8(0, b);
}

void run_status_led_tests(void)
{
    RUN_TEST(test_status_led_startup_is_white);
    RUN_TEST(test_status_led_connected_is_green);
    RUN_TEST(test_status_led_battery_low_is_red);
    RUN_TEST(test_status_led_active_is_blue);
    RUN_TEST(test_status_led_sleeping_is_off);
    RUN_TEST(test_status_led_off_is_off);
}
