idf_component_register(
    SRCS "src/neopixel.c"
    INCLUDE_DIRS "include"
    REQUIRES driver led_strip
)
