# Decrypt SOPS secrets into the build tree so plaintext never sits in source.
# CMAKE_CURRENT_LIST_DIR = devices/freezer-temp-sensor/main
# Project root is one level up.
set(DEVICE_ROOT "${CMAKE_CURRENT_LIST_DIR}/..")
set(SOPS_FILE   "${DEVICE_ROOT}/secrets/thread_auth.sops.yaml")
set(OUT_DIR     "${CMAKE_BINARY_DIR}/generated")
include("${DEVICE_ROOT}/cmake/generate_secrets.cmake")

set(MAIN_SRCS "main.cpp")
set(MAIN_REQUIRES esp_matter neopixel)

if(CONFIG_USE_FAKE_TEMP_SENSOR)
    list(APPEND MAIN_SRCS "fake_temp_sensor.c")
else()
    list(APPEND MAIN_REQUIRES espressif__onewire_bus espressif__ds18b20)
endif()

idf_component_register(
    SRCS ${MAIN_SRCS}
    INCLUDE_DIRS "." "${CMAKE_BINARY_DIR}/generated"
    REQUIRES ${MAIN_REQUIRES}
    PRIV_REQUIRES driver nvs_flash esp_timer
)
