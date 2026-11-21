#include "ha_client.h"

#include <string.h>
#include <stdio.h>
#include "esp_http_client.h"
#include "esp_crt_bundle.h"
#include "esp_log.h"
#include "esp_timer.h"

static const char *TAG = "ha_client";

#define HA_URL_MAX     256
#define HA_AUTH_MAX    512

static char s_base_url[HA_URL_MAX];
static char s_auth_header[HA_AUTH_MAX + 8];

// One client for every request, so HTTP keep-alive reuses the TLS connection.
// A fresh connection costs a full handshake, about a second of elliptic-curve
// maths on the ESP32-S2, which has no hardware for it. With the status poll every
// few seconds, HA never lets the connection go idle long enough to close it, so
// only the first request after boot or after a dropped connection pays for one.
static esp_http_client_handle_t s_client;

typedef struct {
    char *buf;
    int   len;
    int   cap;
} http_body_t;

static esp_err_t http_event_handler(esp_http_client_event_t *evt)
{
    http_body_t *body = (http_body_t *)evt->user_data;
    if (evt->event_id == HTTP_EVENT_ON_DATA && body && body->buf) {
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

void ha_client_init(const char *base_url, const char *api_key)
{
    strncpy(s_base_url, base_url, sizeof(s_base_url) - 1);
    s_base_url[sizeof(s_base_url) - 1] = '\0';
    snprintf(s_auth_header, sizeof(s_auth_header), "Bearer %s", api_key);

    if (s_client) {
        esp_http_client_cleanup(s_client);
    }
    esp_http_client_config_t config = {
        .url = s_base_url,
        .event_handler = http_event_handler,
        .transport_type = HTTP_TRANSPORT_OVER_SSL,
        .crt_bundle_attach = esp_crt_bundle_attach,
        .timeout_ms = 10000,
    };
    s_client = esp_http_client_init(&config);
    esp_http_client_set_header(s_client, "Authorization", s_auth_header);
}

// Sends one request on the shared client. json_body NULL means a GET.
static esp_err_t perform(const char *url, const char *json_body, http_body_t *body)
{
    esp_http_client_set_url(s_client, url);
    esp_http_client_set_user_data(s_client, body);
    if (json_body) {
        esp_http_client_set_method(s_client, HTTP_METHOD_POST);
        esp_http_client_set_header(s_client, "Content-Type", "application/json");
        esp_http_client_set_post_field(s_client, json_body, strlen(json_body));
    } else {
        esp_http_client_set_method(s_client, HTTP_METHOD_GET);
        // Also removes the Content-Type header.
        esp_http_client_set_post_field(s_client, NULL, 0);
    }

    esp_err_t err = esp_http_client_perform(s_client);
    if (err != ESP_OK) {
        // Drop the connection so the next request starts a new one, rather than
        // reusing a socket the server or the network has already given up on.
        esp_http_client_close(s_client);
    }
    return err;
}

static int request(const char *method, const char *path, const char *json_body,
                   char *out_buf, int out_cap)
{
    if (!s_client) {
        ESP_LOGE(TAG, "%s %s: ha_client_init not called", method, path);
        return -1;
    }

    char url[HA_URL_MAX + 128];
    snprintf(url, sizeof(url), "%s%s", s_base_url, path);

    http_body_t body = { .buf = out_buf, .len = 0, .cap = out_cap };
    if (out_buf && out_cap > 0) out_buf[0] = '\0';

    int64_t start_us = esp_timer_get_time();
    esp_err_t err = perform(url, json_body, &body);
    if (err != ESP_OK && json_body == NULL) {
        // The kept-alive connection may have been closed by HA (a restart) while
        // idle, which only shows up when it is next used. A GET is safe to repeat
        // on a new connection. A POST is not retried: if it reached HA before the
        // failure, repeating it would toggle the door twice.
        body.len = 0;
        if (out_buf && out_cap > 0) out_buf[0] = '\0';
        err = perform(url, json_body, &body);
    }
    int elapsed_ms = (int)((esp_timer_get_time() - start_us) / 1000);

    if (err != ESP_OK) {
        ESP_LOGE(TAG, "%s %s failed (%s) after %d ms", method, path, esp_err_to_name(err), elapsed_ms);
        return -1;
    }
    int status = esp_http_client_get_status_code(s_client);
    ESP_LOGI(TAG, "%s %s -> %d in %d ms", method, path, status, elapsed_ms);
    return status;
}

int ha_post(const char *path, const char *json_body)
{
    char discard[64];
    return request("POST", path, json_body, discard, sizeof(discard));
}

int ha_get(const char *path, char *out_buf, int out_cap)
{
    return request("GET", path, NULL, out_buf, out_cap);
}
