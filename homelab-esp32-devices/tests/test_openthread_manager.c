#include "unity.h"
#include "openthread_mocks.h"
#include "openthread_manager.h"

static const uint8_t k_ext_pan_id[8]  = {0x11,0x22,0x33,0x44,0x55,0x66,0x77,0x88};
static const uint8_t k_network_key[16] = {
    0x00,0x11,0x22,0x33,0x44,0x55,0x66,0x77,
    0x88,0x99,0xaa,0xbb,0xcc,0xdd,0xee,0xff
};

/* ---- test 1: is_attached returns false before any init ------------------ */

void test_is_attached_false_before_init(void)
{
    ot_mock_reset();
    TEST_ASSERT_FALSE(ot_manager_is_attached());
}

/* ---- test 2: timeout when role not achieved ----------------------------- */

void test_init_returns_timeout_when_role_not_achieved(void)
{
    ot_mock_reset();
    ot_mock_set_join_immediately(false);

    ot_credentials_t creds = {
        .network_name      = "TestNet",
        .channel           = 15,
        .pan_id            = 0x1234,
        .extended_pan_id   = k_ext_pan_id,
        .network_key       = k_network_key,
        .mesh_local_prefix = "fd11:22::/64",
    };
    esp_err_t ret = ot_manager_init(&creds, 0);
    TEST_ASSERT_EQUAL(ESP_ERR_TIMEOUT, ret);
    TEST_ASSERT_FALSE(ot_manager_is_attached());
    ot_manager_deinit();
}

/* ---- test 3: init succeeds when role achieved immediately --------------- */

void test_init_succeeds_when_role_achieved(void)
{
    ot_mock_reset();
    ot_mock_set_join_immediately(true);

    ot_credentials_t creds = {
        .network_name      = "TestNet",
        .channel           = 15,
        .pan_id            = 0x1234,
        .extended_pan_id   = k_ext_pan_id,
        .network_key       = k_network_key,
        .mesh_local_prefix = "fd11:22::/64",
    };
    esp_err_t ret = ot_manager_init(&creds, 30000);
    TEST_ASSERT_EQUAL(ESP_OK, ret);
    TEST_ASSERT_TRUE(ot_manager_is_attached());
    ot_manager_deinit();
}

/* ---- test 4: deinit resets attached state ------------------------------ */

void test_deinit_resets_attached_state(void)
{
    ot_mock_reset();
    ot_mock_set_join_immediately(true);

    ot_credentials_t creds = {
        .network_name      = "TestNet",
        .channel           = 15,
        .pan_id            = 0x1234,
        .extended_pan_id   = k_ext_pan_id,
        .network_key       = k_network_key,
        .mesh_local_prefix = NULL,
    };
    esp_err_t ret = ot_manager_init(&creds, 30000);
    TEST_ASSERT_EQUAL(ESP_OK, ret);
    TEST_ASSERT_TRUE(ot_manager_is_attached());

    ot_manager_deinit();
    TEST_ASSERT_FALSE(ot_manager_is_attached());
}

/* ---- test 5: all credential fields accepted without crash --------------- */

void test_accepts_all_credential_fields(void)
{
    ot_mock_reset();
    ot_mock_set_join_immediately(true);

    static const uint8_t ext_pan[8]  = {0xAA,0xBB,0xCC,0xDD,0xEE,0xFF,0x11,0x22};
    static const uint8_t net_key[16] = {
        0x00,0x01,0x02,0x03,0x04,0x05,0x06,0x07,
        0x08,0x09,0x0A,0x0B,0x0C,0x0D,0x0E,0x0F
    };
    ot_credentials_t creds = {
        .network_name      = "FullCredNet",
        .channel           = 25,
        .pan_id            = 0xABCD,
        .extended_pan_id   = ext_pan,
        .network_key       = net_key,
        .mesh_local_prefix = "fdab:cd::/64",
    };
    esp_err_t ret = ot_manager_init(&creds, 30000);
    TEST_ASSERT_EQUAL(ESP_OK, ret);
    ot_manager_deinit();
}

/* ---- test 6: esp_openthread_start failure → returns error immediately -- */

void test_start_failure_returns_error(void)
{
    ot_mock_reset();
    ot_mock_set_start_result(ESP_FAIL);

    ot_credentials_t creds = {
        .network_name      = "TestNet",
        .channel           = 15,
        .pan_id            = 0x1234,
        .extended_pan_id   = k_ext_pan_id,
        .network_key       = k_network_key,
        .mesh_local_prefix = "fd11:22::/64",
    };
    esp_err_t ret = ot_manager_init(&creds, 30000);
    TEST_ASSERT_NOT_EQUAL(ESP_OK, ret);
    TEST_ASSERT_FALSE(ot_manager_is_attached());
}

/* ---- test 7: esp_openthread_auto_start failure → stop is called -------- */

void test_auto_start_failure_calls_stop(void)
{
    ot_mock_reset();
    ot_mock_set_auto_start_result(ESP_FAIL);

    ot_credentials_t creds = {
        .network_name      = "TestNet",
        .channel           = 15,
        .pan_id            = 0x1234,
        .extended_pan_id   = k_ext_pan_id,
        .network_key       = k_network_key,
        .mesh_local_prefix = "fd11:22::/64",
    };
    esp_err_t ret = ot_manager_init(&creds, 30000);
    TEST_ASSERT_NOT_EQUAL(ESP_OK, ret);
    TEST_ASSERT_EQUAL_INT(1, ot_mock_get_stop_count());
}

/* ---- test suite entry point -------------------------------------------- */

void run_openthread_manager_tests(void)
{
    RUN_TEST(test_is_attached_false_before_init);
    RUN_TEST(test_init_returns_timeout_when_role_not_achieved);
    RUN_TEST(test_init_succeeds_when_role_achieved);
    RUN_TEST(test_deinit_resets_attached_state);
    RUN_TEST(test_accepts_all_credential_fields);
    RUN_TEST(test_start_failure_returns_error);
    RUN_TEST(test_auto_start_failure_calls_stop);
}
