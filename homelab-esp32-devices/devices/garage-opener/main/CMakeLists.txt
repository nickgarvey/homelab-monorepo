idf_component_register(
    SRCS "main.c"
    INCLUDE_DIRS "." "${CMAKE_SOURCE_DIR}/include"
    EMBED_TXTFILES "certs/ca_bundle.pem"
    REQUIRES neopixel wifi_manager ha_client
    PRIV_REQUIRES esp_timer json driver nvs_flash
)
