#include "unity.h"
#include "boot_action.h"

/* ---- Uncommissioned: always wait for pairing, regardless of wake cause -- */

static void test_uncommissioned_fresh_boot(void)
{
    TEST_ASSERT_EQUAL(BOOT_ACTION_WAIT_FOR_COMMISSION,
                      determine_boot_action(WAKEUP_FRESH_BOOT, false));
}

static void test_uncommissioned_timer_wake(void)
{
    TEST_ASSERT_EQUAL(BOOT_ACTION_WAIT_FOR_COMMISSION,
                      determine_boot_action(WAKEUP_TIMER, false));
}

static void test_uncommissioned_gpio_wake(void)
{
    TEST_ASSERT_EQUAL(BOOT_ACTION_WAIT_FOR_COMMISSION,
                      determine_boot_action(WAKEUP_GPIO, false));
}

/* ---- Commissioned: dispatch by wake cause ------------------------------ */

static void test_commissioned_fresh_boot(void)
{
    TEST_ASSERT_EQUAL(BOOT_ACTION_STARTUP_SLEEP,
                      determine_boot_action(WAKEUP_FRESH_BOOT, true));
}

static void test_commissioned_timer_wake(void)
{
    TEST_ASSERT_EQUAL(BOOT_ACTION_TIMER_REPORT,
                      determine_boot_action(WAKEUP_TIMER, true));
}

static void test_commissioned_gpio_wake(void)
{
    TEST_ASSERT_EQUAL(BOOT_ACTION_ACTIVE_MONITOR,
                      determine_boot_action(WAKEUP_GPIO, true));
}

/* ---- Edge case: unknown wake cause when commissioned → startup sleep --- */

static void test_commissioned_unknown_wake_cause(void)
{
    TEST_ASSERT_EQUAL(BOOT_ACTION_STARTUP_SLEEP,
                      determine_boot_action((wakeup_cause_t)99, true));
}

/* ---- Suite entry point ------------------------------------------------- */

void run_boot_action_tests(void)
{
    RUN_TEST(test_uncommissioned_fresh_boot);
    RUN_TEST(test_uncommissioned_timer_wake);
    RUN_TEST(test_uncommissioned_gpio_wake);
    RUN_TEST(test_commissioned_fresh_boot);
    RUN_TEST(test_commissioned_timer_wake);
    RUN_TEST(test_commissioned_gpio_wake);
    RUN_TEST(test_commissioned_unknown_wake_cause);
}
