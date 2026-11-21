#include "esp_idf_mocks.h"
#include <stdlib.h>
#include <string.h>

/*
 * Controllable state for HTTP mock.
 *
 * Tests set these before calling ha_post / ha_get to simulate specific
 * server responses.
 */
static int      s_mock_status        = 200;
static esp_err_t s_perform_result    = ESP_OK;
static int      s_fail_performs      = 0;
static char   s_mock_body[1024] = {0};
static char   s_last_url[512]   = {0};
static char   s_last_auth[512]  = {0};
static char   s_last_body[1024] = {0};
static char   s_last_content_type[64] = {0};
static int    s_last_method   = -1;
static int    s_init_count    = 0;
static int    s_perform_count = 0;
static int    s_close_count   = 0;

void http_mock_set_response(int status, const char *body)
{
    s_mock_status = status;
    strncpy(s_mock_body, body ? body : "", sizeof(s_mock_body) - 1);
}

void http_mock_set_perform_result(esp_err_t result)
{
    s_perform_result = result;
}

void http_mock_fail_next_performs(int n)
{
    s_fail_performs = n;
}

const char *http_mock_last_url(void)  { return s_last_url; }
const char *http_mock_last_auth(void) { return s_last_auth; }
const char *http_mock_last_body(void) { return s_last_body; }
const char *http_mock_last_content_type(void) { return s_last_content_type; }
int http_mock_last_method(void)   { return s_last_method; }
int http_mock_init_count(void)    { return s_init_count; }
int http_mock_perform_count(void) { return s_perform_count; }
int http_mock_close_count(void)   { return s_close_count; }

void http_mock_reset(void)
{
    s_mock_status    = 200;
    s_perform_result = ESP_OK;
    s_fail_performs  = 0;
    s_mock_body[0] = '\0';
    s_last_url[0]  = '\0';
    s_last_auth[0] = '\0';
    s_last_body[0] = '\0';
    s_last_content_type[0] = '\0';
    s_last_method   = -1;
    s_init_count    = 0;
    s_perform_count = 0;
    s_close_count   = 0;
}

/* ---- Internal mock client state ---------------------------------------- */

struct EspHttpClient {
    char url[512];
    char auth[512];
    char content_type[64];
    char post_body[1024];
    int  method;
    http_event_handle_cb event_handler;
    void *user_data;
};

esp_http_client_handle_t esp_http_client_init(const esp_http_client_config_t *config)
{
    struct EspHttpClient *c = calloc(1, sizeof(struct EspHttpClient));
    if (config->url) {
        strncpy(c->url, config->url, sizeof(c->url) - 1);
    }
    c->event_handler = config->event_handler;
    c->user_data     = config->user_data;
    s_init_count++;
    return (esp_http_client_handle_t)c;
}

esp_err_t esp_http_client_set_url(esp_http_client_handle_t client, const char *url)
{
    struct EspHttpClient *c = (struct EspHttpClient *)client;
    strncpy(c->url, url, sizeof(c->url) - 1);
    return ESP_OK;
}

esp_err_t esp_http_client_set_user_data(esp_http_client_handle_t client, void *data)
{
    ((struct EspHttpClient *)client)->user_data = data;
    return ESP_OK;
}

esp_err_t esp_http_client_set_method(esp_http_client_handle_t client, int method)
{
    ((struct EspHttpClient *)client)->method = method;
    return ESP_OK;
}

esp_err_t esp_http_client_set_header(esp_http_client_handle_t client,
                                      const char *key, const char *value)
{
    struct EspHttpClient *c = (struct EspHttpClient *)client;
    if (strcmp(key, "Authorization") == 0) {
        strncpy(c->auth, value, sizeof(c->auth) - 1);
    } else if (strcmp(key, "Content-Type") == 0) {
        strncpy(c->content_type, value ? value : "", sizeof(c->content_type) - 1);
    }
    return ESP_OK;
}

/* Like ESP-IDF: NULL data clears the body and removes the Content-Type header. */
esp_err_t esp_http_client_set_post_field(esp_http_client_handle_t client,
                                          const char *data, int len)
{
    struct EspHttpClient *c = (struct EspHttpClient *)client;
    if (data == NULL) {
        c->post_body[0] = '\0';
        c->content_type[0] = '\0';
        return ESP_OK;
    }
    int copy = len < (int)sizeof(c->post_body) - 1 ? len : (int)sizeof(c->post_body) - 1;
    memcpy(c->post_body, data, copy);
    c->post_body[copy] = '\0';
    return ESP_OK;
}

/* Records what the request sent, then delivers the mock response. */
esp_err_t esp_http_client_perform(esp_http_client_handle_t client)
{
    struct EspHttpClient *c = (struct EspHttpClient *)client;
    s_perform_count++;
    strncpy(s_last_url, c->url, sizeof(s_last_url) - 1);
    strncpy(s_last_auth, c->auth, sizeof(s_last_auth) - 1);
    strncpy(s_last_body, c->post_body, sizeof(s_last_body) - 1);
    strncpy(s_last_content_type, c->content_type, sizeof(s_last_content_type) - 1);
    s_last_method = c->method;

    if (s_fail_performs > 0) {
        s_fail_performs--;
        return ESP_FAIL;
    }
    if (s_perform_result != ESP_OK) return s_perform_result;
    if (c->event_handler && s_mock_body[0] != '\0') {
        esp_http_client_event_t evt = {
            .event_id = HTTP_EVENT_ON_DATA,
            .user_data = c->user_data,
            .data = s_mock_body,
            .data_len = (int)strlen(s_mock_body),
        };
        c->event_handler(&evt);
    }
    return ESP_OK;
}

int esp_http_client_get_status_code(esp_http_client_handle_t client)
{
    (void)client;
    return s_mock_status;
}

esp_err_t esp_http_client_close(esp_http_client_handle_t client)
{
    (void)client;
    s_close_count++;
    return ESP_OK;
}

esp_err_t esp_http_client_cleanup(esp_http_client_handle_t client)
{
    free(client);
    return ESP_OK;
}
