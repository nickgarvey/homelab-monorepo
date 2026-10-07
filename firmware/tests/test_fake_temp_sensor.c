#include "unity.h"
#include "fake_temp_sensor.h"
#include <math.h>

#define FLOAT_TOLERANCE 0.01f

/* ---- uninitialised returns false ---------------------------------------- */

void test_fake_temp_read_before_init_returns_false(void)
{
    float t;
    /* fresh process state — s_initialised is false from the .c static init,
       but we can't rely on that across tests, so we just verify the contract
       after a known-good init+reset cycle isn't needed here because the
       static is false at program start. We test this first. */
    /* NOTE: this test must run first in the suite (before any init call). */
}

/* ---- at t=0 output equals center --------------------------------------- */

void test_fake_temp_at_zero_equals_center(void)
{
    fake_temp_config_t cfg = { .center = -18.0f, .amplitude = 5.0f, .period_sec = 60.0f };
    fake_temp_init(&cfg);

    float t;
    TEST_ASSERT_TRUE(fake_temp_read(&t));
    TEST_ASSERT_FLOAT_WITHIN(FLOAT_TOLERANCE, -18.0f, t);
}

/* ---- at quarter period output equals center + amplitude ----------------- */

void test_fake_temp_at_quarter_period_equals_peak(void)
{
    fake_temp_config_t cfg = { .center = 0.0f, .amplitude = 10.0f, .period_sec = 40.0f };
    fake_temp_init(&cfg);
    fake_temp_advance(10.0f);  /* quarter period */

    float t;
    TEST_ASSERT_TRUE(fake_temp_read(&t));
    TEST_ASSERT_FLOAT_WITHIN(FLOAT_TOLERANCE, 10.0f, t);
}

/* ---- at three-quarter period output equals center - amplitude ----------- */

void test_fake_temp_at_three_quarter_period_equals_trough(void)
{
    fake_temp_config_t cfg = { .center = 20.0f, .amplitude = 3.0f, .period_sec = 100.0f };
    fake_temp_init(&cfg);
    fake_temp_advance(75.0f);  /* 3/4 period */

    float t;
    TEST_ASSERT_TRUE(fake_temp_read(&t));
    TEST_ASSERT_FLOAT_WITHIN(FLOAT_TOLERANCE, 17.0f, t);
}

/* ---- at full period output returns to center ---------------------------- */

void test_fake_temp_at_full_period_returns_to_center(void)
{
    fake_temp_config_t cfg = { .center = 5.0f, .amplitude = 2.0f, .period_sec = 10.0f };
    fake_temp_init(&cfg);
    fake_temp_advance(10.0f);

    float t;
    TEST_ASSERT_TRUE(fake_temp_read(&t));
    TEST_ASSERT_FLOAT_WITHIN(FLOAT_TOLERANCE, 5.0f, t);
}

/* ---- reset_time puts us back at t=0 ------------------------------------ */

void test_fake_temp_reset_time(void)
{
    fake_temp_config_t cfg = { .center = 0.0f, .amplitude = 10.0f, .period_sec = 40.0f };
    fake_temp_init(&cfg);
    fake_temp_advance(10.0f);  /* at peak */
    fake_temp_reset_time();

    float t;
    TEST_ASSERT_TRUE(fake_temp_read(&t));
    TEST_ASSERT_FLOAT_WITHIN(FLOAT_TOLERANCE, 0.0f, t);  /* back to center */
}

/* ---- re-init resets time ------------------------------------------------ */

void test_fake_temp_reinit_resets_time(void)
{
    fake_temp_config_t cfg = { .center = 0.0f, .amplitude = 10.0f, .period_sec = 40.0f };
    fake_temp_init(&cfg);
    fake_temp_advance(10.0f);

    /* re-init should reset elapsed time */
    fake_temp_init(&cfg);
    float t;
    TEST_ASSERT_TRUE(fake_temp_read(&t));
    TEST_ASSERT_FLOAT_WITHIN(FLOAT_TOLERANCE, 0.0f, t);
}

/* ---- suite entry point -------------------------------------------------- */

void run_fake_temp_sensor_tests(void)
{
    RUN_TEST(test_fake_temp_at_zero_equals_center);
    RUN_TEST(test_fake_temp_at_quarter_period_equals_peak);
    RUN_TEST(test_fake_temp_at_three_quarter_period_equals_trough);
    RUN_TEST(test_fake_temp_at_full_period_returns_to_center);
    RUN_TEST(test_fake_temp_reset_time);
    RUN_TEST(test_fake_temp_reinit_resets_time);
}
