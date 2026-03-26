#include <string.h>
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "esp_log.h"
#include "esp_timer.h"
#include "nvs_flash.h"
#include "driver/gpio.h"
#include "cJSON.h"

#include "wifi_manager.h"
#include "ha_client.h"
#include "neopixel.h"
#include "nvs.h"

extern const char ca_bundle_pem_start[] asm("_binary_ca_bundle_pem_start");
extern const char ca_bundle_pem_end[]   asm("_binary_ca_bundle_pem_end");

static const char *TAG = "garage";

#define WIFI_MAX_RETRIES 5
#define NVS_NAMESPACE    "garage"
#define MAX_STR_LEN      256

static char s_wifi_ssid[MAX_STR_LEN];
static char s_wifi_pass[MAX_STR_LEN];
static char s_ha_url[MAX_STR_LEN];
static char s_ha_entity[MAX_STR_LEN];
static char s_ha_key[512];

static bool load_secrets(void)
{
    nvs_handle_t h;
    esp_err_t err = nvs_open(NVS_NAMESPACE, NVS_READONLY, &h);
    if (err != ESP_OK) {
        ESP_LOGE(TAG, "NVS open failed (%s) — flash secrets partition first", esp_err_to_name(err));
        return false;
    }

    size_t len;
    bool ok = true;

    #define READ_STR(nvs_key, buf) do { \
        len = sizeof(buf); \
        err = nvs_get_str(h, nvs_key, buf, &len); \
        if (err != ESP_OK) { \
            ESP_LOGE(TAG, "NVS key '%s' missing (%s)", nvs_key, esp_err_to_name(err)); \
            ok = false; \
        } \
    } while (0)

    READ_STR("wifi_ssid", s_wifi_ssid);
    READ_STR("wifi_pass",  s_wifi_pass);
    READ_STR("ha_url",     s_ha_url);
    READ_STR("ha_entity",  s_ha_entity);
    READ_STR("ha_key",     s_ha_key);

    #undef READ_STR
    nvs_close(h);
    return ok;
}

// GPIO
#define BUTTON_PIN       17
#define BUTTON_LED_PIN   18
#define NEOPIXEL_PIN     33
#define NEOPIXEL_POWER   21

// Timing
#define POLL_INTERVAL_MS       3000
#define BUTTON_LED_DURATION_MS 3000
#define DEBOUNCE_MS            50

#define LED_BRIGHTNESS 128

// Status colors
#define COLOR_RED_R    LED_BRIGHTNESS
#define COLOR_RED_G    0
#define COLOR_RED_B    0
#define COLOR_GREEN_R  0
#define COLOR_GREEN_G  LED_BRIGHTNESS
#define COLOR_GREEN_B  0
#define COLOR_YELLOW_R LED_BRIGHTNESS
#define COLOR_YELLOW_G LED_BRIGHTNESS
#define COLOR_YELLOW_B 0
#define COLOR_BLUE_R   0
#define COLOR_BLUE_G   0
#define COLOR_BLUE_B   LED_BRIGHTNESS

#define HTTP_BUF_SIZE 1024

static void toggle_garage(void)
{
    ESP_LOGI(TAG, "Toggling garage: %s", s_ha_entity);
    char payload[MAX_STR_LEN + 32];
    snprintf(payload, sizeof(payload), "{\"entity_id\":\"%s\"}", s_ha_entity);
    int code = ha_post("/api/services/cover/toggle", payload);
    if (code < 0) {
        ESP_LOGE(TAG, "Toggle request failed");
    }
}

static bool poll_garage_status(void)
{
    ESP_LOGI(TAG, "Polling garage status...");
    char resp[HTTP_BUF_SIZE] = {0};
    char url_buf[MAX_STR_LEN + 16];
    snprintf(url_buf, sizeof(url_buf), "/api/states/%s", s_ha_entity);
    int code = ha_get(url_buf, resp, HTTP_BUF_SIZE);

    if (code != 200) {
        ESP_LOGE(TAG, "Poll failed, HTTP %d", code);
        neopixel_set(COLOR_YELLOW_R, COLOR_YELLOW_G, COLOR_YELLOW_B);
        return false;
    }

    cJSON *doc = cJSON_Parse(resp);
    if (!doc) {
        ESP_LOGE(TAG, "JSON parse error");
        neopixel_set(COLOR_YELLOW_R, COLOR_YELLOW_G, COLOR_YELLOW_B);
        return false;
    }

    cJSON *state_item = cJSON_GetObjectItem(doc, "state");
    if (!cJSON_IsString(state_item)) {
        ESP_LOGE(TAG, "JSON 'state' key not found");
        cJSON_Delete(doc);
        neopixel_set(COLOR_YELLOW_R, COLOR_YELLOW_G, COLOR_YELLOW_B);
        return false;
    }

    const char *state = state_item->valuestring;
    ESP_LOGI(TAG, "Garage state: %s", state);
    if (strcmp(state, "open") == 0) {
        neopixel_set(COLOR_GREEN_R, COLOR_GREEN_G, COLOR_GREEN_B);
    } else if (strcmp(state, "opening") == 0 || strcmp(state, "closing") == 0) {
        neopixel_set(COLOR_YELLOW_R, COLOR_YELLOW_G, COLOR_YELLOW_B);
    } else {
        neopixel_set(COLOR_RED_R, COLOR_RED_G, COLOR_RED_B);
    }
    cJSON_Delete(doc);
    return true;
}

void app_main(void)
{
    ESP_LOGI(TAG, "Starting up...");

    esp_err_t ret = nvs_flash_init();
    if (ret == ESP_ERR_NVS_NO_FREE_PAGES || ret == ESP_ERR_NVS_NEW_VERSION_FOUND) {
        ESP_ERROR_CHECK(nvs_flash_erase());
        ret = nvs_flash_init();
    }
    ESP_ERROR_CHECK(ret);

    neopixel_init(NEOPIXEL_PIN, NEOPIXEL_POWER);
    neopixel_set(COLOR_BLUE_R, COLOR_BLUE_G, COLOR_BLUE_B);

    gpio_config_t btn_led_cfg = {
        .pin_bit_mask = (1ULL << BUTTON_LED_PIN),
        .mode = GPIO_MODE_OUTPUT,
    };
    ESP_ERROR_CHECK(gpio_config(&btn_led_cfg));
    gpio_set_level(BUTTON_LED_PIN, 0);

    gpio_config_t btn_cfg = {
        .pin_bit_mask = (1ULL << BUTTON_PIN),
        .mode = GPIO_MODE_INPUT,
        .pull_down_en = GPIO_PULLDOWN_ENABLE,
        .pull_up_en = GPIO_PULLUP_DISABLE,
        .intr_type = GPIO_INTR_DISABLE,
    };
    ESP_ERROR_CHECK(gpio_config(&btn_cfg));

    if (!load_secrets()) {
        ESP_LOGE(TAG, "Cannot start without secrets — flash the NVS secrets partition");
        neopixel_set(COLOR_RED_R, COLOR_RED_G, COLOR_RED_B);
        return;
    }
    ESP_LOGI(TAG, "Secrets loaded from NVS");

    wifi_manager_init(s_wifi_ssid, s_wifi_pass, WIFI_MAX_RETRIES);
    ha_client_init(s_ha_url, s_ha_key, ca_bundle_pem_start);

    poll_garage_status();

    int last_button_reading = 0;
    int button_state = 0;
    int64_t last_debounce_time = 0;
    int64_t button_led_on_at = 0;
    int64_t last_poll_time = esp_timer_get_time() / 1000;

    while (1) {
        int64_t now = esp_timer_get_time() / 1000;

        int reading = gpio_get_level(BUTTON_PIN);
        if (reading != last_button_reading) {
            last_debounce_time = now;
        }
        if ((now - last_debounce_time) > DEBOUNCE_MS && reading != button_state) {
            button_state = reading;
            if (button_state == 1) {
                ESP_LOGI(TAG, "Button pressed.");
                gpio_set_level(BUTTON_LED_PIN, 1);
                button_led_on_at = now;
                if (wifi_manager_connected()) {
                    toggle_garage();
                    vTaskDelay(pdMS_TO_TICKS(100));
                    poll_garage_status();
                } else {
                    ESP_LOGW(TAG, "WiFi not connected");
                    neopixel_set(COLOR_YELLOW_R, COLOR_YELLOW_G, COLOR_YELLOW_B);
                }
            }
        }
        last_button_reading = reading;

        if (button_led_on_at != 0 && (now - button_led_on_at) >= BUTTON_LED_DURATION_MS) {
            gpio_set_level(BUTTON_LED_PIN, 0);
            button_led_on_at = 0;
        }

        if ((now - last_poll_time) >= POLL_INTERVAL_MS) {
            if (wifi_manager_connected()) {
                poll_garage_status();
            } else {
                ESP_LOGW(TAG, "WiFi disconnected");
                neopixel_set(COLOR_YELLOW_R, COLOR_YELLOW_G, COLOR_YELLOW_B);
            }
            last_poll_time = now;
        }

        vTaskDelay(pdMS_TO_TICKS(10));
    }
}
