#include "esp_idf_mocks.h"
#include <string.h>

/* ---- Mock state -------------------------------------------------------- */

struct I2cMasterBus { int dummy; };
struct I2cMasterDev { uint16_t addr; };

static struct I2cMasterBus s_fake_bus;
static struct I2cMasterDev s_fake_devs[4];
static int s_dev_count = 0;

/* Register file: 256 bytes per device address (up to 4 devices). */
#define MAX_I2C_DEVS 4
static uint8_t s_registers[MAX_I2C_DEVS][256];

/* Error injection */
static esp_err_t s_bus_init_result = ESP_OK;
static esp_err_t s_transmit_receive_result = ESP_OK;
static esp_err_t s_transmit_result = ESP_OK;

/* Write capture */
static uint8_t s_last_write_reg = 0;
static uint8_t s_last_write_val = 0;
static int s_write_count = 0;
static int s_bus_init_count = 0;

/* ---- Mock control API -------------------------------------------------- */

void i2c_mock_set_register(uint16_t dev_addr, uint8_t reg, uint8_t value)
{
    for (int i = 0; i < s_dev_count; i++) {
        if (s_fake_devs[i].addr == dev_addr) {
            s_registers[i][reg] = value;
            return;
        }
    }
    /* Device not added yet — pre-populate slot 0 for the expected address */
    if (s_dev_count < MAX_I2C_DEVS) {
        s_fake_devs[s_dev_count].addr = dev_addr;
        s_registers[s_dev_count][reg] = value;
        s_dev_count++;
    }
}

uint8_t i2c_mock_get_register(uint16_t dev_addr, uint8_t reg)
{
    for (int i = 0; i < s_dev_count; i++) {
        if (s_fake_devs[i].addr == dev_addr) {
            return s_registers[i][reg];
        }
    }
    return 0;
}

void i2c_mock_set_bus_init_result(esp_err_t result) { s_bus_init_result = result; }
void i2c_mock_set_transmit_receive_result(esp_err_t result) { s_transmit_receive_result = result; }
void i2c_mock_set_transmit_result(esp_err_t result) { s_transmit_result = result; }

uint8_t i2c_mock_last_write_reg(void) { return s_last_write_reg; }
uint8_t i2c_mock_last_write_val(void) { return s_last_write_val; }
int     i2c_mock_get_write_count(void) { return s_write_count; }
int     i2c_mock_get_bus_init_count(void) { return s_bus_init_count; }

void i2c_mock_reset(void)
{
    memset(s_registers, 0, sizeof(s_registers));
    s_dev_count = 0;
    s_bus_init_result = ESP_OK;
    s_transmit_receive_result = ESP_OK;
    s_transmit_result = ESP_OK;
    s_last_write_reg = 0;
    s_last_write_val = 0;
    s_write_count = 0;
    s_bus_init_count = 0;
}

/* ---- Find device index by handle --------------------------------------- */

static int find_dev_index(i2c_master_dev_handle_t dev)
{
    for (int i = 0; i < s_dev_count; i++) {
        if (dev == &s_fake_devs[i])
            return i;
    }
    return -1;
}

/* ---- ESP-IDF I2C master stubs ------------------------------------------ */

esp_err_t i2c_new_master_bus(const i2c_master_bus_config_t *config,
                              i2c_master_bus_handle_t *ret_handle)
{
    (void)config;
    s_bus_init_count++;
    if (s_bus_init_result != ESP_OK)
        return s_bus_init_result;
    *ret_handle = &s_fake_bus;
    return ESP_OK;
}

esp_err_t i2c_master_bus_add_device(i2c_master_bus_handle_t bus,
                                     const i2c_device_config_t *config,
                                     i2c_master_dev_handle_t *ret_handle)
{
    (void)bus;
    /* Find existing or create new slot */
    for (int i = 0; i < s_dev_count; i++) {
        if (s_fake_devs[i].addr == config->device_address) {
            *ret_handle = &s_fake_devs[i];
            return ESP_OK;
        }
    }
    if (s_dev_count >= MAX_I2C_DEVS)
        return ESP_FAIL;
    s_fake_devs[s_dev_count].addr = config->device_address;
    *ret_handle = &s_fake_devs[s_dev_count];
    s_dev_count++;
    return ESP_OK;
}

esp_err_t i2c_master_transmit_receive(i2c_master_dev_handle_t dev,
                                       const uint8_t *write_buffer,
                                       size_t write_size,
                                       uint8_t *read_buffer,
                                       size_t read_size,
                                       int xfer_timeout_ms)
{
    (void)xfer_timeout_ms;
    if (s_transmit_receive_result != ESP_OK)
        return s_transmit_receive_result;

    int idx = find_dev_index(dev);
    if (idx < 0)
        return ESP_FAIL;

    /* write_buffer[0] is the register address.
     * Always auto-increment for multi-byte reads. Bit 7 (LIS3DH auto-increment
     * flag) is masked off to get the starting register address. */
    uint8_t reg = write_buffer[0] & 0x7F;

    for (size_t i = 0; i < read_size; i++) {
        read_buffer[i] = s_registers[idx][reg];
        reg++;
    }
    return ESP_OK;
}

esp_err_t i2c_master_receive(i2c_master_dev_handle_t dev,
                              uint8_t *read_buffer,
                              size_t read_size,
                              int xfer_timeout_ms)
{
    (void)xfer_timeout_ms;
    if (s_transmit_receive_result != ESP_OK)
        return s_transmit_receive_result;

    int idx = find_dev_index(dev);
    if (idx < 0)
        return ESP_FAIL;

    /* Read sequential bytes starting from register 0.
     * For command-based sensors (SHT40), pre-populate registers 0-5
     * with the expected response bytes. */
    for (size_t i = 0; i < read_size; i++) {
        read_buffer[i] = s_registers[idx][i];
    }
    return ESP_OK;
}

esp_err_t i2c_master_transmit(i2c_master_dev_handle_t dev,
                               const uint8_t *write_buffer,
                               size_t write_size,
                               int xfer_timeout_ms)
{
    (void)xfer_timeout_ms;
    if (s_transmit_result != ESP_OK)
        return s_transmit_result;

    int idx = find_dev_index(dev);
    if (idx < 0)
        return ESP_FAIL;

    /* write_buffer[0] = register, write_buffer[1] = value */
    if (write_size >= 2) {
        uint8_t reg = write_buffer[0];
        uint8_t val = write_buffer[1];
        s_registers[idx][reg] = val;
        s_last_write_reg = reg;
        s_last_write_val = val;
        s_write_count++;
    }
    return ESP_OK;
}

