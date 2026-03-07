#pragma once

#include <stddef.h>

/**
 * Configure the Home Assistant client.
 *
 * Must be called once before ha_post / ha_get.
 *
 * @param base_url  e.g. "https://homeassistant.home.arpa:8123"
 * @param api_key   Long-lived access token
 * @param ca_pem    PEM-encoded CA bundle for TLS verification (NULL-terminated string)
 */
void ha_client_init(const char *base_url, const char *api_key, const char *ca_pem);

/**
 * POST a JSON body to a HA API path.
 *
 * @param path       e.g. "/api/services/cover/toggle"
 * @param json_body  JSON string to send as the request body
 * @return HTTP status code, or -1 on transport error
 */
int ha_post(const char *path, const char *json_body);

/**
 * GET a HA API path and store the response body.
 *
 * @param path     e.g. "/api/states/cover.garage_door"
 * @param out_buf  Buffer to store the response body
 * @param out_cap  Size of out_buf
 * @return HTTP status code, or -1 on transport error
 */
int ha_get(const char *path, char *out_buf, int out_cap);
