#include "openthread_mocks.h"
#include <stdlib.h>

/* ---- Mock state -------------------------------------------------------- */

static esp_err_t    s_mock_start_result      = ESP_OK;
static esp_err_t    s_mock_auto_start_result = ESP_OK;
static bool         s_join_immediately       = false;
static otDeviceRole s_mock_role              = OT_DEVICE_ROLE_DETACHED;
static int          s_stop_count             = 0;

static struct otInstance s_fake_instance;

void ot_mock_set_start_result(esp_err_t result)      { s_mock_start_result = result; }
void ot_mock_set_auto_start_result(esp_err_t result) { s_mock_auto_start_result = result; }
void ot_mock_set_join_immediately(bool join)         { s_join_immediately = join; }
void ot_mock_set_role(otDeviceRole role)             { s_mock_role = role; }
int  ot_mock_get_stop_count(void)                    { return s_stop_count; }

void ot_mock_reset(void)
{
    s_mock_start_result      = ESP_OK;
    s_mock_auto_start_result = ESP_OK;
    s_join_immediately       = false;
    s_mock_role              = OT_DEVICE_ROLE_DETACHED;
    s_stop_count             = 0;
}

/* ---- esp_openthread stubs ---------------------------------------------- */

esp_err_t esp_openthread_start(const esp_openthread_config_t *config)
{
    (void)config;
    return s_mock_start_result;
}

esp_err_t esp_openthread_stop(void)
{
    s_stop_count++;
    s_mock_role = OT_DEVICE_ROLE_DETACHED;
    return ESP_OK;
}

esp_err_t esp_openthread_auto_start(otOperationalDatasetTlvs *dataset_tlvs)
{
    (void)dataset_tlvs;
    if (s_mock_auto_start_result != ESP_OK) {
        return s_mock_auto_start_result;
    }
    if (s_join_immediately) {
        s_mock_role = OT_DEVICE_ROLE_CHILD;
    }
    return ESP_OK;
}

otInstance *esp_openthread_get_instance(void)
{
    return &s_fake_instance;
}

/* ---- OpenThread stubs -------------------------------------------------- */

void otDatasetConvertToTlvs(const otOperationalDataset *dataset,
                             otOperationalDatasetTlvs *tlvs)
{
    (void)dataset;
    if (tlvs) {
        tlvs->mLength = 0;
    }
}

otDeviceRole otThreadGetDeviceRole(otInstance *instance)
{
    (void)instance;
    return s_mock_role;
}
