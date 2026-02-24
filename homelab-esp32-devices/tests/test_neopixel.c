#include "unity.h"
#include "esp_idf_mocks.h"
#include "neopixel.h"
#include "test_helpers.h"

void test_neopixel_set_stores_color(void)
{
    neopixel_init(33, 21);
    neopixel_set(128, 0, 0);

    uint8_t r, g, b;
    neopixel_mock_get_color(&r, &g, &b);

    TEST_ASSERT_EQUAL_UINT8(128, r);
    TEST_ASSERT_EQUAL_UINT8(0,   g);
    TEST_ASSERT_EQUAL_UINT8(0,   b);
}

void test_neopixel_set_green(void)
{
    neopixel_init(33, 21);
    neopixel_set(0, 128, 0);

    uint8_t r, g, b;
    neopixel_mock_get_color(&r, &g, &b);

    TEST_ASSERT_EQUAL_UINT8(0,   r);
    TEST_ASSERT_EQUAL_UINT8(128, g);
    TEST_ASSERT_EQUAL_UINT8(0,   b);
}

void test_neopixel_set_updates_on_each_call(void)
{
    neopixel_init(33, 21);

    neopixel_set(255, 0, 0);
    neopixel_set(0, 0, 255);

    uint8_t r, g, b;
    neopixel_mock_get_color(&r, &g, &b);

    TEST_ASSERT_EQUAL_UINT8(0,   r);
    TEST_ASSERT_EQUAL_UINT8(0,   g);
    TEST_ASSERT_EQUAL_UINT8(255, b);
}

void test_neopixel_init_no_power_pin(void)
{
    /* -1 for power_pin should not crash */
    neopixel_init(33, -1);
    neopixel_set(10, 20, 30);

    uint8_t r, g, b;
    neopixel_mock_get_color(&r, &g, &b);
    TEST_ASSERT_EQUAL_UINT8(10, r);
    TEST_ASSERT_EQUAL_UINT8(20, g);
    TEST_ASSERT_EQUAL_UINT8(30, b);
}

void run_neopixel_tests(void)
{
    RUN_TEST(test_neopixel_set_stores_color);
    RUN_TEST(test_neopixel_set_green);
    RUN_TEST(test_neopixel_set_updates_on_each_call);
    RUN_TEST(test_neopixel_init_no_power_pin);
}
