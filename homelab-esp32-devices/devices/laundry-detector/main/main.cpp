extern "C" {
#include <string.h>
#include <stdio.h>
#include <math.h>
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "esp_log.h"
#include "esp_sleep.h"
#include "driver/gpio.h"
#include "nvs_flash.h"
#include "lis3dh.h"
#include "max17048.h"
#include "sht40.h"
#include "boot_action.h"
#include "vibration_monitor.h"
#include "status_led.h"
}

#include <esp_matter.h>
#include <esp_matter_endpoint.h>
#include <esp_openthread_types.h>
#include <platform/ESP32/OpenthreadLauncher.h>
#include <platform/CHIPDeviceLayer.h>
#include <platform/ThreadStackManager.h>
#include <setup_payload/OnboardingCodesUtil.h>
#include <app/server/Dnssd.h>
#include <app/server/Server.h>

static const char *TAG = "laundry";

/* ---- Pin definitions (Adafruit ESP32-C6 Feather) ----------------------- */
#define I2C_SDA_GPIO     19
#define I2C_SCL_GPIO     18
#define NEOPIXEL_GPIO     9
#define NEOPIXEL_PWR_GPIO 20
#define LIS3DH_INT1_GPIO  7

/* ---- Configurable via Kconfig ------------------------------------------ */
#ifndef CONFIG_VIBRATION_THRESHOLD_MG
#define CONFIG_VIBRATION_THRESHOLD_MG 90
#endif
#ifndef CONFIG_BUCKET_THRESHOLD
#define CONFIG_BUCKET_THRESHOLD 15
#endif
#ifndef CONFIG_QUIET_TIMEOUT_SEC
#define CONFIG_QUIET_TIMEOUT_SEC 120
#endif
#ifndef CONFIG_REPORT_INTERVAL_SEC
#define CONFIG_REPORT_INTERVAL_SEC 60
#endif
#ifndef CONFIG_SLEEP_TIMER_MIN
#define CONFIG_SLEEP_TIMER_MIN 30
#endif

#define SAMPLE_INTERVAL_MS 10  /* 100 Hz sampling */
#define LED_DIM 24
#define STARTUP_DISPLAY_MS 15000

using namespace esp_matter;
using namespace chip::app::Clusters;

static uint16_t s_washer_endpoint_id = 0;
static bool s_was_running = false; /* track Running→Stopped transitions */
static bool s_commissioned = false;
static uint8_t s_fabric_count = 0;  /* debug: fabric count at boot */


/* -------------------------------------------------------------------------- */
/* OperationalState delegate                                                  */
/* -------------------------------------------------------------------------- */

#include <app/clusters/operational-state-server/operational-state-server.h>

class LaundryOperationalStateDelegate : public OperationalState::Delegate {
public:
    LaundryOperationalStateDelegate() = default;

    OperationalState::Instance *get_instance() { return GetInstance(); }

    chip::app::DataModel::Nullable<uint32_t> GetCountdownTime() override {
        return chip::app::DataModel::Nullable<uint32_t>();
    }

    CHIP_ERROR GetOperationalStateAtIndex(size_t index,
            OperationalState::GenericOperationalState &state) override {
        /* Two states: Stopped(0) and Running(1) */
        if (index == 0) {
            state.Set(chip::to_underlying(OperationalState::OperationalStateEnum::kStopped));
            return CHIP_NO_ERROR;
        }
        if (index == 1) {
            state.Set(chip::to_underlying(OperationalState::OperationalStateEnum::kRunning));
            return CHIP_NO_ERROR;
        }
        return CHIP_ERROR_NOT_FOUND;
    }

    CHIP_ERROR GetOperationalPhaseAtIndex(size_t index,
            chip::MutableCharSpan &phase) override {
        return CHIP_ERROR_NOT_FOUND;
    }

    void HandlePauseStateCallback(OperationalState::GenericOperationalError &err) override {
        err.Set(chip::to_underlying(OperationalState::ErrorStateEnum::kCommandInvalidInState));
    }
    void HandleResumeStateCallback(OperationalState::GenericOperationalError &err) override {
        err.Set(chip::to_underlying(OperationalState::ErrorStateEnum::kCommandInvalidInState));
    }
    void HandleStartStateCallback(OperationalState::GenericOperationalError &err) override {
        err.Set(chip::to_underlying(OperationalState::ErrorStateEnum::kCommandInvalidInState));
    }
    void HandleStopStateCallback(OperationalState::GenericOperationalError &err) override {
        err.Set(chip::to_underlying(OperationalState::ErrorStateEnum::kCommandInvalidInState));
    }
};

static LaundryOperationalStateDelegate s_opstate_delegate;

/* -------------------------------------------------------------------------- */
/* Deep sleep helpers                                                         */
/* -------------------------------------------------------------------------- */

static void enter_deep_sleep(void)
{
    ESP_LOGI(TAG, "Entering deep sleep (timer=%d min, GPIO=%d)",
             CONFIG_SLEEP_TIMER_MIN, LIS3DH_INT1_GPIO);

    /* Configure LIS3DH to generate interrupt on motion */
    lis3dh_configure_motion_interrupt(CONFIG_VIBRATION_THRESHOLD_MG, 10);

    status_led_set(STATUS_SLEEPING);

    /* Debug: read GPIO state before sleep */
    int gpio_level = gpio_get_level((gpio_num_t)LIS3DH_INT1_GPIO);
    ESP_LOGI(TAG, "GPIO%d level before sleep: %d", LIS3DH_INT1_GPIO, gpio_level);

    /* Small delay to let interrupt settle after config */
    vTaskDelay(pdMS_TO_TICKS(100));
    gpio_level = gpio_get_level((gpio_num_t)LIS3DH_INT1_GPIO);
    ESP_LOGI(TAG, "GPIO%d level after 100ms settle: %d", LIS3DH_INT1_GPIO, gpio_level);

    /* Enable timer wakeup */
    esp_sleep_enable_timer_wakeup((uint64_t)CONFIG_SLEEP_TIMER_MIN * 60 * 1000000ULL);

    /* Enable GPIO wakeup from LIS3DH INT1 (LP GPIO for deep sleep on C6) */
    esp_deep_sleep_enable_gpio_wakeup(1ULL << LIS3DH_INT1_GPIO,
                                       ESP_GPIO_WAKEUP_GPIO_HIGH);

    esp_deep_sleep_start();
}

/* -------------------------------------------------------------------------- */
/* I2C sensor initialization                                                  */
/* -------------------------------------------------------------------------- */

static bool init_sensors(void)
{
    lis3dh_config_t lis_cfg = {
        .sda_gpio = I2C_SDA_GPIO,
        .scl_gpio = I2C_SCL_GPIO,
        .addr = 0x18,
    };
    if (!lis3dh_init(&lis_cfg)) {
        ESP_LOGE(TAG, "LIS3DH init failed!");
        return false;
    }
    ESP_LOGI(TAG, "LIS3DH initialized");

    max17048_config_t bat_cfg = {
        .sda_gpio = I2C_SDA_GPIO,
        .scl_gpio = I2C_SCL_GPIO,
        .i2c_bus = lis3dh_get_i2c_bus(),
    };
    if (!max17048_init(&bat_cfg)) {
        ESP_LOGE(TAG, "MAX17048 init failed!");
        return false;
    }
    ESP_LOGI(TAG, "MAX17048 initialized");

    sht40_config_t sht_cfg = {
        .sda_gpio = I2C_SDA_GPIO,
        .scl_gpio = I2C_SCL_GPIO,
        .i2c_bus = lis3dh_get_i2c_bus(),
    };
    if (!sht40_init(&sht_cfg)) {
        ESP_LOGE(TAG, "SHT40 init failed!");
        return false;
    }
    ESP_LOGI(TAG, "SHT40 initialized");
    return true;
}

/* -------------------------------------------------------------------------- */
/* Matter callbacks                                                           */
/* -------------------------------------------------------------------------- */

static esp_err_t app_attribute_update_cb(
    attribute::callback_type_t type,
    uint16_t endpoint_id, uint32_t cluster_id,
    uint32_t attribute_id, esp_matter_attr_val_t *val, void *priv_data)
{
    return ESP_OK;
}

static esp_err_t app_identification_cb(
    identification::callback_type_t type,
    uint16_t endpoint_id, uint8_t effect_id, uint8_t effect_variant,
    void *priv_data)
{
    ESP_LOGI(TAG, "Identify: endpoint=%u effect=%u", endpoint_id, effect_id);
    for (int i = 0; i < 3; i++) {
        status_led_set(STATUS_STARTUP);
        vTaskDelay(pdMS_TO_TICKS(300));
        status_led_set(STATUS_OFF);
        vTaskDelay(pdMS_TO_TICKS(300));
    }
    return ESP_OK;
}

static void app_event_cb(const ChipDeviceEvent *event, intptr_t arg)
{
    switch (event->Type) {
    case chip::DeviceLayer::DeviceEventType::kCommissioningComplete:
        ESP_LOGI(TAG, "Commissioning complete");
        s_commissioned = true;
        status_led_set(STATUS_CONNECTED);
        break;
    case chip::DeviceLayer::DeviceEventType::kFabricRemoved:
        ESP_LOGI(TAG, "Fabric removed");
        status_led_set(STATUS_STARTUP); /* amber-ish */
        break;
    case chip::DeviceLayer::DeviceEventType::kThreadStateChange:
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
/* Matter attribute update helpers                                            */
/* -------------------------------------------------------------------------- */

static void update_operational_state(bool is_running)
{
    /* OperationalState: 0=Stopped, 1=Running */
    uint8_t state = is_running
        ? chip::to_underlying(OperationalState::OperationalStateEnum::kRunning)
        : chip::to_underlying(OperationalState::OperationalStateEnum::kStopped);

    ESP_LOGI(TAG, "update_operational_state: state=%u (%s)",
             state, is_running ? "Running" : "Stopped");

    /* Must hold the CHIP stack lock when calling into CHIP cluster APIs
     * from a FreeRTOS task (monitor_task runs outside the CHIP thread). */
    chip::DeviceLayer::StackLock lock;

    OperationalState::Instance *inst = s_opstate_delegate.get_instance();
    if (inst) {
        CHIP_ERROR err = inst->SetOperationalState(state);
        if (err != CHIP_NO_ERROR) {
            ESP_LOGE(TAG, "SetOperationalState failed: %" CHIP_ERROR_FORMAT, err.Format());
        }
    } else {
        ESP_LOGW(TAG, "OperationalState instance not initialized");
    }

    /* Emit OperationCompletion event on Running→Stopped transition */
    if (s_was_running && !is_running) {
        ESP_LOGI(TAG, "Laundry cycle complete — emitting OperationCompletion event");
        /* TODO: emit OperationalState::Events::OperationCompletion once
         * esp_matter event send API is confirmed */
    }
    s_was_running = is_running;
}

static void update_battery(void)
{
    uint8_t soc = 0;
    if (max17048_read_soc(&soc)) {
        /* Matter BatPercentRemaining: 0-200 in 0.5% steps */
        esp_matter_attr_val_t val = esp_matter_nullable_uint8(soc * 2);
        esp_err_t err = attribute::update(
            s_washer_endpoint_id,
            PowerSource::Id,
            PowerSource::Attributes::BatPercentRemaining::Id,
            &val);
        if (err != ESP_OK) {
            ESP_LOGE(TAG, "BatPercentRemaining update failed: %d", err);
        }
    }
}

static void update_temp_humidity(void)
{
    sht40_reading_t reading;
    if (!sht40_read(&reading)) return;

    /* TemperatureMeasurement: int16 in 0.01°C units */
    int16_t matter_temp = (int16_t)(reading.temperature_c * 100);
    esp_matter_attr_val_t tval = esp_matter_nullable_int16(matter_temp);
    esp_err_t err = attribute::update(
        s_washer_endpoint_id,
        TemperatureMeasurement::Id,
        TemperatureMeasurement::Attributes::MeasuredValue::Id,
        &tval);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "Temperature update failed: %d", err);
    }

    /* RelativeHumidityMeasurement: uint16 in 0.01% units */
    uint16_t matter_hum = (uint16_t)(reading.humidity_pct * 100);
    esp_matter_attr_val_t hval = esp_matter_nullable_uint16(matter_hum);
    err = attribute::update(
        s_washer_endpoint_id,
        RelativeHumidityMeasurement::Id,
        RelativeHumidityMeasurement::Attributes::MeasuredValue::Id,
        &hval);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "Humidity update failed: %d", err);
    }

    ESP_LOGI(TAG, "T=%.1f°C RH=%.1f%%", reading.temperature_c, reading.humidity_pct);
}

/* -------------------------------------------------------------------------- */
/* Active monitoring task                                                     */
/* -------------------------------------------------------------------------- */

static void monitor_task(void *arg)
{
    int log_counter = 0;
    bool last_running = false;

    while (1) {
        lis3dh_accel_t accel;
        if (lis3dh_read_accel(&accel)) {
            float mag = sqrtf((float)(accel.x_mg * accel.x_mg +
                                      accel.y_mg * accel.y_mg +
                                      accel.z_mg * accel.z_mg));
            int16_t vibration = (int16_t)(mag - 1000.0f);
            if (vibration < 0) vibration = 0;

            vibration_monitor_feed_sample((uint16_t)vibration);

            /* Update LED: cyan when vibrating, dark blue when quiet */
            status_led_set(vibration > CONFIG_VIBRATION_THRESHOLD_MG
                           ? STATUS_ACTIVE_VIBRATING : STATUS_ACTIVE);

            if (++log_counter >= 100) {
                log_counter = 0;
                uint8_t bat_pct = 0;
                max17048_read_soc(&bat_pct);
                uint16_t active = vibration_monitor_get_active_buckets();
                ESP_LOGI(TAG, "vib=%d buckets=%u/%d bat=%d%%",
                         vibration, active, VM_WINDOW_SECONDS, bat_pct);
            }
        }

        vibration_monitor_tick(SAMPLE_INTERVAL_MS);

        /* Check for state transitions — report immediately on change */
        bool running = vibration_monitor_is_running();
        if (running != last_running) {
            ESP_LOGI(TAG, "=== STATE CHANGE: %s -> %s (buckets=%u/%d) ===",
                     last_running ? "RUNNING" : "STOPPED",
                     running ? "RUNNING" : "STOPPED",
                     vibration_monitor_get_active_buckets(), VM_WINDOW_SECONDS);
            last_running = running;
            update_operational_state(running);
            update_battery();
            update_temp_humidity();
        }

        /* Periodic report on timer */
        if (vibration_monitor_report_ready()) {
            uint16_t active = vibration_monitor_get_active_buckets();
            update_operational_state(running);
            update_battery();
            update_temp_humidity();

            ESP_LOGI(TAG, "=== REPORT: running=%s buckets=%u/%d threshold=%d ===",
                     running ? "YES" : "NO", active, VM_WINDOW_SECONDS,
                     CONFIG_BUCKET_THRESHOLD);
        }

        if (vibration_monitor_should_sleep()) {
            ESP_LOGI(TAG, "Quiet timeout reached, going to sleep");
            update_operational_state(false);
            enter_deep_sleep();
        }

        vTaskDelay(pdMS_TO_TICKS(SAMPLE_INTERVAL_MS));
    }
}

/* -------------------------------------------------------------------------- */
/* Thread credential provisioning from NVS                                    */
/* -------------------------------------------------------------------------- */

static bool s_thread_provisioned = false;

/**
 * Read pre-provisioned Thread dataset TLV from NVS (written by flash.py --thread)
 * and provision the Thread stack so the device joins the network.
 *
 * Must be called AFTER esp_matter::start() (which initializes OpenThread).
 */
static void provision_thread_from_nvs(void)
{
    nvs_handle_t nvs;
    esp_err_t err = nvs_open("thread", NVS_READONLY, &nvs);
    if (err != ESP_OK) {
        ESP_LOGI(TAG, "No thread NVS namespace — skipping Thread provisioning");
        return;
    }

    /* First call with NULL to get the required length */
    size_t len = 0;
    err = nvs_get_blob(nvs, "dataset_tlv", NULL, &len);
    if (err != ESP_OK || len == 0) {
        ESP_LOGI(TAG, "No dataset_tlv in NVS — skipping Thread provisioning");
        nvs_close(nvs);
        return;
    }

    uint8_t *tlv = (uint8_t *)malloc(len);
    if (!tlv) {
        ESP_LOGE(TAG, "Failed to allocate %u bytes for Thread dataset", (unsigned)len);
        nvs_close(nvs);
        return;
    }

    err = nvs_get_blob(nvs, "dataset_tlv", tlv, &len);
    nvs_close(nvs);

    if (err != ESP_OK) {
        ESP_LOGE(TAG, "Failed to read dataset_tlv from NVS: %d", err);
        free(tlv);
        return;
    }

    ESP_LOGI(TAG, "Found Thread dataset in NVS (%u bytes), provisioning...", (unsigned)len);

    CHIP_ERROR chipErr = chip::DeviceLayer::ThreadStackMgr().SetThreadProvision(
        chip::ByteSpan(tlv, len));
    if (chipErr != CHIP_NO_ERROR) {
        ESP_LOGE(TAG, "SetThreadProvision failed: %" CHIP_ERROR_FORMAT, chipErr.Format());
        free(tlv);
        return;
    }

    chipErr = chip::DeviceLayer::ThreadStackMgr().SetThreadEnabled(true);
    free(tlv);

    if (chipErr != CHIP_NO_ERROR) {
        ESP_LOGE(TAG, "SetThreadEnabled failed: %" CHIP_ERROR_FORMAT, chipErr.Format());
        return;
    }

    s_thread_provisioned = true;
    ESP_LOGI(TAG, "Thread provisioned from NVS — device will join network");
}

/* -------------------------------------------------------------------------- */
/* app_main                                                                   */
/* -------------------------------------------------------------------------- */

extern "C" void app_main(void)
{
    /* 1. NeoPixel init */
    status_led_init(NEOPIXEL_GPIO, NEOPIXEL_PWR_GPIO);

    ESP_LOGI(TAG, "Laundry detector (Matter) starting");

    /* 2. Check wakeup cause — set LED immediately so user sees the right
     *    color during the multi-second Matter init that follows. */
    esp_sleep_source_t wakeup = esp_sleep_get_wakeup_cause();
    switch (wakeup) {
    case ESP_SLEEP_WAKEUP_TIMER:
        ESP_LOGI(TAG, "Woke from timer");
        status_led_set(STATUS_STARTUP);
        break;
    case ESP_SLEEP_WAKEUP_GPIO:
        ESP_LOGI(TAG, "Woke from GPIO (vibration interrupt)");
        status_led_set(STATUS_ACTIVE);
        break;
    default:
        ESP_LOGI(TAG, "Fresh boot / reset");
        status_led_set(STATUS_STARTUP);
        break;
    }

    /* 3. NVS */
    esp_err_t ret = nvs_flash_init();
    if (ret == ESP_ERR_NVS_NO_FREE_PAGES || ret == ESP_ERR_NVS_NEW_VERSION_FOUND) {
        ESP_ERROR_CHECK(nvs_flash_erase());
        ret = nvs_flash_init();
    }
    ESP_ERROR_CHECK(ret);

    /* 4. Init sensors */
    if (!init_sensors()) {
        status_led_set(STATUS_BATTERY_LOW);
        return;
    }

    /* 5. Init vibration monitor */
    vibration_monitor_config_t vm_cfg = {
        .threshold_mg = CONFIG_VIBRATION_THRESHOLD_MG,
        .quiet_timeout_ms = (uint32_t)CONFIG_QUIET_TIMEOUT_SEC * 1000,
        .report_interval_ms = (uint32_t)CONFIG_REPORT_INTERVAL_SEC * 1000,
        .bucket_threshold = CONFIG_BUCKET_THRESHOLD,
    };
    vibration_monitor_init(&vm_cfg);

    /* 6. Create Matter node */
    node::config_t node_config;
    node_t *node = node::create(&node_config,
                                app_attribute_update_cb,
                                app_identification_cb);
    if (!node) {
        ESP_LOGE(TAG, "Matter node creation failed");
        status_led_set(STATUS_BATTERY_LOW);
        return;
    }

    /* 7. Create laundry washer endpoint with OperationalState cluster */
    endpoint::laundry_washer::config_t washer_cfg;
    washer_cfg.operational_state.delegate = &s_opstate_delegate;
    endpoint_t *ep = endpoint::laundry_washer::create(
        node, &washer_cfg, ENDPOINT_FLAG_NONE, NULL);
    if (!ep) {
        ESP_LOGE(TAG, "Laundry washer endpoint creation failed");
        status_led_set(STATUS_BATTERY_LOW);
        return;
    }
    s_washer_endpoint_id = endpoint::get_id(ep);
    ESP_LOGI(TAG, "Laundry washer endpoint id=%u", s_washer_endpoint_id);

    /* 8. Add PowerSource cluster (battery-powered device) */
    cluster::power_source::config_t ps_cfg;
    ps_cfg.feature_flags = static_cast<uint32_t>(PowerSource::Feature::kBattery);
    cluster_t *ps = cluster::power_source::create(ep, &ps_cfg, CLUSTER_FLAG_SERVER);
    if (!ps) {
        ESP_LOGW(TAG, "PowerSource cluster creation failed (non-fatal)");
    } else {
        /* BatPercentRemaining isn't created by power_source::create —
         * add it explicitly so we can update it at runtime. */
        cluster::power_source::attribute::create_bat_percent_remaining(
            ps, nullable<uint8_t>(100), nullable<uint8_t>(0), nullable<uint8_t>(200));
    }

    /* 9. Add TemperatureMeasurement cluster */
    cluster::temperature_measurement::config_t temp_cfg;
    temp_cfg.measured_value = nullable<int16_t>(0);
    temp_cfg.min_measured_value = nullable<int16_t>(-4000);
    temp_cfg.max_measured_value = nullable<int16_t>(12500);
    cluster_t *tc = cluster::temperature_measurement::create(ep, &temp_cfg, CLUSTER_FLAG_SERVER);
    if (!tc) {
        ESP_LOGW(TAG, "TemperatureMeasurement cluster creation failed (non-fatal)");
    }

    /* 10. Add RelativeHumidityMeasurement cluster */
    cluster::relative_humidity_measurement::config_t rh_cfg;
    rh_cfg.measured_value = nullable<uint16_t>(0);
    rh_cfg.min_measured_value = nullable<uint16_t>(0);
    rh_cfg.max_measured_value = nullable<uint16_t>(10000);
    cluster_t *rh = cluster::relative_humidity_measurement::create(ep, &rh_cfg, CLUSTER_FLAG_SERVER);
    if (!rh) {
        ESP_LOGW(TAG, "RelativeHumidityMeasurement cluster creation failed (non-fatal)");
    }

    /* 11. Configure OpenThread platform for native 802.15.4 radio */
    esp_openthread_platform_config_t ot_config = {};
    ot_config.radio_config.radio_mode = RADIO_MODE_NATIVE;
    ot_config.host_config.host_connection_mode = HOST_CONNECTION_MODE_NONE;
    ot_config.port_config.storage_partition_name = "nvs";
    ot_config.port_config.netif_queue_size = 10;
    ot_config.port_config.task_queue_size = 10;
    set_openthread_platform_config(&ot_config);

    /* 12. Start Matter stack */
    ret = esp_matter::start(app_event_cb);
    if (ret != ESP_OK) {
        ESP_LOGE(TAG, "esp_matter::start failed: %d", ret);
        status_led_set(STATUS_BATTERY_LOW);
        return;
    }
    ESP_LOGI(TAG, "Matter stack started");

    /* 12a. Provision Thread from NVS if flash.py --thread was used */
    provision_thread_from_nvs();

    PrintOnboardingCodes(chip::RendezvousInformationFlags(
        chip::RendezvousInformationFlag::kOnNetwork));

    /* Check if already commissioned (has fabrics) */
    {
        auto & fabricTable = chip::Server::GetInstance().GetFabricTable();
        s_fabric_count = fabricTable.FabricCount();
        s_commissioned = (s_fabric_count > 0);
        ESP_LOGI(TAG, "Commissioned: %s (fabrics=%u)",
                 s_commissioned ? "YES" : "NO", s_fabric_count);
    }

    /* 13. Determine boot action */
    boot_action_t action = determine_boot_action(
        (wakeup_cause_t)wakeup, s_commissioned);
    ESP_LOGI(TAG, "Boot action: %d (wakeup=%d commissioned=%d)",
             action, wakeup, s_commissioned);

    switch (action) {
    case BOOT_ACTION_WAIT_FOR_COMMISSION: {
        ESP_LOGI(TAG, "Not commissioned — staying awake indefinitely for pairing");
        status_led_set(STATUS_UNCOMMISSIONED);

        /* Print sensor readings every 10s while waiting for commissioning.
         * Stay awake forever — do not deep sleep until commissioned. */
        const uint32_t poll_ms = 10000;
        while (!s_commissioned) {
            vTaskDelay(pdMS_TO_TICKS(poll_ms));

            /* Periodic sensor readout for validation */
            uint8_t bat = 0;
            max17048_read_soc(&bat);
            sht40_reading_t env;
            if (sht40_read(&env)) {
                ESP_LOGI(TAG, "[commission-wait] T=%.1f°C RH=%.1f%% bat=%d%%",
                         env.temperature_c, env.humidity_pct, bat);
            } else {
                ESP_LOGI(TAG, "[commission-wait] SHT40 read failed, bat=%d%%", bat);
            }
        }

        ESP_LOGI(TAG, "Commissioned during wait — reporting and sleeping");
        update_battery();
        update_temp_humidity();
        update_operational_state(false);
        vTaskDelay(pdMS_TO_TICKS(3000));
        enter_deep_sleep();
        return;
    }

    case BOOT_ACTION_TIMER_REPORT:
        ESP_LOGI(TAG, "Timer wake — reporting and sleeping");
        status_led_set(STATUS_STARTUP);
        vTaskDelay(pdMS_TO_TICKS(2000));
        update_battery();
        update_temp_humidity();
        update_operational_state(false);
        vTaskDelay(pdMS_TO_TICKS(3000));
        enter_deep_sleep();
        return;

    case BOOT_ACTION_ACTIVE_MONITOR:
        ESP_LOGI(TAG, "Entering active monitoring mode");
        vibration_monitor_notify_wake();
        status_led_set(STATUS_ACTIVE);
        xTaskCreate(monitor_task, "monitor", 4096, NULL, 5, NULL);
        return;

    case BOOT_ACTION_STARTUP_SLEEP:
    default: {
        ESP_LOGI(TAG, "Fresh boot — checking for vibration before sleeping");
        uint8_t bat_pct = 0;
        max17048_read_soc(&bat_pct);
        ESP_LOGI(TAG, "Battery: %d%%", bat_pct);

        if (bat_pct < 10) {
            status_led_set(STATUS_BATTERY_LOW);
        } else {
            status_led_set(STATUS_STARTUP);
        }

        /* Sample accelerometer during startup window.  If vibration is
         * detected, skip sleep and enter active monitoring instead. */
        bool vibration_seen = false;
        const int check_interval_ms = 100;
        const int checks = STARTUP_DISPLAY_MS / check_interval_ms;
        for (int i = 0; i < checks; i++) {
            vTaskDelay(pdMS_TO_TICKS(check_interval_ms));
            lis3dh_accel_t accel;
            if (lis3dh_read_accel(&accel)) {
                float mag = sqrtf((float)(accel.x_mg * accel.x_mg +
                                          accel.y_mg * accel.y_mg +
                                          accel.z_mg * accel.z_mg));
                int16_t vib = (int16_t)(mag - 1000.0f);
                if (vib < 0) vib = 0;
                if (vib >= CONFIG_VIBRATION_THRESHOLD_MG) {
                    vibration_seen = true;
                    break;
                }
            }
        }

        if (vibration_seen) {
            ESP_LOGI(TAG, "Vibration detected during startup — entering active monitoring");
            vibration_monitor_notify_wake();
            status_led_set(STATUS_ACTIVE);
            /* Report initial state to HA */
            update_battery();
            update_temp_humidity();
            update_operational_state(false);
            xTaskCreate(monitor_task, "monitor", 4096, NULL, 5, NULL);
            return;
        }

        ESP_LOGI(TAG, "No vibration during startup — sleeping (action=%d wakeup=%d commissioned=%d fabrics=%d)",
                 action, wakeup, s_commissioned, s_fabric_count);
        /* Report sensor values before sleeping */
        update_battery();
        update_temp_humidity();
        update_operational_state(false);
        vTaskDelay(pdMS_TO_TICKS(2000));
        enter_deep_sleep();
        return;
    }
    }
}
