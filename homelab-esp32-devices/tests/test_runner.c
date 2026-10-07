#include "unity.h"

void run_wifi_manager_tests(void);
void run_ha_client_tests(void);
void run_neopixel_tests(void);
void run_openthread_manager_tests(void);
void run_fake_temp_sensor_tests(void);
void run_ds18b20_reader_tests(void);
void run_lis3dh_tests(void);
void run_max17048_tests(void);
void run_vibration_monitor_tests(void);
void run_status_led_tests(void);
void run_sht40_tests(void);
void run_boot_action_tests(void);

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
    run_ds18b20_reader_tests();
    run_lis3dh_tests();
    run_max17048_tests();
    run_vibration_monitor_tests();
    run_status_led_tests();
    run_sht40_tests();
    run_boot_action_tests();
    return UNITY_END();
}
