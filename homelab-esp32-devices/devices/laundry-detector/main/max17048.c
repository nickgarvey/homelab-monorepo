#include "max17048.h"
#include "driver/i2c_master.h"
#include "esp_log.h"

static const char *TAG = "max17048";

#define MAX17048_ADDR   0x36
#define REG_VCELL       0x02
#define REG_SOC         0x04
#define I2C_TIMEOUT_MS  100

static i2c_master_bus_handle_t s_bus = NULL;
static i2c_master_dev_handle_t s_dev = NULL;
static bool s_initialized = false;

/* ---- Low-level helpers ------------------------------------------------- */

static esp_err_t read_reg16(uint8_t reg, uint8_t *out)
{
    return i2c_master_transmit_receive(s_dev, &reg, 1, out, 2, I2C_TIMEOUT_MS);
}

/* ---- Public API -------------------------------------------------------- */

bool max17048_init(const max17048_config_t *cfg)
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
        .device_address = MAX17048_ADDR,
        .dev_addr_length = I2C_ADDR_BIT_LEN_7,
        .scl_speed_hz = 400000,
    };
    esp_err_t err = i2c_master_bus_add_device(s_bus, &dev_cfg, &s_dev);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "I2C add device failed: %d", err);
        return false;
    }

    s_initialized = true;
    ESP_LOGI(TAG, "MAX17048 initialized");
    return true;
}

bool max17048_read_voltage(float *out_volts)
{
    if (!s_initialized) return false;

    uint8_t raw[2];
    esp_err_t err = read_reg16(REG_VCELL, raw);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "VCELL read failed: %d", err);
        return false;
    }

    /* VCELL: 16-bit big-endian, 1 LSB = 78.125 uV */
    uint16_t vcell = ((uint16_t)raw[0] << 8) | raw[1];
    *out_volts = vcell * 78.125e-6f;
    return true;
}

bool max17048_read_soc(uint8_t *out_percent)
{
    if (!s_initialized) return false;

    uint8_t raw[2];
    esp_err_t err = read_reg16(REG_SOC, raw);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "SOC read failed: %d", err);
        return false;
    }

    /* SOC: high byte = integer %, low byte = 1/256 fraction */
    uint8_t pct = raw[0];
    if (pct > 100) pct = 100;
    *out_percent = pct;
    return true;
}

void max17048_reset(void)
{
    s_bus = NULL;
    s_dev = NULL;
    s_initialized = false;
}
