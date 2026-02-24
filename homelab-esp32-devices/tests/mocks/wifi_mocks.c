#include "esp_idf_mocks.h"
#include <stdlib.h>

/* ---- Event group simulation -------------------------------------------- */

struct EventGroup {
    EventBits_t bits;
};

EventGroupHandle_t xEventGroupCreate(void)
{
    struct EventGroup *g = calloc(1, sizeof(struct EventGroup));
    return (EventGroupHandle_t)g;
}

EventBits_t xEventGroupSetBits(EventGroupHandle_t xEventGroup, EventBits_t uxBitsToSet)
{
    struct EventGroup *g = (struct EventGroup *)xEventGroup;
    g->bits |= uxBitsToSet;
    return g->bits;
}

EventBits_t xEventGroupGetBits(EventGroupHandle_t xEventGroup)
{
    struct EventGroup *g = (struct EventGroup *)xEventGroup;
    return g->bits;
}

/*
 * In tests, wifi_manager_init calls xEventGroupWaitBits expecting the event
 * handler to set either WIFI_CONNECTED_BIT or WIFI_FAIL_BIT. Since we are
 * not running FreeRTOS, this implementation returns whatever bits are already
 * set (the test is responsible for pre-seeding them via the test helper below).
 */
EventBits_t xEventGroupWaitBits(EventGroupHandle_t xEventGroup,
                                EventBits_t uxBitsToWaitFor,
                                int xClearOnExit,
                                int xWaitForAllBits,
                                uint32_t xTicksToWait)
{
    (void)xClearOnExit;
    (void)xWaitForAllBits;
    (void)xTicksToWait;
    struct EventGroup *g = (struct EventGroup *)xEventGroup;
    return g->bits & uxBitsToWaitFor;
}

/* ---- WiFi stubs --------------------------------------------------------- */

static esp_event_handler_t s_wifi_handler;
static esp_event_handler_t s_ip_handler;

esp_err_t esp_wifi_init(wifi_init_config_t *cfg)   { (void)cfg; return ESP_OK; }
esp_err_t esp_wifi_set_mode(int mode)               { (void)mode; return ESP_OK; }
esp_err_t esp_wifi_set_config(int iface, wifi_config_t *cfg) { (void)iface; (void)cfg; return ESP_OK; }
esp_err_t esp_wifi_connect(void)                    { return ESP_OK; }

/*
 * esp_wifi_start triggers the STA_START event in the real driver.
 * Here we call the registered WIFI_EVENT handler with STA_START so that
 * wifi_manager.c calls esp_wifi_connect() as it normally would, then we
 * immediately deliver the GOT_IP event to simulate a successful connection.
 */
esp_err_t esp_wifi_start(void)
{
    if (s_wifi_handler) {
        s_wifi_handler(NULL, WIFI_EVENT, WIFI_EVENT_STA_START, NULL);
    }
    if (s_ip_handler) {
        ip_event_got_ip_t evt = {0};
        s_ip_handler(NULL, IP_EVENT, IP_EVENT_STA_GOT_IP, &evt);
    }
    return ESP_OK;
}

esp_err_t esp_netif_init(void)                      { return ESP_OK; }
void      esp_netif_create_default_wifi_sta(void)   {}
esp_err_t esp_event_loop_create_default(void)       { return ESP_OK; }

esp_err_t esp_event_handler_instance_register(esp_event_base_t event_base,
                                               int32_t event_id,
                                               esp_event_handler_t event_handler,
                                               void *event_handler_arg,
                                               esp_event_handler_instance_t *instance)
{
    (void)event_id;
    (void)event_handler_arg;
    (void)instance;
    if (event_base == WIFI_EVENT) {
        s_wifi_handler = event_handler;
    } else if (event_base == IP_EVENT) {
        s_ip_handler = event_handler;
    }
    return ESP_OK;
}

/* ---- GPIO stubs --------------------------------------------------------- */

esp_err_t gpio_config(const gpio_config_t *cfg)            { (void)cfg; return ESP_OK; }
esp_err_t gpio_set_level(int gpio_num, uint32_t level)     { (void)gpio_num; (void)level; return ESP_OK; }
int       gpio_get_level(int gpio_num)                     { (void)gpio_num; return 0; }
