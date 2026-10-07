cmake_minimum_required(VERSION 3.16)
project(homelab_tests C)

set(CMAKE_C_STANDARD 11)

add_library(unity STATIC ${UNITY_SOURCE_DIR}/src/unity.c)
target_include_directories(unity PUBLIC ${UNITY_SOURCE_DIR}/src)

set(COMMON_SRC
    ${CMAKE_CURRENT_SOURCE_DIR}/../components/wifi_manager/src/wifi_manager.c
    ${CMAKE_CURRENT_SOURCE_DIR}/../components/ha_client/src/ha_client.c
    ${CMAKE_CURRENT_SOURCE_DIR}/../components/neopixel/src/neopixel.c
    ${CMAKE_CURRENT_SOURCE_DIR}/../components/openthread_manager/src/openthread_manager.c
)

set(MOCK_SRC
    ${CMAKE_CURRENT_SOURCE_DIR}/mocks/wifi_mocks.c
    ${CMAKE_CURRENT_SOURCE_DIR}/mocks/http_mocks.c
    ${CMAKE_CURRENT_SOURCE_DIR}/mocks/neopixel_mocks.c
    ${CMAKE_CURRENT_SOURCE_DIR}/mocks/openthread_mocks.c
    ${CMAKE_CURRENT_SOURCE_DIR}/mocks/ds18b20_mocks.c
    ${CMAKE_CURRENT_SOURCE_DIR}/mocks/system_mocks.c
)

set(TEST_SRC
    ${CMAKE_CURRENT_SOURCE_DIR}/test_wifi_manager.c
    ${CMAKE_CURRENT_SOURCE_DIR}/test_ha_client.c
    ${CMAKE_CURRENT_SOURCE_DIR}/test_neopixel.c
    ${CMAKE_CURRENT_SOURCE_DIR}/test_openthread_manager.c
    ${CMAKE_CURRENT_SOURCE_DIR}/test_fake_temp_sensor.c
    ${CMAKE_CURRENT_SOURCE_DIR}/test_ds18b20_reader.c
    ${CMAKE_CURRENT_SOURCE_DIR}/../devices/freezer-temp-sensor/main/fake_temp_sensor.c
    ${CMAKE_CURRENT_SOURCE_DIR}/../devices/freezer-temp-sensor/main/ds18b20_reader.c
    ${CMAKE_CURRENT_SOURCE_DIR}/test_runner.c
)

add_executable(run_tests
    ${COMMON_SRC}
    ${MOCK_SRC}
    ${TEST_SRC}
)

target_include_directories(run_tests PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/../components/wifi_manager/include
    ${CMAKE_CURRENT_SOURCE_DIR}/../components/ha_client/include
    ${CMAKE_CURRENT_SOURCE_DIR}/../components/neopixel/include
    ${CMAKE_CURRENT_SOURCE_DIR}/../components/openthread_manager/include
    ${CMAKE_CURRENT_SOURCE_DIR}/../devices/freezer-temp-sensor/main
    ${CMAKE_CURRENT_SOURCE_DIR}/mocks
)

target_link_libraries(run_tests PRIVATE unity m)

enable_testing()
add_test(NAME unit_tests COMMAND run_tests)
