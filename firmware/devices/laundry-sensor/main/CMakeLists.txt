idf_component_register(
    SRCS "main.cpp" "board.c" "lis2dw12.c" "sht40.c" "battery.c" "vibration_monitor.c"
         "fake_accel.c"
    INCLUDE_DIRS "."
    REQUIRES esp_matter
    PRIV_REQUIRES driver esp_driver_spi esp_driver_i2c esp_driver_gpio esp_adc esp_pm
                  nvs_flash esp_timer
)
