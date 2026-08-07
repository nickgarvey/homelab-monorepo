#include "lis3dh.h"
#include "driver/i2c_master.h"
#include "esp_log.h"

static const char *TAG = "lis3dh";

/* LIS3DH register addresses */
#define REG_WHO_AM_I   0x0F
#define REG_CTRL_REG1  0x20
#define REG_CTRL_REG2  0x21
#define REG_CTRL_REG3  0x22
#define REG_CTRL_REG4  0x23
#define REG_INT1_CFG   0x30
#define REG_INT1_SRC   0x31
#define REG_INT1_THS   0x32
#define REG_INT1_DUR   0x33
#define REG_OUT_X_L    0x28

#define WHO_AM_I_VALUE 0x33
#define I2C_TIMEOUT_MS 100

/* Auto-increment bit for multi-byte reads */
#define AUTO_INC       0x80

static i2c_master_bus_handle_t s_bus = NULL;
static i2c_master_dev_handle_t s_dev = NULL;
static bool s_initialized = false;

/* ---- Low-level helpers ------------------------------------------------- */

static esp_err_t write_reg(uint8_t reg, uint8_t value)
{
    uint8_t buf[2] = { reg, value };
    return i2c_master_transmit(s_dev, buf, 2, I2C_TIMEOUT_MS);
}

static esp_err_t read_reg(uint8_t reg, uint8_t *out, size_t len)
{
    /* Set auto-increment bit for multi-byte reads */
    uint8_t addr = (len > 1) ? (reg | AUTO_INC) : reg;
    return i2c_master_transmit_receive(s_dev, &addr, 1, out, len, I2C_TIMEOUT_MS);
}

/* ---- Public API -------------------------------------------------------- */

bool lis3dh_init(const lis3dh_config_t *cfg)
{
    /* Create I2C bus if not already created */
    if (!s_bus) {
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

    /* Add LIS3DH device */
    i2c_device_config_t dev_cfg = {
        .device_address = cfg->addr,
        .dev_addr_length = I2C_ADDR_BIT_LEN_7,
        .scl_speed_hz = 400000,
    };
    esp_err_t err = i2c_master_bus_add_device(s_bus, &dev_cfg, &s_dev);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "I2C add device failed: %d", err);
        return false;
    }

    /* Check WHO_AM_I */
    uint8_t who = 0;
    err = read_reg(REG_WHO_AM_I, &who, 1);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "WHO_AM_I read failed: %d", err);
        return false;
    }
    if (who != WHO_AM_I_VALUE) {
        ESP_LOGE(TAG, "WHO_AM_I mismatch: expected 0x%02X, got 0x%02X",
                 WHO_AM_I_VALUE, who);
        return false;
    }

    /* Configure: 100Hz ODR, all axes enabled */
    err = write_reg(REG_CTRL_REG1, 0x57);
    if (err != ESP_OK) return false;

    /* +/-2g range, high-resolution mode */
    err = write_reg(REG_CTRL_REG4, 0x08);
    if (err != ESP_OK) return false;

    s_initialized = true;
    ESP_LOGI(TAG, "LIS3DH initialized (addr=0x%02X)", cfg->addr);
    return true;
}

bool lis3dh_read_accel(lis3dh_accel_t *out)
{
    if (!s_initialized) return false;

    uint8_t raw[6];
    esp_err_t err = read_reg(REG_OUT_X_L, raw, 6);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "Accel read failed: %d", err);
        return false;
    }

    /* Raw values are left-justified 12-bit in 16-bit registers.
     * At +/-2g high-res: 1 mg/LSB after right-shift by 4. */
    int16_t raw_x = (int16_t)(raw[1] << 8 | raw[0]);
    int16_t raw_y = (int16_t)(raw[3] << 8 | raw[2]);
    int16_t raw_z = (int16_t)(raw[5] << 8 | raw[4]);

    out->x_mg = raw_x >> 4;
    out->y_mg = raw_y >> 4;
    out->z_mg = raw_z >> 4;

    return true;
}

bool lis3dh_configure_motion_interrupt(uint8_t threshold_mg, uint8_t duration_ms)
{
    if (!s_initialized) return false;

    /* CTRL_REG2: enable high-pass filter on INT1 interrupts.
     * HP_IA1 = bit 0. This makes the interrupt threshold compare against
     * *change* in acceleration, not absolute value (filters out gravity). */
    esp_err_t err = write_reg(REG_CTRL_REG2, 0x01);
    if (err != ESP_OK) return false;

    /* CTRL_REG3: route AOI1 interrupt to INT1 pin */
    err = write_reg(REG_CTRL_REG3, 0x40);
    if (err != ESP_OK) return false;

    /* INT1_CFG: OR combination, high events on X, Y, Z
     * XHIE | YHIE | ZHIE = 0x2A */
    err = write_reg(REG_INT1_CFG, 0x2A);
    if (err != ESP_OK) return false;

    /* INT1_THS: threshold in mg, register unit = 16mg/LSB at +/-2g */
    uint8_t ths = threshold_mg / 16;
    if (ths == 0) ths = 1;
    err = write_reg(REG_INT1_THS, ths);
    if (err != ESP_OK) return false;

    /* INT1_DURATION: register unit = 1/ODR. At 100Hz, 1 LSB = 10ms */
    uint8_t dur = duration_ms / 10;
    if (dur == 0) dur = 1;
    err = write_reg(REG_INT1_DUR, dur);
    if (err != ESP_OK) return false;

    /* Read INT1_SRC to clear any latched interrupt */
    uint8_t int1_src = 0;
    read_reg(REG_INT1_SRC, &int1_src, 1);
    ESP_LOGI(TAG, "INT1_SRC after config: 0x%02X (IA=%d XH=%d YH=%d ZH=%d)",
             int1_src, (int1_src >> 6) & 1, (int1_src >> 1) & 1,
             (int1_src >> 3) & 1, (int1_src >> 5) & 1);

    /* Read it again to confirm it cleared */
    uint8_t int1_src2 = 0;
    read_reg(REG_INT1_SRC, &int1_src2, 1);
    ESP_LOGI(TAG, "INT1_SRC after clear read: 0x%02X", int1_src2);

    /* Debug: read CTRL_REG2 back to confirm HPF is set */
    uint8_t ctrl2 = 0;
    read_reg(REG_CTRL_REG2, &ctrl2, 1);
    ESP_LOGI(TAG, "CTRL_REG2: 0x%02X (HP_IA1=%d)", ctrl2, ctrl2 & 1);

    /* Debug: read current accel to see baseline */
    uint8_t raw[6];
    if (read_reg(REG_OUT_X_L, raw, 6) == ESP_OK) {
        int16_t x = (int16_t)(raw[1] << 8 | raw[0]) >> 4;
        int16_t y = (int16_t)(raw[3] << 8 | raw[2]) >> 4;
        int16_t z = (int16_t)(raw[5] << 8 | raw[4]) >> 4;
        ESP_LOGI(TAG, "Accel at sleep: x=%d y=%d z=%d mg", x, y, z);
    }

    ESP_LOGI(TAG, "Motion interrupt configured: threshold=%dmg(%d LSB) duration=%dms(%d LSB)",
             threshold_mg, ths, duration_ms, dur);
    return true;
}

void *lis3dh_get_i2c_bus(void)
{
    return (void *)s_bus;
}

void lis3dh_reset(void)
{
    s_bus = NULL;
    s_dev = NULL;
    s_initialized = false;
}
