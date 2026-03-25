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

/*
 * vTaskDelay mock uses longjmp to break out of infinite loops in app_main().
 * Tests call system_mock_set_max_delays(N) then setjmp(g_test_jmp_buf) before
 * calling app_main().  After N calls to vTaskDelay, the mock longjmps back.
 */
#include <setjmp.h>
extern jmp_buf g_test_jmp_buf;
extern int g_vTaskDelay_count;
extern int g_vTaskDelay_max;

static inline void vTaskDelay(uint32_t xTicksToDelay)
{
    (void)xTicksToDelay;
    g_vTaskDelay_count++;
    if (g_vTaskDelay_max > 0 && g_vTaskDelay_count >= g_vTaskDelay_max) {
        longjmp(g_test_jmp_buf, 1);
    }
}
static inline void vTaskDelete(TaskHandle_t xTask)    { (void)xTask; }
