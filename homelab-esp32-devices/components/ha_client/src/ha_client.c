#include "ha_client.h"

#include <string.h>
#include <stdio.h>
#include "esp_http_client.h"
#include "esp_log.h"

static const char *TAG = "ha_client";

#define HA_BUF_SIZE    1024
#define HA_URL_MAX     256
#define HA_AUTH_MAX    512

static char s_base_url[HA_URL_MAX];
static char s_api_key[HA_AUTH_MAX];
static const char *s_ca_pem;

typedef struct {
    char *buf;
    int   len;
    int   cap;
} http_body_t;

static esp_err_t http_event_handler(esp_http_client_event_t *evt)
{
    http_body_t *body = (http_body_t *)evt->user_data;
    if (evt->event_id == HTTP_EVENT_ON_DATA && body) {
        int remaining = body->cap - body->len - 1;
        int copy = evt->data_len < remaining ? evt->data_len : remaining;
        if (copy > 0) {
            memcpy(body->buf + body->len, evt->data, copy);
            body->len += copy;
            body->buf[body->len] = '\0';
        }
    }
    return ESP_OK;
}

void ha_client_init(const char *base_url, const char *api_key, const char *ca_pem)
{
    strncpy(s_base_url, base_url, sizeof(s_base_url) - 1);
    s_base_url[sizeof(s_base_url) - 1] = '\0';
    strncpy(s_api_key, api_key, sizeof(s_api_key) - 1);
    s_api_key[sizeof(s_api_key) - 1] = '\0';
    s_ca_pem = ca_pem;
}

int ha_post(const char *path, const char *json_body)
{
    char url[HA_URL_MAX + 128];
    snprintf(url, sizeof(url), "%s%s", s_base_url, path);

    char auth_header[HA_AUTH_MAX + 8];
    snprintf(auth_header, sizeof(auth_header), "Bearer %s", s_api_key);

    char body_buf[HA_BUF_SIZE] = {0};
    http_body_t body = { .buf = body_buf, .len = 0, .cap = HA_BUF_SIZE };

    esp_http_client_config_t config = {
        .url = url,
        .event_handler = http_event_handler,
        .user_data = &body,
        .transport_type = HTTP_TRANSPORT_OVER_SSL,
        .cert_pem = s_ca_pem,
        .timeout_ms = 10000,
    };
    esp_http_client_handle_t client = esp_http_client_init(&config);
    esp_http_client_set_method(client, HTTP_METHOD_POST);
    esp_http_client_set_header(client, "Authorization", auth_header);
    esp_http_client_set_header(client, "Content-Type", "application/json");
    esp_http_client_set_post_field(client, json_body, strlen(json_body));

    esp_err_t err = esp_http_client_perform(client);
    int status = (err == ESP_OK) ? esp_http_client_get_status_code(client) : -1;
    esp_http_client_cleanup(client);

    if (status < 0) {
        ESP_LOGE(TAG, "POST %s failed", path);
    } else {
        ESP_LOGI(TAG, "POST %s -> %d", path, status);
    }
    return status;
}

int ha_get(const char *path, char *out_buf, int out_cap)
{
    char url[HA_URL_MAX + 128];
    snprintf(url, sizeof(url), "%s%s", s_base_url, path);

    char auth_header[HA_AUTH_MAX + 8];
    snprintf(auth_header, sizeof(auth_header), "Bearer %s", s_api_key);

    http_body_t body = { .buf = out_buf, .len = 0, .cap = out_cap };
    if (out_buf) out_buf[0] = '\0';

    esp_http_client_config_t config = {
        .url = url,
        .event_handler = http_event_handler,
        .user_data = &body,
        .transport_type = HTTP_TRANSPORT_OVER_SSL,
        .cert_pem = s_ca_pem,
        .timeout_ms = 10000,
    };
    esp_http_client_handle_t client = esp_http_client_init(&config);
    esp_http_client_set_header(client, "Authorization", auth_header);

    esp_err_t err = esp_http_client_perform(client);
    int status = (err == ESP_OK) ? esp_http_client_get_status_code(client) : -1;
    esp_http_client_cleanup(client);

    if (status < 0) {
        ESP_LOGE(TAG, "GET %s failed", path);
    } else {
        ESP_LOGI(TAG, "GET %s -> %d", path, status);
    }
    return status;
}
