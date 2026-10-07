idf_component_register(
    SRCS "main.cpp" "lis3dh.c" "max17048.c" "vibration_monitor.c" "status_led.c" "sht40.c" "boot_action.c"
    INCLUDE_DIRS "."
    REQUIRES esp_matter neopixel
    PRIV_REQUIRES driver nvs_flash esp_timer
)
