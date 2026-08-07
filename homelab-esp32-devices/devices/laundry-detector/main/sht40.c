#include "sht40.h"
#include "driver/i2c_master.h"
#include "esp_log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

static const char *TAG = "sht40";

#define SHT40_ADDR       0x44
#define CMD_MEASURE_HP   0xFD  /* High-precision measurement */
#define I2C_TIMEOUT_MS   100

static i2c_master_bus_handle_t s_bus = NULL;
static i2c_master_dev_handle_t s_dev = NULL;
static bool s_initialized = false;

/* ---- CRC-8 (polynomial 0x31, init 0xFF) -------------------------------- */

static uint8_t crc8(uint8_t msb, uint8_t lsb)
{
    uint8_t crc = 0xFF;
    uint8_t data[2] = { msb, lsb };
    for (int i = 0; i < 2; i++) {
        crc ^= data[i];
        for (int b = 0; b < 8; b++) {
            if (crc & 0x80)
                crc = (crc << 1) ^ 0x31;
            else
                crc <<= 1;
        }
    }
    return crc;
}

/* ---- Public API -------------------------------------------------------- */

bool sht40_init(const sht40_config_t *cfg)
{
    if (cfg->i2c_bus) {
        s_bus = (i2c_master_bus_handle_t)cfg->i2c_bus;
    } else if (!s_bus) {
        i2c_master_bus_config_t bus_cfg = {
            .i2c_port = 0,
            .sda_io_num = cfg->sda_gpio,
            .scl_io_num = cfg->scl_gpio,
            .clk_source = I2C_CLK_SRC_DEFAULT,
            .glitch_ignore_cnt = 7,
            .flags.enable_internal_pullup = true,
        };
        esp_err_t err = i2c_new_master_bus(&bus_cfg, &s_bus);
        if (err != ESP_OK) {
            ESP_LOGE(TAG, "I2C bus init failed: %d", err);
            return false;
        }
    }

    i2c_device_config_t dev_cfg = {
        .device_address = SHT40_ADDR,
        .dev_addr_length = I2C_ADDR_BIT_LEN_7,
        .scl_speed_hz = 400000,
    };
    esp_err_t err = i2c_master_bus_add_device(s_bus, &dev_cfg, &s_dev);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "I2C add device failed: %d", err);
        return false;
    }

    s_initialized = true;
    ESP_LOGI(TAG, "SHT40 initialized");
    return true;
}

bool sht40_read(sht40_reading_t *out)
{
    if (!s_initialized) return false;

    /* Send high-precision measurement command */
    uint8_t cmd = CMD_MEASURE_HP;
    esp_err_t err = i2c_master_transmit(s_dev, &cmd, 1, I2C_TIMEOUT_MS);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "Measurement command failed: %d", err);
        return false;
    }

    /* SHT40 needs ~10ms for high-precision measurement */
    vTaskDelay(pdMS_TO_TICKS(15));

    /* Read 6 bytes: temp_msb, temp_lsb, temp_crc, hum_msb, hum_lsb, hum_crc */
    uint8_t raw[6];
    err = i2c_master_receive(s_dev, raw, 6, I2C_TIMEOUT_MS);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "Read failed: %d", err);
        return false;
    }

    /* Verify CRCs */
    if (crc8(raw[0], raw[1]) != raw[2]) {
        ESP_LOGE(TAG, "Temperature CRC mismatch");
        return false;
    }
    if (crc8(raw[3], raw[4]) != raw[5]) {
        ESP_LOGE(TAG, "Humidity CRC mismatch");
        return false;
    }

    /* Convert raw values */
    uint16_t raw_temp = ((uint16_t)raw[0] << 8) | raw[1];
    uint16_t raw_hum  = ((uint16_t)raw[3] << 8) | raw[4];

    out->temperature_c = -45.0f + 175.0f * raw_temp / 65535.0f;

    float rh = -6.0f + 125.0f * raw_hum / 65535.0f;
    if (rh < 0.0f) rh = 0.0f;
    if (rh > 100.0f) rh = 100.0f;
    out->humidity_pct = rh;

    return true;
}

void sht40_reset(void)
{
    s_bus = NULL;
    s_dev = NULL;
    s_initialized = false;
}
