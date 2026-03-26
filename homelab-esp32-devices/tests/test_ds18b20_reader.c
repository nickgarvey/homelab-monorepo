#include "unity.h"
#include "esp_idf_mocks.h"
#include "ds18b20_reader.h"
#include "test_helpers.h"

/* ---- bus is only initialized once across multiple reads ----------------- */

void test_ds18b20_reader_inits_bus_once(void)
{
    ds18b20_mock_reset();
    ds18b20_reader_reset();
    ds18b20_mock_set_temperature(25.0f);

    float t;
    TEST_ASSERT_TRUE(ds18b20_reader_read(5, &t));
    TEST_ASSERT_TRUE(ds18b20_reader_read(5, &t));
    TEST_ASSERT_TRUE(ds18b20_reader_read(5, &t));

    TEST_ASSERT_EQUAL_INT(1, ds18b20_mock_get_bus_init_count());
}

/* ---- successful read returns correct temperature ----------------------- */

void test_ds18b20_reader_returns_temperature(void)
{
    ds18b20_mock_reset();
    ds18b20_reader_reset();
    ds18b20_mock_set_temperature(-18.5f);

    float t;
    TEST_ASSERT_TRUE(ds18b20_reader_read(5, &t));
    TEST_ASSERT_FLOAT_WITHIN(0.01f, -18.5f, t);
}

/* ---- bus init failure returns false ------------------------------------- */

void test_ds18b20_reader_bus_fail_returns_false(void)
{
    ds18b20_mock_reset();
    ds18b20_reader_reset();
    ds18b20_mock_set_bus_init_result(ESP_FAIL);

    float t;
    TEST_ASSERT_FALSE(ds18b20_reader_read(5, &t));
}

/* ---- reset allows re-initialization ------------------------------------ */

void test_ds18b20_reader_reset_reinits(void)
{
    ds18b20_mock_reset();
    ds18b20_reader_reset();
    ds18b20_mock_set_temperature(10.0f);

    float t;
    TEST_ASSERT_TRUE(ds18b20_reader_read(5, &t));
    TEST_ASSERT_EQUAL_INT(1, ds18b20_mock_get_bus_init_count());

    ds18b20_reader_reset();
    TEST_ASSERT_TRUE(ds18b20_reader_read(5, &t));
    TEST_ASSERT_EQUAL_INT(2, ds18b20_mock_get_bus_init_count());
}

/* ---- suite entry point ------------------------------------------------- */

void run_ds18b20_reader_tests(void)
{
    RUN_TEST(test_ds18b20_reader_inits_bus_once);
    RUN_TEST(test_ds18b20_reader_returns_temperature);
    RUN_TEST(test_ds18b20_reader_bus_fail_returns_false);
    RUN_TEST(test_ds18b20_reader_reset_reinits);
}
