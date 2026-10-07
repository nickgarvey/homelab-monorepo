set(MAIN_SRCS "main.cpp")
set(MAIN_REQUIRES esp_matter neopixel)

if(CONFIG_USE_FAKE_TEMP_SENSOR)
    list(APPEND MAIN_SRCS "fake_temp_sensor.c")
else()
    list(APPEND MAIN_SRCS "ds18b20_reader.c")
    list(APPEND MAIN_REQUIRES espressif__onewire_bus espressif__ds18b20)
endif()

idf_component_register(
    SRCS ${MAIN_SRCS}
    INCLUDE_DIRS "."
    REQUIRES ${MAIN_REQUIRES}
    PRIV_REQUIRES driver nvs_flash esp_timer
)
