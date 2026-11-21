#include "unity.h"
#include "esp_idf_mocks.h"
#include "ha_client.h"
#include "test_helpers.h"
#include <string.h>

static const char *TEST_BASE_URL = "https://ha.example.com:8123";
static const char *TEST_API_KEY  = "test-api-key-abc123";

static void setup(void)
{
    http_mock_reset();
    ha_client_init(TEST_BASE_URL, TEST_API_KEY);
}

void test_ha_post_builds_correct_url(void)
{
    setup();
    http_mock_set_response(200, "{}");
    ha_post("/api/services/cover/toggle", "{\"entity_id\":\"cover.garage\"}");

    const char *url = http_mock_last_url();
    TEST_ASSERT_NOT_NULL(url);
    TEST_ASSERT_NOT_NULL(strstr(url, "https://ha.example.com:8123"));
    TEST_ASSERT_NOT_NULL(strstr(url, "/api/services/cover/toggle"));
}

void test_ha_post_sends_bearer_auth_header(void)
{
    setup();
    http_mock_set_response(200, "{}");
    ha_post("/api/services/cover/toggle", "{}");

    const char *auth = http_mock_last_auth();
    TEST_ASSERT_NOT_NULL(auth);
    TEST_ASSERT_NOT_NULL(strstr(auth, "Bearer "));
    TEST_ASSERT_NOT_NULL(strstr(auth, TEST_API_KEY));
}

void test_ha_post_returns_status_code(void)
{
    setup();
    http_mock_set_response(200, "{}");
    int status = ha_post("/api/services/cover/toggle", "{}");
    TEST_ASSERT_EQUAL_INT(200, status);
}

void test_ha_post_returns_negative_on_error(void)
{
    setup();
    http_mock_set_response(-1, NULL);
    int status = ha_post("/api/services/cover/toggle", "{}");
    TEST_ASSERT_EQUAL_INT(-1, status);
}

void test_ha_post_sends_json_body(void)
{
    setup();
    http_mock_set_response(200, "{}");
    const char *payload = "{\"entity_id\":\"cover.garage_door\"}";
    ha_post("/api/services/cover/toggle", payload);

    const char *body = http_mock_last_body();
    TEST_ASSERT_NOT_NULL(body);
    TEST_ASSERT_EQUAL_STRING(payload, body);
}

void test_ha_get_builds_correct_url(void)
{
    setup();
    http_mock_set_response(200, "{\"state\":\"closed\"}");
    char buf[256] = {0};
    ha_get("/api/states/cover.garage_door", buf, sizeof(buf));

    const char *url = http_mock_last_url();
    TEST_ASSERT_NOT_NULL(strstr(url, "/api/states/cover.garage_door"));
}

void test_ha_get_populates_response_buffer(void)
{
    setup();
    const char *fake_body = "{\"state\":\"open\"}";
    http_mock_set_response(200, fake_body);

    char buf[256] = {0};
    int status = ha_get("/api/states/cover.garage_door", buf, sizeof(buf));

    TEST_ASSERT_EQUAL_INT(200, status);
    TEST_ASSERT_EQUAL_STRING(fake_body, buf);
}

void test_ha_get_truncates_to_buffer_capacity(void)
{
    setup();
    http_mock_set_response(200, "ABCDEFGHIJ");
    char buf[5] = {0};
    ha_get("/api/states/cover.garage_door", buf, sizeof(buf));

    /* Must be null-terminated and not overflow */
    TEST_ASSERT_EQUAL_INT('\0', buf[4]);
}

void test_requests_share_one_client(void)
{
    setup();
    http_mock_set_response(200, "{}");
    char buf[64];
    ha_get("/api/states/cover.garage_door", buf, sizeof(buf));
    ha_post("/api/services/cover/toggle", "{}");
    ha_get("/api/states/cover.garage_door", buf, sizeof(buf));

    TEST_ASSERT_EQUAL_INT(1, http_mock_init_count());
    TEST_ASSERT_EQUAL_INT(3, http_mock_perform_count());
    TEST_ASSERT_EQUAL_INT(0, http_mock_close_count());
}

void test_get_after_post_sends_no_body(void)
{
    setup();
    http_mock_set_response(200, "{}");
    ha_post("/api/services/cover/toggle", "{\"entity_id\":\"cover.garage\"}");
    TEST_ASSERT_EQUAL_INT(HTTP_METHOD_POST, http_mock_last_method());
    TEST_ASSERT_EQUAL_STRING("application/json", http_mock_last_content_type());

    char buf[64];
    ha_get("/api/states/cover.garage_door", buf, sizeof(buf));
    TEST_ASSERT_EQUAL_INT(HTTP_METHOD_GET, http_mock_last_method());
    TEST_ASSERT_EQUAL_STRING("", http_mock_last_body());
    TEST_ASSERT_EQUAL_STRING("", http_mock_last_content_type());
    TEST_ASSERT_NOT_NULL(strstr(http_mock_last_auth(), TEST_API_KEY));
}

void test_ha_get_retries_once_on_new_connection(void)
{
    setup();
    http_mock_set_response(200, "{\"state\":\"open\"}");
    http_mock_fail_next_performs(1);
    char buf[64];
    int status = ha_get("/api/states/cover.garage_door", buf, sizeof(buf));

    TEST_ASSERT_EQUAL_INT(200, status);
    TEST_ASSERT_EQUAL_STRING("{\"state\":\"open\"}", buf);
    TEST_ASSERT_EQUAL_INT(2, http_mock_perform_count());
    TEST_ASSERT_EQUAL_INT(1, http_mock_close_count());
}

void test_ha_get_gives_up_after_retry(void)
{
    setup();
    http_mock_fail_next_performs(2);
    char buf[64];
    int status = ha_get("/api/states/cover.garage_door", buf, sizeof(buf));

    TEST_ASSERT_EQUAL_INT(-1, status);
    TEST_ASSERT_EQUAL_INT(2, http_mock_perform_count());
    TEST_ASSERT_EQUAL_INT(2, http_mock_close_count());
}

void test_ha_post_is_not_retried(void)
{
    setup();
    http_mock_fail_next_performs(1);
    int status = ha_post("/api/services/cover/toggle", "{}");

    TEST_ASSERT_EQUAL_INT(-1, status);
    TEST_ASSERT_EQUAL_INT(1, http_mock_perform_count());
    TEST_ASSERT_EQUAL_INT(1, http_mock_close_count());
}

void run_ha_client_tests(void)
{
    RUN_TEST(test_requests_share_one_client);
    RUN_TEST(test_get_after_post_sends_no_body);
    RUN_TEST(test_ha_get_retries_once_on_new_connection);
    RUN_TEST(test_ha_get_gives_up_after_retry);
    RUN_TEST(test_ha_post_is_not_retried);
    RUN_TEST(test_ha_post_builds_correct_url);
    RUN_TEST(test_ha_post_sends_bearer_auth_header);
    RUN_TEST(test_ha_post_returns_status_code);
    RUN_TEST(test_ha_post_returns_negative_on_error);
    RUN_TEST(test_ha_post_sends_json_body);
    RUN_TEST(test_ha_get_builds_correct_url);
    RUN_TEST(test_ha_get_populates_response_buffer);
    RUN_TEST(test_ha_get_truncates_to_buffer_capacity);
}
