#pragma once

#include <stdint.h>
#include <stdbool.h>
#include <string.h>
#include "esp_idf_mocks.h"

/* ---- OpenThread device role -------------------------------------------- */

typedef enum {
    OT_DEVICE_ROLE_DISABLED = 0,
    OT_DEVICE_ROLE_DETACHED = 1,
    OT_DEVICE_ROLE_CHILD    = 2,
    OT_DEVICE_ROLE_ROUTER   = 3,
    OT_DEVICE_ROLE_LEADER   = 4,
} otDeviceRole;

typedef int otError;
#define OT_ERROR_NONE 0

/* Instance type (opaque in real OT; concrete stub here for static allocation). */
struct otInstance { int dummy; };
typedef struct otInstance otInstance;

/* ---- OpenThread dataset types ------------------------------------------ */

#define OT_NETWORK_NAME_MAX_SIZE 16
#define OT_EXT_PAN_ID_SIZE       8
#define OT_NETWORK_KEY_SIZE      16

typedef struct { char    m8[OT_NETWORK_NAME_MAX_SIZE + 1]; } otNetworkName;
typedef struct { uint8_t m8[OT_NETWORK_KEY_SIZE]; }          otNetworkKey;
typedef struct { uint8_t m8[OT_EXT_PAN_ID_SIZE];  }          otExtendedPanId;
typedef struct { uint8_t m8[8]; uint8_t mLength;  }          otMeshLocalPrefix;

typedef struct {
    bool mIsNetworkKeyPresent;
    bool mIsNetworkNamePresent;
    bool mIsExtendedPanIdPresent;
    bool mIsMeshLocalPrefixPresent;
    bool mIsPanIdPresent;
    bool mIsChannelPresent;
    bool mIsTimestampPresent;
} otOperationalDatasetComponents;

typedef struct {
    struct { uint64_t mSeconds; uint16_t mTicks; bool mAuthoritative; } mActiveTimestamp;
    struct { uint64_t mSeconds; uint16_t mTicks; bool mAuthoritative; } mPendingTimestamp;
    otNetworkKey                   mNetworkKey;
    otNetworkName                  mNetworkName;
    otExtendedPanId                mExtendedPanId;
    otMeshLocalPrefix              mMeshLocalPrefix;
    uint32_t                       mDelay;
    uint16_t                       mPanId;
    uint16_t                       mChannel;
    otOperationalDatasetComponents mComponents;
} otOperationalDataset;

typedef struct {
    uint8_t mTlvs[255];
    uint8_t mLength;
} otOperationalDatasetTlvs;

/* ---- ESP OpenThread platform config ------------------------------------ */

typedef enum {
    RADIO_MODE_NATIVE   = 0,
    RADIO_MODE_UART_RCP = 1,
    RADIO_MODE_SPI_RCP  = 2,
} esp_openthread_radio_mode_t;

typedef enum {
    HOST_CONNECTION_MODE_NONE     = 0,
    HOST_CONNECTION_MODE_CLI_UART = 1,
    HOST_CONNECTION_MODE_RCP_UART = 2,
} esp_openthread_host_connection_mode_t;

typedef struct {
    esp_openthread_radio_mode_t radio_mode;
} esp_openthread_radio_config_t;

typedef struct {
    esp_openthread_host_connection_mode_t host_connection_mode;
} esp_openthread_host_connection_config_t;

typedef struct {
    const char *storage_partition_name;
    uint8_t     netif_queue_size;
    uint8_t     task_queue_size;
} esp_openthread_port_config_t;

typedef struct {
    esp_openthread_radio_config_t           radio_config;
    esp_openthread_host_connection_config_t host_config;
    esp_openthread_port_config_t            port_config;
} esp_openthread_platform_config_t;

/* Stub netif types */
typedef int esp_netif_config_t;
#define ESP_NETIF_DEFAULT_OPENTHREAD() 0

typedef struct {
    esp_netif_config_t               netif_config;
    esp_openthread_platform_config_t platform_config;
} esp_openthread_config_t;

/* ---- esp_openthread API ------------------------------------------------ */

esp_err_t   esp_openthread_start(const esp_openthread_config_t *config);
esp_err_t   esp_openthread_stop(void);
esp_err_t   esp_openthread_auto_start(otOperationalDatasetTlvs *dataset_tlvs);
otInstance *esp_openthread_get_instance(void);

/* ---- OpenThread API ---------------------------------------------------- */

void         otDatasetConvertToTlvs(const otOperationalDataset *dataset,
                                    otOperationalDatasetTlvs *tlvs);
otDeviceRole otThreadGetDeviceRole(otInstance *instance);

/* ---- Mock control ----------------------------------------------------- */

void ot_mock_set_start_result(esp_err_t result);
void ot_mock_set_auto_start_result(esp_err_t result);
void ot_mock_set_join_immediately(bool join);
void ot_mock_set_role(otDeviceRole role);
int  ot_mock_get_stop_count(void);
void ot_mock_reset(void);
