idf_component_register(
    SRCS "main.c"
    INCLUDE_DIRS "." "${CMAKE_SOURCE_DIR}/include"
    EMBED_TXTFILES "certs/ca_bundle.pem"
    REQUIRES common
    PRIV_REQUIRES esp_timer json driver nvs_flash
)
