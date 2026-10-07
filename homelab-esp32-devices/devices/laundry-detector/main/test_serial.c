#include <stdio.h>
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "esp_log.h"

static const char *TAG = "test";

void app_main(void)
{
    int i = 0;
    while (1) {
        ESP_LOGI(TAG, "Hello from laundry-detector! count=%d", i++);
        printf("printf test %d\n", i);
        vTaskDelay(pdMS_TO_TICKS(1000));
    }
}
