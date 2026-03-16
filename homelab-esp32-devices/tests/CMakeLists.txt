cmake_minimum_required(VERSION 3.16)
project(homelab_tests C)

set(CMAKE_C_STANDARD 11)

include(FetchContent)
FetchContent_Declare(
    unity
    GIT_REPOSITORY https://github.com/ThrowTheSwitch/Unity.git
    GIT_TAG        v2.6.0
)
FetchContent_MakeAvailable(unity)

set(COMMON_SRC
    ${CMAKE_CURRENT_SOURCE_DIR}/../common/src/wifi_manager.c
    ${CMAKE_CURRENT_SOURCE_DIR}/../common/src/ha_client.c
    ${CMAKE_CURRENT_SOURCE_DIR}/../common/src/neopixel.c
    ${CMAKE_CURRENT_SOURCE_DIR}/../common/src/openthread_manager.c
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
    ${CMAKE_CURRENT_SOURCE_DIR}/test_main.c
    ${CMAKE_CURRENT_SOURCE_DIR}/../devices/freezer-temp-sensor/main/main.c
    ${CMAKE_CURRENT_SOURCE_DIR}/test_runner.c
)

add_executable(run_tests
    ${COMMON_SRC}
    ${MOCK_SRC}
    ${TEST_SRC}
)

target_include_directories(run_tests PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/../common/include
    ${CMAKE_CURRENT_SOURCE_DIR}/mocks
    ${unity_SOURCE_DIR}/src
)

target_link_libraries(run_tests PRIVATE unity)

# Expose the Unity include path used by FetchContent
target_include_directories(run_tests PRIVATE ${unity_SOURCE_DIR}/src)

enable_testing()
add_test(NAME unit_tests COMMAND run_tests)
