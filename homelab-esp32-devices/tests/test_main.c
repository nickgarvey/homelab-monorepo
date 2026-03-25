#include "unity.h"
#include "esp_idf_mocks.h"
#include "test_helpers.h"
#include "openthread_mocks.h"
#include "openthread_manager.h"
#include <setjmp.h>

/* app_main is defined in devices/freezer-temp-sensor/main/main.c */
void app_main(void);

/* longjmp buffer from system_mocks.c — vTaskDelay jumps here after N calls */
extern jmp_buf g_test_jmp_buf;

static void reset_all(void)
{
    system_mock_reset();
    ds18b20_mock_reset();
    ot_mock_reset();
    ot_mock_set_join_immediately(true);
    http_mock_reset();
    /*
     * vTaskDelay is called twice per successful iteration:
     *   1. Inside read_temperature() — 800ms DS18B20 conversion wait
     *   2. At the end of the main loop — REPORT_INTERVAL_MS
     * For failed sensor reads, only the loop delay (#2) fires.
     * Default to 2 (happy path); tests with sensor failure override to 1.
     */
    g_vTaskDelay_max = 2;
}

/*
 * Helper: call app_main() but longjmp back after one loop iteration.
 * The main loop calls vTaskDelay at the end of each iteration; the mock
 * longjmps back here after g_vTaskDelay_max calls.
 */
static void run_one_iteration(void)
{
    if (setjmp(g_test_jmp_buf) == 0) {
        app_main();
        /* If we get here, app_main returned (shouldn't happen) */
    }
    /* Landed here via longjmp from vTaskDelay mock */
}

/* ---- test 1: temperature read failure → no HA call ---------------------- */

void test_temp_fail_no_ha_call(void)
{
    reset_all();
    g_vTaskDelay_max = 1;  /* sensor fails early, only loop delay fires */
    ds18b20_mock_set_bus_init_result(ESP_FAIL);

    run_one_iteration();

    TEST_ASSERT_EQUAL_STRING("", http_mock_last_url());
}

/* ---- test 2: Thread join failure → still runs loop ---------------------- */

void test_ot_join_fail_still_loops(void)
{
    reset_all();
    ot_mock_set_join_immediately(false);

    run_one_iteration();

    /* Should have completed a loop iteration even without Thread.
     * 2 delays: one in read_temperature (conversion wait), one at loop end. */
    TEST_ASSERT_EQUAL_INT(2, g_vTaskDelay_count);
}

/* ---- test 3: happy path → posts to HA ----------------------------------- */

void test_happy_path_posts_to_ha(void)
{
    reset_all();
    ds18b20_mock_set_temperature(21.5f);

    run_one_iteration();

    TEST_ASSERT_NOT_NULL(strstr(http_mock_last_url(),
                                "/api/states/sensor.freezer_temperature"));
}

/* ---- test 4: temperature value appears in the HA payload ---------------- */

void test_happy_path_payload_contains_temperature(void)
{
    reset_all();
    ds18b20_mock_set_temperature(-18.75f);

    run_one_iteration();

    TEST_ASSERT_NOT_NULL(strstr(http_mock_last_body(), "-18.75"));
}

/* ---- test 5: HA POST failure → loop continues --------------------------- */

void test_ha_post_fail_continues_loop(void)
{
    reset_all();
    http_mock_set_perform_result(ESP_FAIL);

    run_one_iteration();

    /* Loop should still complete the iteration.
     * 2 delays: sensor conversion wait + loop end. */
    TEST_ASSERT_EQUAL_INT(2, g_vTaskDelay_count);
}

/* ---- test suite entry point --------------------------------------------- */

void run_main_tests(void)
{
    RUN_TEST(test_temp_fail_no_ha_call);
    RUN_TEST(test_ot_join_fail_still_loops);
    RUN_TEST(test_happy_path_posts_to_ha);
    RUN_TEST(test_happy_path_payload_contains_temperature);
    RUN_TEST(test_ha_post_fail_continues_loop);
}
