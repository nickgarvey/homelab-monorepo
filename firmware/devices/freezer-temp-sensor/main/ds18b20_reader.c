#include "ds18b20_reader.h"
#include "onewire_bus.h"
#include "ds18b20.h"
#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

static const char *TAG = "ds18b20_reader";

static ds18b20_device_handle_t s_sensor = NULL;

void ds18b20_reader_reset(void)
{
    s_sensor = NULL;
}

static bool init_sensor(int gpio_num)
{
    onewire_bus_handle_t bus = NULL;
    onewire_bus_config_t bus_cfg = {
        .bus_gpio_num = gpio_num,
    };
    bus_cfg.flags.en_pull_up = true;
    onewire_bus_rmt_config_t rmt_cfg = {
        .max_rx_bytes = 10,
    };
    if (onewire_new_bus_rmt(&bus_cfg, &rmt_cfg, &bus) != ESP_OK) {
        ESP_LOGE(TAG, "1-Wire bus init failed on GPIO %d", gpio_num);
        return false;
    }

    onewire_device_iter_handle_t iter = NULL;
    if (onewire_new_device_iter(bus, &iter) != ESP_OK) {
        ESP_LOGE(TAG, "Failed to create device iterator");
        return false;
    }

    onewire_device_t device;
    esp_err_t result = onewire_device_iter_get_next(iter, &device);
    onewire_del_device_iter(iter);

    if (result != ESP_OK) {
        ESP_LOGE(TAG, "No DS18B20 found on GPIO %d", gpio_num);
        return false;
    }

    ds18b20_config_t cfg = {};
    if (ds18b20_new_device(&device, &cfg, &s_sensor) != ESP_OK) {
        ESP_LOGE(TAG, "ds18b20_new_device failed");
        return false;
    }

    ESP_LOGI(TAG, "DS18B20 initialized on GPIO %d", gpio_num);
    return true;
}

bool ds18b20_reader_read(int gpio_num, float *out_celsius)
{
    if (!s_sensor) {
        if (!init_sensor(gpio_num)) {
            return false;
        }
    }

    if (ds18b20_trigger_temperature_conversion(s_sensor) != ESP_OK) {
        ESP_LOGE(TAG, "Temperature conversion failed");
        return false;
    }
    vTaskDelay(pdMS_TO_TICKS(800));

    if (ds18b20_get_temperature(s_sensor, out_celsius) != ESP_OK) {
        ESP_LOGE(TAG, "Temperature read failed");
        return false;
    }

    ESP_LOGI(TAG, "Temperature: %.2f°C", *out_celsius);
    return true;
}
