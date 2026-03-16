#include <string.h>
#include <stdio.h>
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "esp_log.h"
#include "esp_sleep.h"
#include "nvs_flash.h"
#include "onewire_bus.h"
#include "ds18b20.h"

#include "openthread_manager.h"
#include "ha_client.h"
#include "secrets/thread_auth.h"

static const char *TAG = "freezer";

#define TEMP_SENSOR_PIN    5
#define DEEP_SLEEP_US      (60ULL * 1000000ULL)

extern const char ca_bundle_pem_start[] asm("_binary_ca_bundle_pem_start");
extern const char ca_bundle_pem_end[]   asm("_binary_ca_bundle_pem_end");

static bool read_temperature(float *out_celsius)
{
    onewire_bus_handle_t bus = NULL;
    onewire_bus_config_t bus_cfg = {
        .bus_gpio_num   = TEMP_SENSOR_PIN,
        .flags.en_pull_up = true,
    };
    onewire_bus_rmt_config_t rmt_cfg = {
        .max_rx_bytes = 10,
    };
    if (onewire_new_bus_rmt(&bus_cfg, &rmt_cfg, &bus) != ESP_OK) {
        ESP_LOGE(TAG, "1-Wire bus init failed on GPIO %d", TEMP_SENSOR_PIN);
        return false;
    }

    onewire_device_iter_handle_t iter = NULL;
    ESP_ERROR_CHECK(onewire_new_device_iter(bus, &iter));

    onewire_device_t device;
    esp_err_t result = onewire_device_iter_get_next(iter, &device);
    onewire_del_device_iter(iter);

    if (result != ESP_OK) {
        ESP_LOGE(TAG, "No DS18B20 found on GPIO %d", TEMP_SENSOR_PIN);
        return false;
    }

    ds18b20_device_handle_t sensor = NULL;
    ds18b20_config_t cfg           = {};
    if (ds18b20_new_device(&device, &cfg, &sensor) != ESP_OK) {
        ESP_LOGE(TAG, "ds18b20_new_device failed");
        return false;
    }

    if (ds18b20_trigger_temperature_conversion(sensor) != ESP_OK) {
        ESP_LOGE(TAG, "Temperature conversion failed");
        return false;
    }
    vTaskDelay(pdMS_TO_TICKS(800)); /* DS18B20 needs ~750 ms for 12-bit conversion */

    if (ds18b20_get_temperature(sensor, out_celsius) != ESP_OK) {
        ESP_LOGE(TAG, "Temperature read failed");
        return false;
    }

    ESP_LOGI(TAG, "Temperature: %.2f°C", *out_celsius);
    return true;
}

void app_main(void)
{
    ESP_LOGI(TAG, "Freezer temp sensor starting");

    /* 1. NVS (required by OpenThread for persistent storage). */
    esp_err_t ret = nvs_flash_init();
    if (ret == ESP_ERR_NVS_NO_FREE_PAGES || ret == ESP_ERR_NVS_NEW_VERSION_FOUND) {
        ESP_ERROR_CHECK(nvs_flash_erase());
        ret = nvs_flash_init();
    }
    ESP_ERROR_CHECK(ret);

    /* 2. Read temperature. */
    float celsius = 0.0f;
    if (!read_temperature(&celsius)) {
        ESP_LOGE(TAG, "Temperature read failed — sleeping");
        esp_deep_sleep(DEEP_SLEEP_US);
        return; /* not reached */
    }

    /* 3. Join Thread network. */
    static const ot_credentials_t creds = {
        .network_name      = THREAD_NETWORK_NAME,
        .channel           = THREAD_CHANNEL,
        .pan_id            = THREAD_PAN_ID,
        .extended_pan_id   = THREAD_EXT_PAN_ID,
        .network_key       = THREAD_NETWORK_KEY,
        .mesh_local_prefix = THREAD_MESH_LOCAL_PREFIX,
    };

    if (ot_manager_init(&creds, 30000) != ESP_OK) {
        ESP_LOGE(TAG, "Thread join failed — sleeping");
        esp_deep_sleep(DEEP_SLEEP_US);
        return;
    }

    /* 4. Post to Home Assistant. */
    ha_client_init(THREAD_HA_BASE_URL, THREAD_HA_API_KEY, ca_bundle_pem_start);

    char payload[128];
    snprintf(payload, sizeof(payload),
             "{\"state\":\"%.2f\",\"attributes\":{\"unit_of_measurement\":\"°C\"}}",
             celsius);

    int code = ha_post("/api/states/sensor.freezer_temperature", payload);
    if (code < 0) {
        ESP_LOGE(TAG, "HA POST failed");
    } else {
        ESP_LOGI(TAG, "HA POST -> %d", code);
    }

    /* 5. Clean up and deep sleep. */
    ot_manager_deinit();
    ESP_LOGI(TAG, "Sleeping for 60 s");
    esp_deep_sleep(DEEP_SLEEP_US);
}
