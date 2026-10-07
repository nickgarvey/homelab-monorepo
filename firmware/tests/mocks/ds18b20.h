#pragma once
#include "esp_idf_mocks.h"
#include "onewire_bus.h"

typedef struct Ds18b20Device *ds18b20_device_handle_t;

typedef struct {
    int dummy;
} ds18b20_config_t;

esp_err_t ds18b20_new_device(const onewire_device_t *device,
                              const ds18b20_config_t *config,
                              ds18b20_device_handle_t *ret_sensor);
esp_err_t ds18b20_trigger_temperature_conversion(ds18b20_device_handle_t sensor);
esp_err_t ds18b20_get_temperature(ds18b20_device_handle_t sensor, float *temperature);
