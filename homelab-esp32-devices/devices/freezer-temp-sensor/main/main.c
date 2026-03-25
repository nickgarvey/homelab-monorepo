#include <string.h>
#include <stdio.h>
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "esp_log.h"
#include "nvs_flash.h"
#include "esp_event.h"
#include "esp_netif.h"

#ifdef CONFIG_USE_FAKE_TEMP_SENSOR
#include "fake_temp_sensor.h"
#else
#include "onewire_bus.h"
#include "ds18b20.h"
#endif

#include "openthread_manager.h"
#include "ha_client.h"
#include "neopixel.h"
#include "secrets/thread_creds.h"
#include "secrets/ha_creds.h"

static const char *TAG = "freezer";

#define TEMP_SENSOR_PIN    5
#ifdef CONFIG_USE_FAKE_TEMP_SENSOR
#define REPORT_INTERVAL_MS (5 * 1000)
#else
#define REPORT_INTERVAL_MS (60 * 1000)
#endif
#define DEBUG_NEOPIXEL_PIN       8
#define DEBUG_NEOPIXEL_POWER_PIN -1

#define LED_DIM 24

extern const char ca_bundle_pem_start[] asm("_binary_ca_bundle_pem_start");
extern const char ca_bundle_pem_end[]   asm("_binary_ca_bundle_pem_end");

static void set_debug_led(uint8_t r, uint8_t g, uint8_t b)
{
    neopixel_set(r, g, b);
}

#ifdef CONFIG_USE_FAKE_TEMP_SENSOR

static bool s_fake_inited = false;

static bool read_temperature(float *out_celsius)
{
    if (!s_fake_inited) {
        fake_temp_config_t cfg = {
            .center      = -18.0f,
            .amplitude   = 5.0f,
            .period_sec  = 300.0f,
        };
        fake_temp_init(&cfg);
        s_fake_inited = true;
    }

    /* Advance by the report interval each call */
    fake_temp_advance((float)REPORT_INTERVAL_MS / 1000.0f);

    if (!fake_temp_read(out_celsius)) {
        ESP_LOGE(TAG, "Fake sensor read failed");
        return false;
    }

    ESP_LOGI(TAG, "Temperature (fake): %.2f°C", *out_celsius);
    return true;
}

#else /* real DS18B20 sensor */

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

#endif /* CONFIG_USE_FAKE_TEMP_SENSOR */

void app_main(void)
{
    /* Visual bring-up marker: if this lights, app_main is executing. */
    neopixel_init(DEBUG_NEOPIXEL_PIN, DEBUG_NEOPIXEL_POWER_PIN);
    set_debug_led(LED_DIM, LED_DIM, LED_DIM);

    ESP_LOGI(TAG, "Freezer temp sensor starting");

    /* 1. NVS (required by OpenThread for persistent storage). */
    esp_err_t ret = nvs_flash_init();
    if (ret == ESP_ERR_NVS_NO_FREE_PAGES || ret == ESP_ERR_NVS_NEW_VERSION_FOUND) {
        ESP_ERROR_CHECK(nvs_flash_erase());
        ret = nvs_flash_init();
    }
    ESP_ERROR_CHECK(ret);

    /* 1b. Core network/event loop setup required by OpenThread netif glue. */
    ESP_ERROR_CHECK(esp_netif_init());
    ret = esp_event_loop_create_default();
    if (ret != ESP_OK && ret != ESP_ERR_INVALID_STATE) {
        ESP_ERROR_CHECK(ret);
    }

    /* 2. Join Thread network (stays up for the lifetime of the device). */
    static const ot_credentials_t creds = {
        .network_name      = THREAD_NETWORK_NAME,
        .channel           = THREAD_CHANNEL,
        .pan_id            = THREAD_PAN_ID,
        .extended_pan_id   = THREAD_EXT_PAN_ID,
        .network_key       = THREAD_NETWORK_KEY,
        .mesh_local_prefix = THREAD_MESH_LOCAL_PREFIX,
    };

    if (ot_manager_init(&creds, 30000) != ESP_OK) {
        ESP_LOGE(TAG, "Thread join failed — will retry reads without network");
        set_debug_led(LED_DIM, LED_DIM / 2, 0); /* amber: app alive, no Thread */
    } else {
        set_debug_led(0, LED_DIM, 0); /* green: attached */
    }

    /* 3. Init HA client (posts will fail gracefully if Thread is down). */
    ha_client_init(THREAD_HA_BASE_URL, THREAD_HA_API_KEY, ca_bundle_pem_start);

    /* 4. Main loop: read temperature and report every 60 s. */
    while (1) {
        set_debug_led(0, 0, LED_DIM); /* blue: sensor read/report in progress */
        float celsius = 0.0f;
        if (read_temperature(&celsius)) {
            char payload[128];
            snprintf(payload, sizeof(payload),
                     "{\"state\":\"%.2f\",\"attributes\":{\"unit_of_measurement\":\"°C\"}}",
                     celsius);

            int code = ha_post("/api/states/sensor.freezer_temperature", payload);
            if (code < 0) {
                ESP_LOGE(TAG, "HA POST failed");
                set_debug_led(LED_DIM, 0, 0); /* red: HA post failed */
            } else {
                ESP_LOGI(TAG, "HA POST -> %d", code);
                set_debug_led(0, LED_DIM, 0); /* green: report succeeded */
            }
        } else {
            ESP_LOGW(TAG, "Temperature read failed, will retry next cycle");
            set_debug_led(LED_DIM, 0, 0); /* red: sensor read failed */
        }

        vTaskDelay(pdMS_TO_TICKS(REPORT_INTERVAL_MS));
    }
}
