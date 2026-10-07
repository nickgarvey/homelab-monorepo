#pragma once

#include <stdbool.h>

/**
 * Initialize and connect to WiFi.
 *
 * Blocks until connected or max_retries is exhausted.
 *
 * @param ssid        Network SSID
 * @param password    Network password
 * @param max_retries Number of reconnect attempts before giving up
 */
void wifi_manager_init(const char *ssid, const char *password, int max_retries);

/**
 * Returns true if the station currently has an IP address.
 */
bool wifi_manager_connected(void);
