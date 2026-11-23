/*
 * Board I/O: the laundry sensor PCB (hardware/laundry-sensor) or, for development, a bare
 * Adafruit ESP32-C6 Feather with the sensors simulated.
 */
#pragma once

#include <stdbool.h>
#include <stdint.h>

#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

#include "sht40.h"
#include "vibration_monitor.h"

#ifdef __cplusplus
extern "C" {
#endif

/* Task notification bits sent by the interrupt handlers. */
#define BOARD_NOTIFY_ACCEL_WAKE  (1u << 0)   /* LIS2DW12 INT1: wake-up */
#define BOARD_NOTIFY_ACCEL_FIFO  (1u << 1)   /* LIS2DW12 INT2: FIFO threshold */
#define BOARD_NOTIFY_BUTTON      (1u << 2)

/* Samples per FIFO batch at 50 Hz: one batch every 0.5 s. */
#define BOARD_ACCEL_HZ     50
#define BOARD_ACCEL_BATCH  25

/* GPIOs, LED, button, buses; the accelerometer is left powered down. */
bool board_init(void);

void board_led(bool on);
void board_led_blink(int times, int on_ms, int off_ms);

/* Button interrupts go to this task as BOARD_NOTIFY_BUTTON. Light sleep wakes on a press. */
void board_button_attach(TaskHandle_t task);
bool board_button_pressed(void);
/* Re-enable the button interrupt after a press has been handled. */
void board_button_rearm(void);

/* Accelerometer interrupts go to this task. */
void board_accel_attach(TaskHandle_t task);
/* Wait for wake-ups at threshold_mg (12.5 Hz, about 1 uA). */
bool board_accel_arm(uint16_t threshold_mg);
/* Stream at BOARD_ACCEL_HZ, BOARD_ACCEL_BATCH samples per FIFO interrupt. */
bool board_accel_stream(void);
/* Read the samples waiting in the FIFO; re-enables the interrupt. Returns the count, -1 on error. */
int board_accel_read(vm_sample_t *out, int max);
/* Acknowledge a wake-up interrupt; re-enables it. */
void board_accel_ack_wake(void);
/* Feather only: start or stop the simulated wash. */
void board_fake_wash(bool on);
bool board_fake_washing(void);

bool board_env_read(sht40_reading_t *out);
/* Battery voltage in mV, 0 if unknown. */
uint32_t board_battery_mv(void);

#ifdef __cplusplus
}
#endif
