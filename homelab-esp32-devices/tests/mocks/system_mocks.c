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

/* ---- Reset helper for test setUp --------------------------------------- */

void system_mock_reset(void)
{
    g_deep_sleep_count = 0;
    g_vTaskDelay_count = 0;
    g_vTaskDelay_max   = 0;
}
