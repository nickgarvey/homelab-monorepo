idf_component_register(
    SRCS "main.c"
    INCLUDE_DIRS "." "${CMAKE_SOURCE_DIR}/include"
    EMBED_TXTFILES "certs/ca_bundle.pem"
    REQUIRES openthread_manager ha_client espressif__onewire_bus espressif__ds18b20
    PRIV_REQUIRES driver nvs_flash esp_timer
)
