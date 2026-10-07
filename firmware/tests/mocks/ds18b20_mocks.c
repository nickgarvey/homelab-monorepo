#include "ds18b20.h"
#include <stdlib.h>

/* Concrete definitions for opaque handle types declared in mock headers. */
struct OnewireBus        { int dummy; };
struct OnewireDeviceIter { int dummy; };
struct Ds18b20Device     { int dummy; };

/* ---- Mock state -------------------------------------------------------- */

static esp_err_t s_bus_init_result = ESP_OK;
static float     s_temperature     = 0.0f;
static int       s_bus_init_count  = 0;

void ds18b20_mock_set_bus_init_result(esp_err_t result) { s_bus_init_result = result; }
void ds18b20_mock_set_temperature(float t)              { s_temperature = t; }
int  ds18b20_mock_get_bus_init_count(void)              { return s_bus_init_count; }

void ds18b20_mock_reset(void)
{
    s_bus_init_result = ESP_OK;
    s_temperature     = 0.0f;
    s_bus_init_count  = 0;
}

/* ---- 1-Wire bus stubs -------------------------------------------------- */

static struct OnewireBus        s_fake_bus;
static struct OnewireDeviceIter s_fake_iter;

esp_err_t onewire_new_bus_rmt(const onewire_bus_config_t *bus_cfg,
                               const onewire_bus_rmt_config_t *rmt_cfg,
                               onewire_bus_handle_t *ret_bus)
{
    (void)bus_cfg; (void)rmt_cfg;
    s_bus_init_count++;
    if (s_bus_init_result == ESP_OK) {
        *ret_bus = &s_fake_bus;
    }
    return s_bus_init_result;
}

esp_err_t onewire_new_device_iter(onewire_bus_handle_t bus,
                                   onewire_device_iter_handle_t *ret_iter)
{
    (void)bus;
    *ret_iter = &s_fake_iter;
    return ESP_OK;
}

esp_err_t onewire_device_iter_get_next(onewire_device_iter_handle_t iter,
                                        onewire_device_t *next_device)
{
    (void)iter;
    next_device->address = 0xAA;
    return ESP_OK;
}

esp_err_t onewire_del_device_iter(onewire_device_iter_handle_t iter)
{
    (void)iter;
    return ESP_OK;
}

/* ---- DS18B20 stubs ----------------------------------------------------- */

static struct Ds18b20Device s_fake_sensor;

esp_err_t ds18b20_new_device(const onewire_device_t *device,
                              const ds18b20_config_t *config,
                              ds18b20_device_handle_t *ret_sensor)
{
    (void)device; (void)config;
    *ret_sensor = &s_fake_sensor;
    return ESP_OK;
}

esp_err_t ds18b20_trigger_temperature_conversion(ds18b20_device_handle_t sensor)
{
    (void)sensor;
    return ESP_OK;
}

esp_err_t ds18b20_get_temperature(ds18b20_device_handle_t sensor, float *temperature)
{
    (void)sensor;
    *temperature = s_temperature;
    return ESP_OK;
}
