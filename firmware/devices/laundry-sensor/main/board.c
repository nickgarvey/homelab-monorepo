#include "board.h"

#include "driver/gpio.h"
#include "esp_log.h"
#include "esp_sleep.h"
#include "sdkconfig.h"

#if CONFIG_LAUNDRY_BOARD_PCB
#include "driver/i2c_master.h"
#include "driver/spi_master.h"
#include "esp_adc/adc_cali.h"
#include "esp_adc/adc_cali_scheme.h"
#include "esp_adc/adc_oneshot.h"
#include "battery.h"
#include "lis2dw12.h"
#else
#include "esp_timer.h"
#include "fake_accel.h"
#endif

static const char *TAG = "board";

#if CONFIG_LAUNDRY_BOARD_PCB
/* hardware/laundry-sensor/circuit.py */
#define PIN_LED        GPIO_NUM_14   /* through 5.1k to the LED, active high */
#define PIN_BUTTON     GPIO_NUM_3    /* USER, 100k pull-up on the board, active low */
#define PIN_ACC_INT1   GPIO_NUM_0
#define PIN_ACC_INT2   GPIO_NUM_1
#define PIN_ACC_CS     GPIO_NUM_18
#define PIN_ACC_SCLK   GPIO_NUM_19
#define PIN_ACC_MOSI   GPIO_NUM_20
#define PIN_ACC_MISO   GPIO_NUM_21
#define PIN_I2C_SDA    GPIO_NUM_22
#define PIN_I2C_SCL    GPIO_NUM_23
#define PIN_VBAT_SENSE GPIO_NUM_2
#else
/* Adafruit ESP32-C6 Feather */
#define PIN_LED        GPIO_NUM_15   /* red LED, active high */
#define PIN_BUTTON     GPIO_NUM_9    /* BOOT, active low */
#endif

/* ---- interrupts ----------------------------------------------------------- */

typedef struct {
    gpio_num_t pin;
    uint32_t bit;
    TaskHandle_t task;
} irq_t;

static irq_t s_button_irq;

static void IRAM_ATTR on_irq(void *arg)
{
    irq_t *irq = (irq_t *)arg;
    BaseType_t woken = pdFALSE;
    gpio_intr_disable(irq->pin);   /* level-triggered: the task re-enables once handled */
    if (irq->task) {
        xTaskNotifyFromISR(irq->task, irq->bit, eSetBits, &woken);
    }
    portYIELD_FROM_ISR(woken);
}

/* Level interrupt on pin that also wakes the chip from light sleep. Starts disabled. */
static void irq_setup(irq_t *irq, gpio_num_t pin, uint32_t bit, bool active_high, bool pull_up)
{
    irq->pin = pin;
    irq->bit = bit;
    gpio_config_t io = {
        .pin_bit_mask = 1ULL << pin,
        .mode = GPIO_MODE_INPUT,
        .pull_up_en = pull_up ? GPIO_PULLUP_ENABLE : GPIO_PULLUP_DISABLE,
        .pull_down_en = GPIO_PULLDOWN_DISABLE,
        .intr_type = active_high ? GPIO_INTR_HIGH_LEVEL : GPIO_INTR_LOW_LEVEL,
    };
    gpio_config(&io);
    gpio_intr_disable(pin);
    gpio_isr_handler_add(pin, on_irq, irq);
}

static void irq_enable(irq_t *irq, bool on, bool active_high)
{
    if (on) {
        gpio_wakeup_enable(irq->pin, active_high ? GPIO_INTR_HIGH_LEVEL : GPIO_INTR_LOW_LEVEL);
        gpio_intr_enable(irq->pin);
    } else {
        gpio_intr_disable(irq->pin);
        gpio_wakeup_disable(irq->pin);
    }
}

/* ---- LED and button ------------------------------------------------------- */

void board_led(bool on)
{
    gpio_set_level(PIN_LED, on ? 1 : 0);
}

void board_led_blink(int times, int on_ms, int off_ms)
{
    for (int i = 0; i < times; i++) {
        board_led(true);
        vTaskDelay(pdMS_TO_TICKS(on_ms));
        board_led(false);
        if (i + 1 < times) vTaskDelay(pdMS_TO_TICKS(off_ms));
    }
}

bool board_button_pressed(void)
{
    return gpio_get_level(PIN_BUTTON) == 0;
}

void board_button_attach(TaskHandle_t task)
{
    s_button_irq.task = task;
    irq_enable(&s_button_irq, true, false);
}

void board_button_rearm(void)
{
    irq_enable(&s_button_irq, true, false);
}

#if CONFIG_LAUNDRY_BOARD_PCB

/* ---- LIS2DW12 on SPI -------------------------------------------------------- */

static spi_device_handle_t s_spi;
static irq_t s_wake_irq, s_fifo_irq;

static bool spi_read(void *ctx, uint8_t reg, uint8_t *buf, size_t len)
{
    (void)ctx;
    spi_transaction_t t = {
        .addr = reg | 0x80,
        .length = len * 8,
        .rxlength = len * 8,
        .rx_buffer = buf,
    };
    return spi_device_polling_transmit(s_spi, &t) == ESP_OK;
}

static bool spi_write(void *ctx, uint8_t reg, uint8_t value)
{
    (void)ctx;
    spi_transaction_t t = {
        .flags = SPI_TRANS_USE_TXDATA,
        .addr = reg,
        .length = 8,
        .tx_data = { value },
    };
    return spi_device_polling_transmit(s_spi, &t) == ESP_OK;
}

/* At least ms: a delay of n ticks can end just after the next tick, so add one. */
static void delay_ms(uint32_t ms)
{
    vTaskDelay(pdMS_TO_TICKS(ms) + 1);
}

static bool accel_init(void)
{
    spi_bus_config_t bus = {
        .mosi_io_num = PIN_ACC_MOSI,
        .miso_io_num = PIN_ACC_MISO,
        .sclk_io_num = PIN_ACC_SCLK,
        .quadwp_io_num = -1,
        .quadhd_io_num = -1,
    };
    spi_device_interface_config_t dev = {
        .address_bits = 8,
        .mode = 3,                     /* CPOL = 1, CPHA = 1 (DS11811 section 6.2) */
        .clock_speed_hz = 1000000,
        .spics_io_num = PIN_ACC_CS,
        .queue_size = 1,
    };
    if (spi_bus_initialize(SPI2_HOST, &bus, SPI_DMA_DISABLED) != ESP_OK ||
        spi_bus_add_device(SPI2_HOST, &dev, &s_spi) != ESP_OK) {
        ESP_LOGE(TAG, "SPI init failed");
        return false;
    }
    static const lis2dw12_bus_t lis_bus = { spi_read, spi_write, delay_ms, NULL };
    if (!lis2dw12_init(&lis_bus)) {
        ESP_LOGE(TAG, "LIS2DW12 not found");
        return false;
    }
    irq_setup(&s_wake_irq, PIN_ACC_INT1, BOARD_NOTIFY_ACCEL_WAKE, true, false);
    irq_setup(&s_fifo_irq, PIN_ACC_INT2, BOARD_NOTIFY_ACCEL_FIFO, true, false);
    return true;
}

void board_accel_attach(TaskHandle_t task)
{
    s_wake_irq.task = task;
    s_fifo_irq.task = task;
}

bool board_accel_arm(uint16_t threshold_mg)
{
    irq_enable(&s_fifo_irq, false, true);
    bool ok = lis2dw12_arm_wakeup(threshold_mg);
    irq_enable(&s_wake_irq, true, true);
    return ok;
}

bool board_accel_stream(void)
{
    irq_enable(&s_wake_irq, false, true);
    bool ok = lis2dw12_start_stream(BOARD_ACCEL_BATCH);
    irq_enable(&s_fifo_irq, true, true);
    return ok;
}

int board_accel_read(vm_sample_t *out, int max)
{
    lis2dw12_sample_t raw[LIS2DW12_FIFO_DEPTH];
    if (max > LIS2DW12_FIFO_DEPTH) max = LIS2DW12_FIFO_DEPTH;
    int n = lis2dw12_read_fifo(raw, max);
    for (int i = 0; i < n; i++) {
        out[i].x_mg = raw[i].x_mg;
        out[i].y_mg = raw[i].y_mg;
        out[i].z_mg = raw[i].z_mg;
    }
    gpio_intr_enable(PIN_ACC_INT2);
    return n;
}

void board_accel_ack_wake(void)
{
    lis2dw12_clear_interrupts();
    gpio_intr_enable(PIN_ACC_INT1);
}

void board_fake_wash(bool on) { (void)on; }
bool board_fake_washing(void) { return false; }

/* ---- SHT40 on I2C --------------------------------------------------------- */

static i2c_master_dev_handle_t s_sht;

static bool i2c_tx(void *ctx, const uint8_t *buf, size_t len)
{
    (void)ctx;
    return i2c_master_transmit(s_sht, buf, len, 50) == ESP_OK;
}

static bool i2c_rx(void *ctx, uint8_t *buf, size_t len)
{
    (void)ctx;
    return i2c_master_receive(s_sht, buf, len, 50) == ESP_OK;
}

static bool env_init(void)
{
    i2c_master_bus_config_t bus_cfg = {
        .i2c_port = -1,
        .sda_io_num = PIN_I2C_SDA,
        .scl_io_num = PIN_I2C_SCL,
        .clk_source = I2C_CLK_SRC_DEFAULT,
        .glitch_ignore_cnt = 7,
        .flags.enable_internal_pullup = false,   /* 10k on the board */
    };
    i2c_master_bus_handle_t bus;
    i2c_device_config_t dev = {
        .dev_addr_length = I2C_ADDR_BIT_LEN_7,
        .device_address = 0x44,
        .scl_speed_hz = 100000,
    };
    if (i2c_new_master_bus(&bus_cfg, &bus) != ESP_OK ||
        i2c_master_bus_add_device(bus, &dev, &s_sht) != ESP_OK) {
        ESP_LOGE(TAG, "I2C init failed");
        return false;
    }
    static const sht40_bus_t sht_bus = { i2c_tx, i2c_rx, delay_ms, NULL };
    sht40_init(&sht_bus);
    return true;
}

bool board_env_read(sht40_reading_t *out)
{
    return sht40_read(out);
}

/* ---- battery divider on the ADC -------------------------------------------- */

static adc_oneshot_unit_handle_t s_adc;
static adc_cali_handle_t s_cali;
static adc_channel_t s_adc_chan;

static bool battery_init(void)
{
    adc_unit_t unit;
    if (adc_oneshot_io_to_channel(PIN_VBAT_SENSE, &unit, &s_adc_chan) != ESP_OK) return false;
    adc_oneshot_unit_init_cfg_t unit_cfg = { .unit_id = unit };
    adc_oneshot_chan_cfg_t chan_cfg = { .atten = ADC_ATTEN_DB_12, .bitwidth = ADC_BITWIDTH_DEFAULT };
    adc_cali_curve_fitting_config_t cali_cfg = {
        .unit_id = unit, .chan = s_adc_chan, .atten = ADC_ATTEN_DB_12, .bitwidth = ADC_BITWIDTH_DEFAULT,
    };
    if (adc_oneshot_new_unit(&unit_cfg, &s_adc) != ESP_OK ||
        adc_oneshot_config_channel(s_adc, s_adc_chan, &chan_cfg) != ESP_OK ||
        adc_cali_create_scheme_curve_fitting(&cali_cfg, &s_cali) != ESP_OK) {
        ESP_LOGE(TAG, "ADC init failed");
        return false;
    }
    return true;
}

uint32_t board_battery_mv(void)
{
    /* The divider is 10M/10M with 100 nF at the midpoint; the first conversion after a while
     * can read low while the sampling capacitor charges, so it is discarded. */
    int mv = 0, sum = 0;
    if (adc_oneshot_get_calibrated_result(s_adc, s_cali, s_adc_chan, &mv) != ESP_OK) return 0;
    for (int i = 0; i < 4; i++) {
        if (adc_oneshot_get_calibrated_result(s_adc, s_cali, s_adc_chan, &mv) != ESP_OK) return 0;
        sum += mv;
    }
    return battery_mv_from_sense_mv((uint32_t)(sum / 4));
}

#else /* CONFIG_LAUNDRY_BOARD_FEATHER */

/* ---- simulated sensors ------------------------------------------------------ */

static TaskHandle_t s_accel_task;
static esp_timer_handle_t s_fifo_timer;
static bool s_streaming;

static void fifo_tick(void *arg)
{
    (void)arg;
    if (s_accel_task) xTaskNotify(s_accel_task, BOARD_NOTIFY_ACCEL_FIFO, eSetBits);
}

static bool accel_init(void)
{
    const esp_timer_create_args_t args = { .callback = fifo_tick, .name = "fake_fifo" };
    return esp_timer_create(&args, &s_fifo_timer) == ESP_OK;
}

void board_accel_attach(TaskHandle_t task)
{
    s_accel_task = task;
}

bool board_accel_arm(uint16_t threshold_mg)
{
    (void)threshold_mg;
    s_streaming = false;
    esp_timer_stop(s_fifo_timer);
    if (fake_accel_washing() && s_accel_task) {
        xTaskNotify(s_accel_task, BOARD_NOTIFY_ACCEL_WAKE, eSetBits);
    }
    return true;
}

bool board_accel_stream(void)
{
    s_streaming = true;
    esp_timer_stop(s_fifo_timer);
    return esp_timer_start_periodic(s_fifo_timer, 1000000ULL * BOARD_ACCEL_BATCH / BOARD_ACCEL_HZ)
           == ESP_OK;
}

int board_accel_read(vm_sample_t *out, int max)
{
    int n = max < BOARD_ACCEL_BATCH ? max : BOARD_ACCEL_BATCH;
    fake_accel_generate(out, n, BOARD_ACCEL_HZ);
    return n;
}

void board_accel_ack_wake(void) {}

void board_fake_wash(bool on)
{
    fake_accel_set_washing(on);
    if (on && !s_streaming && s_accel_task) {
        xTaskNotify(s_accel_task, BOARD_NOTIFY_ACCEL_WAKE, eSetBits);
    }
}

bool board_fake_washing(void)
{
    return fake_accel_washing();
}

static bool env_init(void) { return true; }

bool board_env_read(sht40_reading_t *out)
{
    out->temperature_c = 21.5f;
    out->humidity_pct = 45.0f;
    return true;
}

static bool battery_init(void) { return true; }

uint32_t board_battery_mv(void)
{
    return 3000;
}

#endif

/* ---- init ------------------------------------------------------------------- */

bool board_init(void)
{
    gpio_config_t led = {
        .pin_bit_mask = 1ULL << PIN_LED,
        .mode = GPIO_MODE_OUTPUT,
    };
    gpio_config(&led);
    board_led(false);

    gpio_install_isr_service(0);
    esp_sleep_enable_gpio_wakeup();
#if CONFIG_LAUNDRY_BOARD_PCB
    irq_setup(&s_button_irq, PIN_BUTTON, BOARD_NOTIFY_BUTTON, false, false);
#else
    irq_setup(&s_button_irq, PIN_BUTTON, BOARD_NOTIFY_BUTTON, false, true);
#endif

    bool ok = accel_init();
    ok = env_init() && ok;
    ok = battery_init() && ok;
    return ok;
}
