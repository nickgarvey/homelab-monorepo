#include "unity.h"
#include "vibration_monitor.h"

static vibration_monitor_config_t default_cfg = {
    .threshold_mg = 100,
    .quiet_timeout_ms = 5000,     /* 5s for testing */
    .report_interval_ms = 1000,   /* 1s for testing */
    .bucket_threshold = 15,
};

static void setup(void)
{
    vibration_monitor_reset();
    vibration_monitor_init(&default_cfg);
}

/* Helper: simulate one second with vibration above threshold */
static void sim_vibrating_second(void)
{
    vibration_monitor_feed_sample(200);
    vibration_monitor_tick(1000);
}

/* Helper: simulate one second with no vibration */
static void sim_quiet_second(void)
{
    vibration_monitor_feed_sample(10);
    vibration_monitor_tick(1000);
}

/* ---- State tests ------------------------------------------------------- */

static void test_vm_initial_state_is_startup(void)
{
    setup();
    TEST_ASSERT_EQUAL(VM_STATE_STARTUP, vibration_monitor_get_state());
}

static void test_vm_notify_wake_transitions_to_active(void)
{
    setup();
    vibration_monitor_notify_wake();
    TEST_ASSERT_EQUAL(VM_STATE_ACTIVE, vibration_monitor_get_state());
}

/* ---- Bucket counting tests --------------------------------------------- */

static void test_vm_no_vibration_zero_buckets(void)
{
    setup();
    vibration_monitor_notify_wake();
    for (int i = 0; i < 10; i++)
        sim_quiet_second();
    TEST_ASSERT_EQUAL_UINT16(0, vibration_monitor_get_active_buckets());
}

static void test_vm_all_vibrating_counts_all(void)
{
    setup();
    vibration_monitor_notify_wake();
    for (int i = 0; i < 20; i++)
        sim_vibrating_second();
    TEST_ASSERT_EQUAL_UINT16(20, vibration_monitor_get_active_buckets());
}

static void test_vm_mixed_vibration_counts_correctly(void)
{
    setup();
    vibration_monitor_notify_wake();
    /* 10 vibrating, 10 quiet */
    for (int i = 0; i < 10; i++)
        sim_vibrating_second();
    for (int i = 0; i < 10; i++)
        sim_quiet_second();
    TEST_ASSERT_EQUAL_UINT16(10, vibration_monitor_get_active_buckets());
}

static void test_vm_multiple_samples_in_bucket_counts_once(void)
{
    setup();
    vibration_monitor_notify_wake();
    /* Feed many samples within one second — should still be one bucket */
    for (int i = 0; i < 100; i++)
        vibration_monitor_feed_sample(200);
    vibration_monitor_tick(1000);
    TEST_ASSERT_EQUAL_UINT16(1, vibration_monitor_get_active_buckets());
}

static void test_vm_rolling_window_evicts_old_buckets(void)
{
    setup();
    vibration_monitor_notify_wake();
    /* Fill 15 vibrating seconds */
    for (int i = 0; i < 15; i++)
        sim_vibrating_second();
    TEST_ASSERT_EQUAL_UINT16(15, vibration_monitor_get_active_buckets());

    /* Advance 120 quiet seconds — all old vibrating buckets should be evicted */
    for (int i = 0; i < VM_WINDOW_SECONDS; i++)
        sim_quiet_second();
    TEST_ASSERT_EQUAL_UINT16(0, vibration_monitor_get_active_buckets());
}

/* ---- is_running tests -------------------------------------------------- */

static void test_vm_is_running_above_threshold(void)
{
    setup();
    vibration_monitor_notify_wake();
    /* 16 vibrating seconds > 15 bucket threshold */
    for (int i = 0; i < 16; i++)
        sim_vibrating_second();
    TEST_ASSERT_TRUE(vibration_monitor_is_running());
}

static void test_vm_is_running_at_threshold(void)
{
    setup();
    vibration_monitor_notify_wake();
    /* Exactly 15 = threshold */
    for (int i = 0; i < 15; i++)
        sim_vibrating_second();
    TEST_ASSERT_TRUE(vibration_monitor_is_running());
}

static void test_vm_is_not_running_below_threshold(void)
{
    setup();
    vibration_monitor_notify_wake();
    /* 14 vibrating seconds < 15 bucket threshold */
    for (int i = 0; i < 14; i++)
        sim_vibrating_second();
    TEST_ASSERT_FALSE(vibration_monitor_is_running());
}

static void test_vm_is_not_running_no_vibration(void)
{
    setup();
    vibration_monitor_notify_wake();
    for (int i = 0; i < 30; i++)
        sim_quiet_second();
    TEST_ASSERT_FALSE(vibration_monitor_is_running());
}

/* ---- Report ready tests ------------------------------------------------ */

static void test_vm_report_ready_after_interval(void)
{
    setup();
    vibration_monitor_notify_wake();
    /* Tick past the 1000ms report interval */
    vibration_monitor_tick(1000);
    TEST_ASSERT_TRUE(vibration_monitor_report_ready());
}

static void test_vm_report_ready_resets_after_read(void)
{
    setup();
    vibration_monitor_notify_wake();
    vibration_monitor_tick(1000);
    vibration_monitor_report_ready(); /* consumes */
    TEST_ASSERT_FALSE(vibration_monitor_report_ready());
}

/* ---- Should sleep tests ------------------------------------------------ */

static void test_vm_should_sleep_after_quiet_timeout(void)
{
    setup();
    vibration_monitor_notify_wake();
    /* No vibration, tick past 5000ms quiet timeout */
    vibration_monitor_tick(5001);
    TEST_ASSERT_TRUE(vibration_monitor_should_sleep());
}

static void test_vm_should_sleep_resets_on_vibration(void)
{
    setup();
    vibration_monitor_notify_wake();
    vibration_monitor_tick(4000); /* almost at timeout */
    vibration_monitor_feed_sample(200); /* vibration resets timer */
    vibration_monitor_tick(4000); /* still not at timeout from last vibration */
    TEST_ASSERT_FALSE(vibration_monitor_should_sleep());
}

static void test_vm_should_sleep_false_during_startup(void)
{
    setup();
    /* In STARTUP state, should_sleep should return false */
    vibration_monitor_tick(10000);
    TEST_ASSERT_FALSE(vibration_monitor_should_sleep());
}

/* ---- Configurable threshold -------------------------------------------- */

static void test_vm_configurable_bucket_threshold(void)
{
    vibration_monitor_reset();
    vibration_monitor_config_t cfg = {
        .threshold_mg = 50,
        .quiet_timeout_ms = 5000,
        .report_interval_ms = 1000,
        .bucket_threshold = 5,
    };
    vibration_monitor_init(&cfg);
    vibration_monitor_notify_wake();

    /* 5 vibrating seconds with lower threshold */
    for (int i = 0; i < 5; i++) {
        vibration_monitor_feed_sample(75);
        vibration_monitor_tick(1000);
    }
    TEST_ASSERT_TRUE(vibration_monitor_is_running());
}

/* ---- Suite entry point ------------------------------------------------- */

void run_vibration_monitor_tests(void)
{
    RUN_TEST(test_vm_initial_state_is_startup);
    RUN_TEST(test_vm_notify_wake_transitions_to_active);
    RUN_TEST(test_vm_no_vibration_zero_buckets);
    RUN_TEST(test_vm_all_vibrating_counts_all);
    RUN_TEST(test_vm_mixed_vibration_counts_correctly);
    RUN_TEST(test_vm_multiple_samples_in_bucket_counts_once);
    RUN_TEST(test_vm_rolling_window_evicts_old_buckets);
    RUN_TEST(test_vm_is_running_above_threshold);
    RUN_TEST(test_vm_is_running_at_threshold);
    RUN_TEST(test_vm_is_not_running_below_threshold);
    RUN_TEST(test_vm_is_not_running_no_vibration);
    RUN_TEST(test_vm_report_ready_after_interval);
    RUN_TEST(test_vm_report_ready_resets_after_read);
    RUN_TEST(test_vm_should_sleep_after_quiet_timeout);
    RUN_TEST(test_vm_should_sleep_resets_on_vibration);
    RUN_TEST(test_vm_should_sleep_false_during_startup);
    RUN_TEST(test_vm_configurable_bucket_threshold);
}
