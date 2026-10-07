#pragma once
#include "esp_idf_mocks.h"

typedef void *TaskHandle_t;
typedef void (*TaskFunction_t)(void *);

#define pdMS_TO_TICKS(ms) ((uint32_t)(ms))

static inline BaseType_t xTaskCreate(TaskFunction_t pxTaskCode,
                                     const char *pcName,
                                     uint32_t usStackDepth,
                                     void *pvParameters,
                                     uint32_t uxPriority,
                                     TaskHandle_t *pxCreatedTask)
{
    (void)pxTaskCode;
    (void)pcName;
    (void)usStackDepth;
    (void)pvParameters;
    (void)uxPriority;
    if (pxCreatedTask) *pxCreatedTask = (void *)1;
    return pdPASS;
}

static inline void vTaskDelay(uint32_t xTicksToDelay) { (void)xTicksToDelay; }
static inline void vTaskDelete(TaskHandle_t xTask)    { (void)xTask; }
