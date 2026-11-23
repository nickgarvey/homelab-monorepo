/*
 * Laundry sensor: reports whether the washer is running, plus temperature, humidity and battery,
 * as a Matter-over-Thread sleepy end device (short-idle-time ICD). The chip light-sleeps between
 * Thread polls; the accelerometer's wake-up interrupt starts a monitoring session, during which
 * the chip wakes for each FIFO batch (twice a second) until the washer has stopped and the
 * last two minutes were quiet.
 */
extern "C" {
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "esp_log.h"
#include "esp_pm.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "nvs_flash.h"

#include "battery.h"
#include "board.h"
#include "vibration_monitor.h"
}

#include <app/clusters/operational-state-server/operational-state-server.h>
#include <app/server/Dnssd.h>
#include <app/server/Server.h>
#include <clusters/relative_humidity_measurement/integration.h>
#include <clusters/temperature_measurement/integration.h>
#include <esp_matter.h>
#include <esp_matter_core.h>
#include <esp_openthread_types.h>
#include <platform/ESP32/OpenthreadLauncher.h>
#include <platform/ThreadStackManager.h>
#include <setup_payload/OnboardingCodesUtil.h>

static const char *TAG = "laundry";

#if CONFIG_LAUNDRY_BOARD_FEATHER
#define BOARD_NAME "Feather, simulated sensors"
#else
#define BOARD_NAME "PCB"
#endif

#define BUTTON_RESET_HOLD_MS 10000

using namespace esp_matter;
using namespace chip::app::Clusters;

static uint16_t s_endpoint_id;
static TaskHandle_t s_accel_task;
static TaskHandle_t s_button_task;
static volatile bool s_commissioned;

/* ---- Operational State: Stopped / Running only ------------------------------ */

class WasherStateDelegate : public OperationalState::Delegate {
public:
    OperationalState::Instance *instance() { return GetInstance(); }

    chip::app::DataModel::Nullable<uint32_t> GetCountdownTime() override
    {
        return chip::app::DataModel::Nullable<uint32_t>();
    }

    CHIP_ERROR GetOperationalStateAtIndex(size_t index, OperationalState::GenericOperationalState &state) override
    {
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

    CHIP_ERROR GetOperationalPhaseAtIndex(size_t index, chip::MutableCharSpan &phase) override
    {
        return CHIP_ERROR_NOT_FOUND;
    }

    /* The washer cannot be controlled from here. */
    void HandlePauseStateCallback(OperationalState::GenericOperationalError &err) override { reject(err); }
    void HandleResumeStateCallback(OperationalState::GenericOperationalError &err) override { reject(err); }
    void HandleStartStateCallback(OperationalState::GenericOperationalError &err) override { reject(err); }
    void HandleStopStateCallback(OperationalState::GenericOperationalError &err) override { reject(err); }

private:
    static void reject(OperationalState::GenericOperationalError &err)
    {
        err.Set(chip::to_underlying(OperationalState::ErrorStateEnum::kCommandInvalidInState));
    }
};

static WasherStateDelegate s_state_delegate;

static void report_running(bool running, bool was_running)
{
    chip::DeviceLayer::StackLock lock;
    OperationalState::Instance *inst = s_state_delegate.instance();
    if (!inst) {
        ESP_LOGW(TAG, "OperationalState not ready");
        return;
    }
    auto state = running ? OperationalState::OperationalStateEnum::kRunning
                         : OperationalState::OperationalStateEnum::kStopped;
    CHIP_ERROR err = inst->SetOperationalState(chip::to_underlying(state));
    if (err != CHIP_NO_ERROR) {
        ESP_LOGE(TAG, "SetOperationalState: %" CHIP_ERROR_FORMAT, err.Format());
    }
    if (was_running && !running) {
        inst->OnOperationCompletionDetected(chip::to_underlying(OperationalState::ErrorStateEnum::kNoError));
    }
}

/* ---- temperature, humidity, battery ---------------------------------------- */

static uint8_t s_battery_pct = 100;

static void report_environment(void)
{
    sht40_reading_t env;
    if (board_env_read(&env)) {
        /* Code-driven clusters in esp-matter 1.6: the cluster object owns MeasuredValue. */
        chip::DeviceLayer::StackLock lock;
        TemperatureMeasurement::SetMeasuredValue(
            s_endpoint_id, chip::app::DataModel::MakeNullable((int16_t)(env.temperature_c * 100)));
        RelativeHumidityMeasurement::SetMeasuredValue(
            s_endpoint_id, chip::app::DataModel::MakeNullable((uint16_t)(env.humidity_pct * 100)));
    } else {
        ESP_LOGW(TAG, "SHT40 read failed");
    }

    uint32_t mv = board_battery_mv();
    if (mv) {
        s_battery_pct = battery_percent(mv);
        esp_matter_attr_val_t pct = esp_matter_nullable_uint8((uint8_t)(s_battery_pct * 2));  /* 0.5% units */
        esp_matter_attr_val_t volts = esp_matter_nullable_uint32(mv);
        esp_matter_attr_val_t level = esp_matter_enum8(battery_level(s_battery_pct));
        attribute::update(s_endpoint_id, PowerSource::Id, PowerSource::Attributes::BatPercentRemaining::Id, &pct);
        attribute::update(s_endpoint_id, PowerSource::Id, PowerSource::Attributes::BatVoltage::Id, &volts);
        attribute::update(s_endpoint_id, PowerSource::Id, PowerSource::Attributes::BatChargeLevel::Id, &level);
    }
    ESP_LOGI(TAG, "T=%.1f C RH=%.1f %% battery=%lu mV (%u %%)", env.temperature_c, env.humidity_pct,
             (unsigned long)mv, s_battery_pct);
}

static void env_task(void *arg)
{
    for (;;) {
        report_environment();
        vTaskDelay(pdMS_TO_TICKS((uint32_t)CONFIG_ENV_REPORT_INTERVAL_MIN * 60 * 1000));
    }
}

/* ---- accelerometer ---------------------------------------------------------- */

static void accel_task(void *arg)
{
    const vibration_monitor_config_t cfg = {
        .threshold_mg = CONFIG_VIBRATION_THRESHOLD_MG,
        .bucket_threshold = CONFIG_BUCKET_THRESHOLD,
        .stop_delay_ms = (uint32_t)CONFIG_STOP_DELAY_MIN * 60 * 1000,
    };
    vibration_monitor_init(&cfg);
    board_accel_attach(xTaskGetCurrentTaskHandle());
    board_accel_arm(CONFIG_VIBRATION_THRESHOLD_MG);

    bool streaming = false;
    bool running = false;
    int batches = 0;
    vm_sample_t batch[32];

    for (;;) {
        uint32_t bits = 0;
        /* While streaming a FIFO batch is due every 0.5 s; time out after 2 s in case an
         * interrupt was missed, so the FIFO is still drained. */
        xTaskNotifyWait(0, UINT32_MAX, &bits, streaming ? pdMS_TO_TICKS(2000) : portMAX_DELAY);

        if (!streaming) {
            if (!(bits & BOARD_NOTIFY_ACCEL_WAKE)) continue;
            board_accel_ack_wake();
            ESP_LOGI(TAG, "vibration: monitoring");
            board_accel_stream();
            streaming = true;
            continue;
        }

        int n = board_accel_read(batch, 32);
        if (n < 0) {
            ESP_LOGE(TAG, "accelerometer read failed");
            continue;
        }
        if (n == 0) continue;
        uint16_t peak = vibration_monitor_batch_peak_mg(batch, n);
        vibration_monitor_feed(peak);
        vibration_monitor_tick((uint32_t)n * 1000 / BOARD_ACCEL_HZ);

        if (++batches % 20 == 0) {   /* every 10 s */
            ESP_LOGI(TAG, "peak=%u mg active=%u/%d running=%d", peak,
                     vibration_monitor_active_buckets(), VM_WINDOW_SECONDS, running);
        }

        bool now = vibration_monitor_is_running();
        if (now != running) {
            ESP_LOGI(TAG, "=== %s (active %u/%d) ===", now ? "RUNNING" : "STOPPED",
                     vibration_monitor_active_buckets(), VM_WINDOW_SECONDS);
            report_running(now, running);
            running = now;
        }

        if (vibration_monitor_is_idle()) {
            ESP_LOGI(TAG, "vibration: quiet, waiting for wake-up");
            board_accel_arm(CONFIG_VIBRATION_THRESHOLD_MG);
            streaming = false;
        }
    }
}

/* ---- button ------------------------------------------------------------------ */

static void button_task(void *arg)
{
    board_button_attach(xTaskGetCurrentTaskHandle());
    for (;;) {
        xTaskNotifyWait(0, UINT32_MAX, NULL, portMAX_DELAY);
        int held = 0;
        while (board_button_pressed() && held < BUTTON_RESET_HOLD_MS) {
            vTaskDelay(pdMS_TO_TICKS(50));
            held += 50;
        }
        if (held >= BUTTON_RESET_HOLD_MS) {
            ESP_LOGW(TAG, "button held: factory reset");
            board_led_blink(10, 50, 50);
            esp_matter::factory_reset();   /* erases fabrics and reboots */
        } else if (held >= 50) {
#if CONFIG_LAUNDRY_BOARD_FEATHER
            bool on = !board_fake_washing();
            ESP_LOGI(TAG, "simulated wash %s", on ? "started" : "stopped");
            board_fake_wash(on);
            board_led_blink(on ? 1 : 2, 200, 200);
#else
            /* Battery check: 1-4 blinks for each quarter of charge left. */
            int blinks = 1 + (s_battery_pct > 25) + (s_battery_pct > 50) + (s_battery_pct > 75);
            ESP_LOGI(TAG, "button: battery %u %%", s_battery_pct);
            board_led_blink(blinks, 200, 300);
#endif
        }
        while (board_button_pressed()) vTaskDelay(pdMS_TO_TICKS(50));
        board_button_rearm();
    }
}

/* Slow blink until commissioned, so a fresh board shows it is waiting to be paired. */
static void pairing_blink_task(void *arg)
{
    while (!s_commissioned) {
        board_led_blink(1, 100, 0);
        vTaskDelay(pdMS_TO_TICKS(3000));
    }
    vTaskDelete(NULL);
}

static void identify_blink_task(void *arg)
{
    board_led_blink(3, 300, 300);
    vTaskDelete(NULL);
}

/* ---- Matter callbacks --------------------------------------------------------- */

static esp_err_t app_attribute_update_cb(attribute::callback_type_t type, uint16_t endpoint_id,
                                         uint32_t cluster_id, uint32_t attribute_id,
                                         esp_matter_attr_val_t *val, void *priv_data)
{
    return ESP_OK;
}

static esp_err_t app_identification_cb(identification::callback_type_t type, uint16_t endpoint_id,
                                       uint8_t effect_id, uint8_t effect_variant, void *priv_data)
{
    if (type == identification::START) {
        xTaskCreate(identify_blink_task, "identify", 2048, NULL, 3, NULL);
    }
    return ESP_OK;
}

static void app_event_cb(const ChipDeviceEvent *event, intptr_t arg)
{
    switch (event->Type) {
    case chip::DeviceLayer::DeviceEventType::kCommissioningComplete:
        ESP_LOGI(TAG, "commissioning complete");
        s_commissioned = true;
        break;
    case chip::DeviceLayer::DeviceEventType::kFabricRemoved:
        ESP_LOGI(TAG, "fabric removed");
        break;
    case chip::DeviceLayer::DeviceEventType::kThreadStateChange:
        /* StartServer() normally runs on kInterfaceIpAddressChanged, which only fires for
         * Wi-Fi/Ethernet. Refresh SRP here once Thread has its addresses. */
        if (event->ThreadStateChange.AddressChanged &&
            chip::DeviceLayer::ConnectivityMgr().IsThreadAttached()) {
            chip::app::DnssdServer::Instance().StartServer();
        }
        break;
    default:
        break;
    }
}

/* ---- Thread dataset written by flash.py --thread -------------------------------- */

static void provision_thread_from_nvs(void)
{
    nvs_handle_t nvs;
    if (nvs_open("thread", NVS_READONLY, &nvs) != ESP_OK) return;
    size_t len = 0;
    if (nvs_get_blob(nvs, "dataset_tlv", NULL, &len) != ESP_OK || len == 0) {
        nvs_close(nvs);
        return;
    }
    uint8_t *tlv = (uint8_t *)malloc(len);
    esp_err_t err = tlv ? nvs_get_blob(nvs, "dataset_tlv", tlv, &len) : ESP_ERR_NO_MEM;
    nvs_close(nvs);
    if (err != ESP_OK) {
        free(tlv);
        return;
    }
    CHIP_ERROR chip_err = chip::DeviceLayer::ThreadStackMgr().SetThreadProvision(chip::ByteSpan(tlv, len));
    if (chip_err == CHIP_NO_ERROR) {
        chip_err = chip::DeviceLayer::ThreadStackMgr().SetThreadEnabled(true);
    }
    free(tlv);
    if (chip_err != CHIP_NO_ERROR) {
        ESP_LOGE(TAG, "Thread provisioning failed: %" CHIP_ERROR_FORMAT, chip_err.Format());
        return;
    }
    ESP_LOGI(TAG, "Thread provisioned from NVS");
}

/* ---- app_main ------------------------------------------------------------------ */

static bool create_endpoint(node_t *node)
{
    endpoint::laundry_washer::config_t washer_cfg;
    washer_cfg.operational_state.delegate = &s_state_delegate;
    endpoint_t *ep = endpoint::laundry_washer::create(node, &washer_cfg, ENDPOINT_FLAG_NONE, NULL);
    if (!ep) return false;
    s_endpoint_id = endpoint::get_id(ep);

    cluster::power_source::config_t ps_cfg;
    strncpy(ps_cfg.description, "2xAA", sizeof(ps_cfg.description) - 1);
    ps_cfg.feature_flags = cluster::power_source::feature::battery::get_id();
    ps_cfg.features.battery.bat_replaceability = 2;   /* UserReplaceable */
    cluster_t *ps = cluster::power_source::create(ep, &ps_cfg, CLUSTER_FLAG_SERVER);
    if (!ps) return false;
    cluster::power_source::attribute::create_bat_percent_remaining(ps, nullable<uint8_t>(200), nullable<uint8_t>(0),
                                                                 nullable<uint8_t>(200));
    cluster::power_source::attribute::create_bat_voltage(ps, nullable<uint32_t>(), nullable<uint32_t>(0), nullable<uint32_t>(4000));

    cluster::temperature_measurement::config_t t_cfg;
    t_cfg.min_measured_value = nullable<int16_t>(-4000);
    t_cfg.max_measured_value = nullable<int16_t>(12500);
    cluster::relative_humidity_measurement::config_t h_cfg;
    h_cfg.min_measured_value = nullable<uint16_t>(0);
    h_cfg.max_measured_value = nullable<uint16_t>(10000);
    return cluster::temperature_measurement::create(ep, &t_cfg, CLUSTER_FLAG_SERVER) &&
           cluster::relative_humidity_measurement::create(ep, &h_cfg, CLUSTER_FLAG_SERVER);
}

extern "C" void app_main(void)
{
    ESP_LOGI(TAG, "laundry sensor starting (%s)",
             BOARD_NAME);
    if (!board_init()) {
        ESP_LOGE(TAG, "board init incomplete; continuing with what works");
    }
    board_led_blink(1, 300, 0);

    esp_err_t ret = nvs_flash_init();
    if (ret == ESP_ERR_NVS_NO_FREE_PAGES || ret == ESP_ERR_NVS_NEW_VERSION_FOUND) {
        ESP_ERROR_CHECK(nvs_flash_erase());
        ret = nvs_flash_init();
    }
    ESP_ERROR_CHECK(ret);

    node::config_t node_cfg;
    node_t *node = node::create(&node_cfg, app_attribute_update_cb, app_identification_cb);
    if (!node || !create_endpoint(node)) {
        ESP_LOGE(TAG, "Matter data model setup failed");
        return;
    }

    esp_openthread_platform_config_t ot_cfg = {};
    ot_cfg.radio_config.radio_mode = RADIO_MODE_NATIVE;
    ot_cfg.host_config.host_connection_mode = HOST_CONNECTION_MODE_NONE;
    ot_cfg.port_config.storage_partition_name = "nvs";
    ot_cfg.port_config.netif_queue_size = 10;
    ot_cfg.port_config.task_queue_size = 10;
    set_openthread_platform_config(&ot_cfg);

    /* Automatic light sleep whenever every task is blocked. */
    esp_pm_config_t pm = {
        .max_freq_mhz = 160,
        .min_freq_mhz = 40,
        .light_sleep_enable = true,
    };
    ESP_ERROR_CHECK(esp_pm_configure(&pm));

    ret = esp_matter::start(app_event_cb);
    if (ret != ESP_OK) {
        ESP_LOGE(TAG, "esp_matter::start failed: %d", ret);
        return;
    }
    provision_thread_from_nvs();
    PrintOnboardingCodes(chip::RendezvousInformationFlags(chip::RendezvousInformationFlag::kOnNetwork));

    {
        chip::DeviceLayer::StackLock lock;
        s_commissioned = chip::Server::GetInstance().GetFabricTable().FabricCount() > 0;
    }
    ESP_LOGI(TAG, "commissioned: %s", s_commissioned ? "yes" : "no");
    report_running(false, false);

    xTaskCreate(accel_task, "accel", 4096, NULL, 5, &s_accel_task);
    xTaskCreate(button_task, "button", 3072, NULL, 4, &s_button_task);
    xTaskCreate(env_task, "env", 3072, NULL, 3, NULL);
    if (!s_commissioned) {
        xTaskCreate(pairing_blink_task, "pairing", 2048, NULL, 2, NULL);
    }
}
