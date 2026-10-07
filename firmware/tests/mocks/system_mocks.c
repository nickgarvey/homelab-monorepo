#include "esp_idf_mocks.h"

/* ---- Certificate bundle ------------------------------------------------- */
/*
 * ha_client passes esp_crt_bundle_attach to esp_http_client so mbedTLS verifies
 * against ESP-IDF's Mozilla root bundle. On the host there is no TLS stack to
 * attach to -- the tests only assert on URLs and headers -- so this just has to
 * exist for the link.
 */
esp_err_t esp_crt_bundle_attach(void *conf)
{
    (void)conf;
    return ESP_OK;
}
