#include "esp_idf_mocks.h"
#include <setjmp.h>

/* ---- vTaskDelay loop-breaker ------------------------------------------- */

jmp_buf g_test_jmp_buf;
int g_vTaskDelay_count = 0;
int g_vTaskDelay_max   = 0;

/* ---- esp_deep_sleep with call counter ---------------------------------- */

int g_deep_sleep_count = 0;

void esp_deep_sleep(uint64_t time_us)
{
    (void)time_us;
    g_deep_sleep_count++;
}

/* ---- Linker symbols for embedded CA cert (EMBED_TXTFILES in IDF) ------- */
/*
 * main.c declares:
 *   extern const char ca_bundle_pem_start[] asm("_binary_ca_bundle_pem_start");
 * The linker must find that symbol; provide an empty stub here.
 */
const char _binary_ca_bundle_pem_start[] = "";
const char _binary_ca_bundle_pem_end[]   = "";

/* ---- Deep sleep wakeup cause ------------------------------------------- */

esp_sleep_source_t g_mock_wakeup_cause = ESP_SLEEP_WAKEUP_UNDEFINED;

esp_sleep_source_t esp_sleep_get_wakeup_cause(void)
{
    return g_mock_wakeup_cause;
}

esp_err_t esp_deep_sleep_enable_gpio_wakeup(uint64_t gpio_pin_mask,
                                             esp_deepsleep_gpio_wake_up_mode_t mode)
{
    (void)gpio_pin_mask;
    (void)mode;
    return ESP_OK;
}

void esp_deep_sleep_start(void)
{
    g_deep_sleep_count++;
}

/* ---- Reset helper for test setUp --------------------------------------- */

void system_mock_reset(void)
{
    g_deep_sleep_count = 0;
    g_vTaskDelay_count = 0;
    g_vTaskDelay_max   = 0;
    g_mock_wakeup_cause = ESP_SLEEP_WAKEUP_UNDEFINED;
}
