#pragma once

/*
 * Minimal stubs for ESP-IDF types and macros so common/ sources compile
 * under native GCC without the ESP-IDF toolchain.
 */

#include <stdint.h>
#include <stdbool.h>
#include <string.h>
#include <stdio.h>

/* ---- esp_err ------------------------------------------------------------ */
typedef int esp_err_t;
#define ESP_OK    0
#define ESP_FAIL -1
#define ESP_ERR_NVS_NO_FREE_PAGES  0x1101
#define ESP_ERR_NVS_NEW_VERSION_FOUND 0x1102
#define ESP_ERROR_CHECK(x) do { esp_err_t _rc = (x); (void)_rc; } while (0)

/* ---- Logging ------------------------------------------------------------ */
#define ESP_LOGI(tag, fmt, ...) printf("[I][%s] " fmt "\n", tag, ##__VA_ARGS__)
#define ESP_LOGE(tag, fmt, ...) printf("[E][%s] " fmt "\n", tag, ##__VA_ARGS__)
#define ESP_LOGW(tag, fmt, ...) printf("[W][%s] " fmt "\n", tag, ##__VA_ARGS__)

/* ---- FreeRTOS event groups ---------------------------------------------- */
typedef uint32_t EventBits_t;
typedef struct EventGroup *EventGroupHandle_t;

#define WIFI_CONNECTED_BIT BIT0
#define WIFI_FAIL_BIT      BIT1
#define BIT0  (1u << 0)
#define BIT1  (1u << 1)
#define pdFALSE 0
#define pdTRUE  1
#define portMAX_DELAY (~(uint32_t)0)

/* The event group is simulated by a plain uint32_t. */
EventGroupHandle_t xEventGroupCreate(void);
EventBits_t xEventGroupSetBits(EventGroupHandle_t xEventGroup, EventBits_t uxBitsToSet);
EventBits_t xEventGroupGetBits(EventGroupHandle_t xEventGroup);
EventBits_t xEventGroupWaitBits(EventGroupHandle_t xEventGroup,
                                EventBits_t uxBitsToWaitFor,
                                int xClearOnExit,
                                int xWaitForAllBits,
                                uint32_t xTicksToWait);

/* ---- WiFi / event types ------------------------------------------------- */
typedef const char *esp_event_base_t;
typedef void (*esp_event_handler_t)(void *arg, esp_event_base_t event_base,
                                     int32_t event_id, void *event_data);
typedef void *esp_event_handler_instance_t;

#define WIFI_EVENT      "WIFI_EVENT"
#define IP_EVENT        "IP_EVENT"
#define ESP_EVENT_ANY_ID (-1)
#define WIFI_EVENT_STA_START        1
#define WIFI_EVENT_STA_DISCONNECTED 2
#define IP_EVENT_STA_GOT_IP         3
#define WIFI_AUTH_WPA2_PSK          3
#define WIFI_MODE_STA               1
#define WIFI_IF_STA                 0

typedef struct {
    struct {
        char ssid[32];
        char password[64];
        struct { int authmode; } threshold;
    } sta;
} wifi_config_t;

typedef struct { int dummy; } wifi_init_config_t;
#define WIFI_INIT_CONFIG_DEFAULT() { 0 }

typedef struct {
    struct {
        uint32_t addr;
    } ip_info;
} ip_event_got_ip_t;

#define IPSTR       "%d.%d.%d.%d"
#define IP2STR(ip)  0, 0, 0, 0

esp_err_t esp_wifi_init(wifi_init_config_t *cfg);
esp_err_t esp_wifi_set_mode(int mode);
esp_err_t esp_wifi_set_config(int iface, wifi_config_t *cfg);
esp_err_t esp_wifi_start(void);
esp_err_t esp_wifi_connect(void);

esp_err_t esp_netif_init(void);
void      esp_netif_create_default_wifi_sta(void);
esp_err_t esp_event_loop_create_default(void);
esp_err_t esp_event_handler_instance_register(esp_event_base_t event_base,
                                               int32_t event_id,
                                               esp_event_handler_t event_handler,
                                               void *event_handler_arg,
                                               esp_event_handler_instance_t *instance);

/* ---- HTTP client stubs -------------------------------------------------- */
typedef int esp_http_client_event_id_t;
#define HTTP_EVENT_ON_DATA 5

typedef struct {
    esp_http_client_event_id_t event_id;
    void  *user_data;
    char  *data;
    int    data_len;
} esp_http_client_event_t;

typedef esp_err_t (*http_event_handle_cb)(esp_http_client_event_t *evt);

typedef struct {
    const char           *url;
    http_event_handle_cb  event_handler;
    void                 *user_data;
    int                   transport_type;
    const char           *cert_pem;
    int                   timeout_ms;
} esp_http_client_config_t;

typedef struct EspHttpClient *esp_http_client_handle_t;

#define HTTP_TRANSPORT_OVER_SSL 2
#define HTTP_METHOD_POST        1
#define HTTP_METHOD_GET         0

esp_http_client_handle_t esp_http_client_init(const esp_http_client_config_t *config);
esp_err_t esp_http_client_set_method(esp_http_client_handle_t client, int method);
esp_err_t esp_http_client_set_header(esp_http_client_handle_t client,
                                      const char *key, const char *value);
esp_err_t esp_http_client_set_post_field(esp_http_client_handle_t client,
                                          const char *data, int len);
esp_err_t esp_http_client_perform(esp_http_client_handle_t client);
int       esp_http_client_get_status_code(esp_http_client_handle_t client);
esp_err_t esp_http_client_cleanup(esp_http_client_handle_t client);

/* ---- GPIO / LED strip stubs (neopixel) ---------------------------------- */
typedef uint64_t gpio_mode_t;
#define GPIO_MODE_OUTPUT 2
#define GPIO_MODE_INPUT  1
#define GPIO_PULLDOWN_ENABLE 1
#define GPIO_PULLUP_DISABLE  0
#define GPIO_INTR_DISABLE    0

typedef struct {
    uint64_t pin_bit_mask;
    gpio_mode_t mode;
    int pull_down_en;
    int pull_up_en;
    int intr_type;
} gpio_config_t;

esp_err_t gpio_config(const gpio_config_t *cfg);
esp_err_t gpio_set_level(int gpio_num, uint32_t level);
int       gpio_get_level(int gpio_num);

typedef int rmt_clock_source_t;
#define RMT_CLK_SRC_DEFAULT 0
#define LED_PIXEL_FORMAT_GRB 0
#define LED_MODEL_WS2812     0

typedef struct {
    int strip_gpio_num;
    int max_leds;
    int led_pixel_format;
    int led_model;
    struct { bool invert_out; } flags;
} led_strip_config_t;

typedef struct {
    rmt_clock_source_t clk_src;
    uint32_t resolution_hz;
    struct { bool with_dma; } flags;
} led_strip_rmt_config_t;

typedef struct LedStrip *led_strip_handle_t;

esp_err_t led_strip_new_rmt_device(const led_strip_config_t *strip_config,
                                    const led_strip_rmt_config_t *rmt_config,
                                    led_strip_handle_t *ret_strip);
esp_err_t led_strip_set_pixel(led_strip_handle_t strip, uint32_t index,
                               uint32_t red, uint32_t green, uint32_t blue);
esp_err_t led_strip_refresh(led_strip_handle_t strip);
esp_err_t led_strip_clear(led_strip_handle_t strip);
