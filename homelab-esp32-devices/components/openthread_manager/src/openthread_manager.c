#include "openthread_manager.h"

#include <inttypes.h>
#include <string.h>
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "esp_log.h"
#include "esp_openthread.h"
#include "esp_openthread_netif_glue.h"
#include "openthread/thread.h"
#include "openthread/dataset.h"
#include "openthread/instance.h"

static const char *TAG = "ot_manager";

#define OT_POLL_INTERVAL_MS 500

static bool s_attached = false;

esp_err_t ot_manager_init(const ot_credentials_t *creds, uint32_t timeout_ms)
{
    s_attached = false;

    esp_openthread_config_t ot_config = {
        .netif_config = ESP_NETIF_DEFAULT_OPENTHREAD(),
        .platform_config = {
            .radio_config = { .radio_mode = RADIO_MODE_NATIVE },
            .host_config  = { .host_connection_mode = HOST_CONNECTION_MODE_NONE },
            .port_config  = {
                .storage_partition_name = "nvs",
                .netif_queue_size = 10,
                .task_queue_size  = 10,
            },
        },
    };

    esp_err_t ret = esp_openthread_start(&ot_config);
    if (ret != ESP_OK) {
        ESP_LOGE(TAG, "esp_openthread_start failed: %d", ret);
        return ret;
    }

    otOperationalDataset dataset;
    memset(&dataset, 0, sizeof(dataset));

    strncpy(dataset.mNetworkName.m8, creds->network_name, OT_NETWORK_NAME_MAX_SIZE);
    dataset.mComponents.mIsNetworkNamePresent = true;

    dataset.mChannel = creds->channel;
    dataset.mComponents.mIsChannelPresent = true;

    dataset.mPanId = creds->pan_id;
    dataset.mComponents.mIsPanIdPresent = true;

    memcpy(dataset.mExtendedPanId.m8, creds->extended_pan_id, OT_EXT_PAN_ID_SIZE);
    dataset.mComponents.mIsExtendedPanIdPresent = true;

    memcpy(dataset.mNetworkKey.m8, creds->network_key, OT_NETWORK_KEY_SIZE);
    dataset.mComponents.mIsNetworkKeyPresent = true;

    otOperationalDatasetTlvs dataset_tlvs;
    otDatasetConvertToTlvs(&dataset, &dataset_tlvs);

    ret = esp_openthread_auto_start(&dataset_tlvs);
    if (ret != ESP_OK) {
        ESP_LOGE(TAG, "esp_openthread_auto_start failed: %d", ret);
        esp_openthread_stop();
        return ret;
    }

    otInstance *instance = esp_openthread_get_instance();
    uint32_t elapsed = 0;
    while (elapsed <= timeout_ms) {
        otDeviceRole role = otThreadGetDeviceRole(instance);
        if (role == OT_DEVICE_ROLE_CHILD  ||
            role == OT_DEVICE_ROLE_ROUTER ||
            role == OT_DEVICE_ROLE_LEADER) {
            s_attached = true;
            ESP_LOGI(TAG, "OpenThread attached (role=%d)", (int)role);
            return ESP_OK;
        }
        if (elapsed + OT_POLL_INTERVAL_MS > timeout_ms) break;
        vTaskDelay(pdMS_TO_TICKS(OT_POLL_INTERVAL_MS));
        elapsed += OT_POLL_INTERVAL_MS;
    }

    ESP_LOGE(TAG, "OpenThread attach timeout after %" PRIu32 " ms", timeout_ms);
    esp_openthread_stop();
    return ESP_ERR_TIMEOUT;
}

bool ot_manager_is_attached(void)
{
    return s_attached;
}

void ot_manager_deinit(void)
{
    esp_openthread_stop();
    s_attached = false;
}
