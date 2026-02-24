#include "esp_idf_mocks.h"
#include <stdlib.h>
#include <string.h>

/*
 * Controllable state for HTTP mock.
 *
 * Tests set these before calling ha_post / ha_get to simulate specific
 * server responses.
 */
static int    s_mock_status   = 200;
static char   s_mock_body[1024] = {0};
static char   s_last_url[512]   = {0};
static char   s_last_auth[512]  = {0};
static char   s_last_body[1024] = {0};

void http_mock_set_response(int status, const char *body)
{
    s_mock_status = status;
    strncpy(s_mock_body, body ? body : "", sizeof(s_mock_body) - 1);
}

const char *http_mock_last_url(void)  { return s_last_url; }
const char *http_mock_last_auth(void) { return s_last_auth; }
const char *http_mock_last_body(void) { return s_last_body; }

void http_mock_reset(void)
{
    s_mock_status = 200;
    s_mock_body[0] = '\0';
    s_last_url[0]  = '\0';
    s_last_auth[0] = '\0';
    s_last_body[0] = '\0';
}

/* ---- Internal mock client state ---------------------------------------- */

struct EspHttpClient {
    char url[512];
    char auth[512];
    char post_body[1024];
    http_event_handle_cb event_handler;
    void *user_data;
};

esp_http_client_handle_t esp_http_client_init(const esp_http_client_config_t *config)
{
    struct EspHttpClient *c = calloc(1, sizeof(struct EspHttpClient));
    if (config->url) {
        strncpy(c->url, config->url, sizeof(c->url) - 1);
        strncpy(s_last_url, config->url, sizeof(s_last_url) - 1);
    }
    c->event_handler = config->event_handler;
    c->user_data     = config->user_data;
    return (esp_http_client_handle_t)c;
}

esp_err_t esp_http_client_set_method(esp_http_client_handle_t client, int method)
{
    (void)client; (void)method;
    return ESP_OK;
}

esp_err_t esp_http_client_set_header(esp_http_client_handle_t client,
                                      const char *key, const char *value)
{
    struct EspHttpClient *c = (struct EspHttpClient *)client;
    if (strcmp(key, "Authorization") == 0) {
        strncpy(c->auth, value, sizeof(c->auth) - 1);
        strncpy(s_last_auth, value, sizeof(s_last_auth) - 1);
    }
    return ESP_OK;
}

esp_err_t esp_http_client_set_post_field(esp_http_client_handle_t client,
                                          const char *data, int len)
{
    struct EspHttpClient *c = (struct EspHttpClient *)client;
    int copy = len < (int)sizeof(c->post_body) - 1 ? len : (int)sizeof(c->post_body) - 1;
    memcpy(c->post_body, data, copy);
    c->post_body[copy] = '\0';
    strncpy(s_last_body, c->post_body, sizeof(s_last_body) - 1);
    return ESP_OK;
}

esp_err_t esp_http_client_perform(esp_http_client_handle_t client)
{
    struct EspHttpClient *c = (struct EspHttpClient *)client;
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

esp_err_t esp_http_client_cleanup(esp_http_client_handle_t client)
{
    free(client);
    return ESP_OK;
}
