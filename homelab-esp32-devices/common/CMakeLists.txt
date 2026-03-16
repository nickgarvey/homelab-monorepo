idf_component_register(
    SRCS
        "src/wifi_manager.c"
        "src/ha_client.c"
        "src/neopixel.c"
        "src/openthread_manager.c"
    INCLUDE_DIRS "include"
    REQUIRES
        esp_wifi
        esp_event
        esp_netif
        esp_http_client
        driver
        led_strip
        nvs_flash
        openthread
)
