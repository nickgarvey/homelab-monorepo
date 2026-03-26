extern "C" {
#include <string.h>
#include <stdio.h>
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "esp_log.h"
#include "nvs_flash.h"
#include "neopixel.h"

#ifdef CONFIG_USE_FAKE_TEMP_SENSOR
#include "fake_temp_sensor.h"
#else
#include "ds18b20_reader.h"
#endif
}

#include <esp_matter.h>
#include <esp_matter_endpoint.h>
#include <esp_openthread_types.h>
#include <platform/ESP32/OpenthreadLauncher.h>
#include <setup_payload/OnboardingCodesUtil.h>
#include <app/server/Dnssd.h>

static const char *TAG = "freezer";

#define TEMP_SENSOR_PIN    5
#ifdef CONFIG_USE_FAKE_TEMP_SENSOR
#define REPORT_INTERVAL_MS (5 * 1000)
#else
#define REPORT_INTERVAL_MS (60 * 1000)
#endif
#define DEBUG_NEOPIXEL_PIN       9
#define DEBUG_NEOPIXEL_POWER_PIN 20

#define LED_DIM 24

using namespace esp_matter;
using namespace chip::app::Clusters;

static uint16_t s_temperature_endpoint_id = 0;

static void set_debug_led(uint8_t r, uint8_t g, uint8_t b)
{
    neopixel_set(r, g, b);
}

/* -------------------------------------------------------------------------- */
/* Temperature reading (real DS18B20 or fake sine wave)                       */
/* -------------------------------------------------------------------------- */

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
    return ds18b20_reader_read(TEMP_SENSOR_PIN, out_celsius);
}

#endif /* CONFIG_USE_FAKE_TEMP_SENSOR */

/* -------------------------------------------------------------------------- */
/* Matter callbacks                                                           */
/* -------------------------------------------------------------------------- */

static esp_err_t app_attribute_update_cb(
    attribute::callback_type_t type,
    uint16_t endpoint_id, uint32_t cluster_id,
    uint32_t attribute_id, esp_matter_attr_val_t *val, void *priv_data)
{
    /* Temperature sensor is read-only — nothing to handle. */
    return ESP_OK;
}

static esp_err_t app_identification_cb(
    identification::callback_type_t type,
    uint16_t endpoint_id, uint8_t effect_id, uint8_t effect_variant,
    void *priv_data)
{
    ESP_LOGI(TAG, "Identify: endpoint=%u effect=%u", endpoint_id, effect_id);
    for (int i = 0; i < 3; i++) {
        set_debug_led(LED_DIM, LED_DIM, LED_DIM);
        vTaskDelay(pdMS_TO_TICKS(300));
        set_debug_led(0, 0, 0);
        vTaskDelay(pdMS_TO_TICKS(300));
    }
    return ESP_OK;
}

static void app_event_cb(const ChipDeviceEvent *event, intptr_t arg)
{
    switch (event->Type) {
    case chip::DeviceLayer::DeviceEventType::kCommissioningComplete:
        ESP_LOGI(TAG, "Commissioning complete");
        set_debug_led(0, LED_DIM, 0);
        break;
    case chip::DeviceLayer::DeviceEventType::kFabricRemoved:
        ESP_LOGI(TAG, "Fabric removed");
        set_debug_led(LED_DIM, LED_DIM / 2, 0);
        break;
    case chip::DeviceLayer::DeviceEventType::kThreadStateChange:
        /* device_callback_internal calls StartServer() on kInterfaceIpAddressChanged,
         * but that event only fires for WiFi/Ethernet — not Thread. Trigger it here
         * so mDNS and SRP re-register after Thread joins and IPv6 addresses are ready.
         * UpdateThreadInterface() runs before user handlers so addresses are already
         * in the LwIP netif when we get here. */
        if (event->ThreadStateChange.AddressChanged &&
            chip::DeviceLayer::ConnectivityMgr().IsThreadAttached()) {
            ESP_LOGI(TAG, "Thread IPv6 address changed, refreshing mDNS/SRP");
            chip::app::DnssdServer::Instance().StartServer();
        }
        break;
    default:
        break;
    }
}

/* -------------------------------------------------------------------------- */
/* Temperature reporting task                                                 */
/* -------------------------------------------------------------------------- */

static void temp_report_task(void *arg)
{
    while (1) {
        set_debug_led(0, 0, LED_DIM); /* blue: reading sensor */

        float celsius = 0.0f;
        bool read_ok = read_temperature(&celsius);

        if (read_ok) {
            int16_t matter_temp = (int16_t)(celsius * 100);
            esp_matter_attr_val_t val = esp_matter_nullable_int16(matter_temp);

            esp_err_t err = attribute::update(
                s_temperature_endpoint_id,
                TemperatureMeasurement::Id,
                TemperatureMeasurement::Attributes::MeasuredValue::Id,
                &val);

            if (err != ESP_OK) {
                ESP_LOGE(TAG, "attribute::update failed: %d", err);
                set_debug_led(LED_DIM, 0, 0);
            } else {
                ESP_LOGI(TAG, "Matter attribute updated: %d (0.01°C)", matter_temp);
                set_debug_led(0, LED_DIM, 0);
            }
        } else {
            ESP_LOGW(TAG, "Temperature read failed, will retry next cycle");
            set_debug_led(LED_DIM, 0, 0);
        }

        vTaskDelay(pdMS_TO_TICKS(REPORT_INTERVAL_MS));
    }
}

/* -------------------------------------------------------------------------- */
/* app_main                                                                   */
/* -------------------------------------------------------------------------- */

extern "C" void app_main(void)
{
    /* Visual bring-up marker */
    neopixel_init(DEBUG_NEOPIXEL_PIN, DEBUG_NEOPIXEL_POWER_PIN);
    set_debug_led(LED_DIM, LED_DIM, LED_DIM);

    ESP_LOGI(TAG, "Freezer temp sensor (Matter) starting");

    /* 1. NVS (required by Matter for fabric/commissioning storage). */
    esp_err_t ret = nvs_flash_init();
    if (ret == ESP_ERR_NVS_NO_FREE_PAGES || ret == ESP_ERR_NVS_NEW_VERSION_FOUND) {
        ESP_ERROR_CHECK(nvs_flash_erase());
        ret = nvs_flash_init();
    }
    ESP_ERROR_CHECK(ret);

    /* 2. Create Matter node.
     *    Product/vendor names come from CHIP_DEVICE_CONFIG_DEVICE_PRODUCT_NAME /
     *    CHIP_DEVICE_CONFIG_DEVICE_VENDOR_NAME, defined in CHIPProjectConfig.h
     *    from Kconfig values (CONFIG_DEVICE_PRODUCT_NAME / CONFIG_DEVICE_VENDOR_NAME). */
    node::config_t node_config;
    node_t *node = node::create(&node_config,
                                app_attribute_update_cb,
                                app_identification_cb);
    if (!node) {
        ESP_LOGE(TAG, "Matter node creation failed");
        set_debug_led(LED_DIM, 0, 0);
        return;
    }

    /* 3. Create temperature sensor endpoint.
     *    Values are in 0.01°C units per Matter spec. */
    endpoint::temperature_sensor::config_t temp_config;
    temp_config.temperature_measurement.measured_value = nullable<int16_t>(-1800);
    temp_config.temperature_measurement.min_measured_value = nullable<int16_t>(-4000);
    temp_config.temperature_measurement.max_measured_value = nullable<int16_t>(12500);

    endpoint_t *ep = endpoint::temperature_sensor::create(
        node, &temp_config, ENDPOINT_FLAG_NONE, NULL);
    if (!ep) {
        ESP_LOGE(TAG, "Temperature sensor endpoint creation failed");
        set_debug_led(LED_DIM, 0, 0);
        return;
    }
    s_temperature_endpoint_id = endpoint::get_id(ep);
    ESP_LOGI(TAG, "Temperature sensor endpoint id=%u", s_temperature_endpoint_id);

    /* 4. Configure OpenThread platform for native 802.15.4 radio (ESP32-C6). */
    esp_openthread_platform_config_t ot_config = {};
    ot_config.radio_config.radio_mode = RADIO_MODE_NATIVE;
    ot_config.host_config.host_connection_mode = HOST_CONNECTION_MODE_NONE;
    ot_config.port_config.storage_partition_name = "nvs";
    ot_config.port_config.netif_queue_size = 10;
    ot_config.port_config.task_queue_size = 10;
    set_openthread_platform_config(&ot_config);

    /* 5. Start Matter stack (initializes Thread internally). */
    set_debug_led(0, 0, LED_DIM);
    ret = esp_matter::start(app_event_cb);
    if (ret != ESP_OK) {
        ESP_LOGE(TAG, "esp_matter::start failed: %d", ret);
        set_debug_led(LED_DIM, 0, 0);
        return;
    }
    ESP_LOGI(TAG, "Matter stack started");
    PrintOnboardingCodes(chip::RendezvousInformationFlags(chip::RendezvousInformationFlag::kOnNetwork));

    set_debug_led(LED_DIM, LED_DIM / 2, 0); /* amber: waiting for commissioning */

    /* 7. Start temperature reporting task. */
    xTaskCreate(temp_report_task, "temp_report", 4096, NULL, 5, NULL);
}
