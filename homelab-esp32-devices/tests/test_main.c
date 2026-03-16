#include "unity.h"
#include "esp_idf_mocks.h"
#include "test_helpers.h"
#include "openthread_mocks.h"
#include "openthread_manager.h"

/* app_main is defined in devices/freezer-temp-sensor/main/main.c */
void app_main(void);

static void reset_all(void)
{
    system_mock_reset();
    ds18b20_mock_reset();
    ot_mock_reset();
    ot_mock_set_join_immediately(true);
    http_mock_reset();
}

/* ---- test 1: temperature read failure → sleep, no HA call -------------- */

void test_temp_fail_sleeps_without_ha_call(void)
{
    reset_all();
    ds18b20_mock_set_bus_init_result(ESP_FAIL);

    app_main();

    TEST_ASSERT_EQUAL_INT(1, g_deep_sleep_count);
    TEST_ASSERT_EQUAL_STRING("", http_mock_last_url());
}

/* ---- test 2: Thread join failure → sleep, no HA call ------------------- */

void test_ot_join_fail_sleeps_without_ha_call(void)
{
    reset_all();
    ot_mock_set_join_immediately(false);

    app_main();

    TEST_ASSERT_EQUAL_INT(1, g_deep_sleep_count);
    TEST_ASSERT_EQUAL_STRING("", http_mock_last_url());
}

/* ---- test 3: happy path → posts to HA, calls deinit, sleeps once ------- */

void test_happy_path_posts_to_ha_and_sleeps(void)
{
    reset_all();
    ds18b20_mock_set_temperature(21.5f);

    app_main();

    TEST_ASSERT_EQUAL_INT(1, g_deep_sleep_count);
    TEST_ASSERT_NOT_NULL(strstr(http_mock_last_url(),
                                "/api/states/sensor.freezer_temperature"));
    /* ot_manager_deinit was called → attached state reset */
    TEST_ASSERT_FALSE(ot_manager_is_attached());
}

/* ---- test 4: temperature value appears in the HA payload --------------- */

void test_happy_path_payload_contains_temperature(void)
{
    reset_all();
    ds18b20_mock_set_temperature(-18.75f);

    app_main();

    TEST_ASSERT_NOT_NULL(strstr(http_mock_last_body(), "-18.75"));
}

/* ---- test 5: HA POST failure → deinit and sleep still happen ----------- */

void test_ha_post_fail_still_cleans_up(void)
{
    reset_all();
    http_mock_set_perform_result(ESP_FAIL);

    app_main();

    /* Sleep must still be reached even when HA is unreachable. */
    TEST_ASSERT_EQUAL_INT(1, g_deep_sleep_count);
    /* ot_manager_deinit must still have been called. */
    TEST_ASSERT_FALSE(ot_manager_is_attached());
}

/* ---- test suite entry point -------------------------------------------- */

void run_main_tests(void)
{
    RUN_TEST(test_temp_fail_sleeps_without_ha_call);
    RUN_TEST(test_ot_join_fail_sleeps_without_ha_call);
    RUN_TEST(test_happy_path_posts_to_ha_and_sleeps);
    RUN_TEST(test_happy_path_payload_contains_temperature);
    RUN_TEST(test_ha_post_fail_still_cleans_up);
}
