#pragma once
#include "esp_idf_mocks.h"

typedef struct OnewireBus        *onewire_bus_handle_t;
typedef struct OnewireDeviceIter *onewire_device_iter_handle_t;

typedef struct {
    int  bus_gpio_num;
    struct { bool en_pull_up; } flags;
} onewire_bus_config_t;

typedef struct {
    int max_rx_bytes;
} onewire_bus_rmt_config_t;

typedef struct {
    int address;
} onewire_device_t;

esp_err_t onewire_new_bus_rmt(const onewire_bus_config_t *bus_cfg,
                               const onewire_bus_rmt_config_t *rmt_cfg,
                               onewire_bus_handle_t *ret_bus);
esp_err_t onewire_new_device_iter(onewire_bus_handle_t bus,
                                   onewire_device_iter_handle_t *ret_iter);
esp_err_t onewire_device_iter_get_next(onewire_device_iter_handle_t iter,
                                        onewire_device_t *next_device);
esp_err_t onewire_del_device_iter(onewire_device_iter_handle_t iter);
