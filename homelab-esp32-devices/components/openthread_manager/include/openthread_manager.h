#pragma once

#include <stdint.h>
#include <stdbool.h>
#include "esp_err.h"

typedef struct {
    const char    *network_name;
    uint8_t        channel;
    uint16_t       pan_id;
    const uint8_t *extended_pan_id;    /* 8 bytes */
    const uint8_t *network_key;        /* 16 bytes */
    const char    *mesh_local_prefix;
} ot_credentials_t;

/**
 * Initialize OpenThread and join the Thread network.
 *
 * Blocks until the device achieves CHILD/ROUTER/LEADER role or timeout_ms elapses.
 *
 * @param creds       Thread network credentials
 * @param timeout_ms  Maximum time to wait for attachment (milliseconds)
 * @return ESP_OK on success, ESP_ERR_TIMEOUT if attachment not achieved
 */
esp_err_t ot_manager_init(const ot_credentials_t *creds, uint32_t timeout_ms);

/**
 * Returns true if the device is currently attached to a Thread network.
 */
bool ot_manager_is_attached(void);

/**
 * Stop OpenThread, release resources, and reset attachment state.
 */
void ot_manager_deinit(void);
