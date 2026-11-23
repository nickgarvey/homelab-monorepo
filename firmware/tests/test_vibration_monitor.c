#include "unity.h"
#include "vibration_monitor.h"
#include "fake_accel.h"

static const vibration_monitor_config_t CFG = {
    .threshold_mg = 90,
    .bucket_threshold = 15,
    .stop_delay_ms = 600 * 1000,
};

static void vibrating_seconds(int n)
{
    for (int i = 0; i < n; i++) {
        vibration_monitor_feed(200);
        vibration_monitor_tick(1000);
    }
}

static void quiet_seconds(int n)
{
    for (int i = 0; i < n; i++) {
        vibration_monitor_feed(10);
        vibration_monitor_tick(1000);
    }
}

static void test_vm_starts_stopped_and_idle(void)
{
    vibration_monitor_init(&CFG);
    TEST_ASSERT_FALSE(vibration_monitor_is_running());
    TEST_ASSERT_TRUE(vibration_monitor_is_idle());
}

static void test_vm_runs_at_bucket_threshold(void)
{
    vibration_monitor_init(&CFG);
    vibrating_seconds(14);
    TEST_ASSERT_FALSE(vibration_monitor_is_running());
    vibrating_seconds(1);
    TEST_ASSERT_TRUE(vibration_monitor_is_running());
}

static void test_vm_sparse_vibration_counts_per_second(void)
{
    vibration_monitor_init(&CFG);
    /* one vibrating 0.5 s batch every 4 s: 30 active buckets over 120 s */
    for (int i = 0; i < 30; i++) {
        vibration_monitor_feed(150);
        vibration_monitor_tick(1000);
        quiet_seconds(3);
    }
    TEST_ASSERT_EQUAL_UINT16(30, vibration_monitor_active_buckets());
    TEST_ASSERT_TRUE(vibration_monitor_is_running());
}

static void test_vm_below_threshold_peaks_do_not_count(void)
{
    vibration_monitor_init(&CFG);
    for (int i = 0; i < 60; i++) {
        vibration_monitor_feed(89);
        vibration_monitor_tick(1000);
    }
    TEST_ASSERT_EQUAL_UINT16(0, vibration_monitor_active_buckets());
}

static void test_vm_pause_shorter_than_stop_delay_keeps_running(void)
{
    vibration_monitor_init(&CFG);
    vibrating_seconds(120);
    quiet_seconds(8 * 60);   /* an 8-minute soak */
    TEST_ASSERT_TRUE(vibration_monitor_is_running());
    vibrating_seconds(60);
    TEST_ASSERT_TRUE(vibration_monitor_is_running());
}

static void test_vm_stops_after_stop_delay_below_threshold(void)
{
    vibration_monitor_init(&CFG);
    vibrating_seconds(120);
    /* The count drops below 15 on quiet second 106, which is the first of the 600 seconds
     * below the threshold; Stopped on quiet second 106 + 599. */
    quiet_seconds(106 + 598);
    TEST_ASSERT_TRUE(vibration_monitor_is_running());
    quiet_seconds(1);
    TEST_ASSERT_FALSE(vibration_monitor_is_running());
    TEST_ASSERT_TRUE(vibration_monitor_is_idle());
}

static void test_vm_brief_knock_is_not_idle_until_window_clears(void)
{
    vibration_monitor_init(&CFG);
    vibrating_seconds(2);
    TEST_ASSERT_FALSE(vibration_monitor_is_running());
    TEST_ASSERT_FALSE(vibration_monitor_is_idle());
    quiet_seconds(118);
    TEST_ASSERT_FALSE(vibration_monitor_is_idle());
    quiet_seconds(2);
    TEST_ASSERT_TRUE(vibration_monitor_is_idle());
}

static void test_vm_tick_splits_into_seconds(void)
{
    vibration_monitor_init(&CFG);
    for (int i = 0; i < 30; i++) {       /* 0.5 s batches */
        vibration_monitor_feed(200);
        vibration_monitor_tick(500);
    }
    TEST_ASSERT_EQUAL_UINT16(15, vibration_monitor_active_buckets());
}

static void test_vm_batch_peak_ignores_gravity_and_orientation(void)
{
    vm_sample_t still[4] = { { 700, 0, 714 }, { 701, 1, 713 }, { 699, -1, 715 }, { 700, 0, 714 } };
    TEST_ASSERT_TRUE(vibration_monitor_batch_peak_mg(still, 4) <= 2);
}

static void test_vm_batch_peak_measures_shake(void)
{
    vm_sample_t shake[4] = { { 100, 0, 1000 }, { -100, 0, 1000 }, { 100, 0, 1000 }, { -100, 0, 1000 } };
    TEST_ASSERT_EQUAL_UINT16(100, vibration_monitor_batch_peak_mg(shake, 4));
    TEST_ASSERT_EQUAL_UINT16(0, vibration_monitor_batch_peak_mg(shake, 0));
}

static void test_vm_fake_accel_drives_running(void)
{
    vibration_monitor_init(&CFG);
    fake_accel_set_washing(true);
    vm_sample_t batch[25];
    for (int i = 0; i < 40; i++) {
        fake_accel_generate(batch, 25, 50);
        vibration_monitor_feed(vibration_monitor_batch_peak_mg(batch, 25));
        vibration_monitor_tick(500);
    }
    TEST_ASSERT_TRUE(vibration_monitor_is_running());
    fake_accel_set_washing(false);
    fake_accel_generate(batch, 25, 50);
    TEST_ASSERT_EQUAL_UINT16(0, vibration_monitor_batch_peak_mg(batch, 25));
}

void run_vibration_monitor_tests(void)
{
    RUN_TEST(test_vm_starts_stopped_and_idle);
    RUN_TEST(test_vm_runs_at_bucket_threshold);
    RUN_TEST(test_vm_sparse_vibration_counts_per_second);
    RUN_TEST(test_vm_below_threshold_peaks_do_not_count);
    RUN_TEST(test_vm_pause_shorter_than_stop_delay_keeps_running);
    RUN_TEST(test_vm_stops_after_stop_delay_below_threshold);
    RUN_TEST(test_vm_brief_knock_is_not_idle_until_window_clears);
    RUN_TEST(test_vm_tick_splits_into_seconds);
    RUN_TEST(test_vm_batch_peak_ignores_gravity_and_orientation);
    RUN_TEST(test_vm_batch_peak_measures_shake);
    RUN_TEST(test_vm_fake_accel_drives_running);
}
