#include "unity.h"

void run_wifi_manager_tests(void);
void run_ha_client_tests(void);
void run_neopixel_tests(void);
void run_openthread_manager_tests(void);
void run_main_tests(void);
void run_fake_temp_sensor_tests(void);

void setUp(void)    {}
void tearDown(void) {}

int main(void)
{
    UNITY_BEGIN();
    run_wifi_manager_tests();
    run_ha_client_tests();
    run_neopixel_tests();
    run_openthread_manager_tests();
    run_fake_temp_sensor_tests();
    run_main_tests();
    return UNITY_END();
}
