/*
 * CHIPProjectConfig.h — device-specific CHIP/Matter overrides.
 *
 * Maps IDF Kconfig options to CHIP compile-time defines so that
 * CONFIG_DEVICE_PRODUCT_NAME / CONFIG_DEVICE_VENDOR_NAME (set via sdkconfig.defaults
 * or -DSDKCONFIG_DEFAULTS at build time) appear in the Matter Basic Information
 * cluster as reported to Home Assistant.
 *
 * Referenced by CONFIG_CHIP_PROJECT_CONFIG in sdkconfig.defaults.
 */
#pragma once

/* Build-time device identification — set product/vendor name from Kconfig.
 * CONFIG_DEVICE_PRODUCT_NAME and CONFIG_DEVICE_VENDOR_NAME are string Kconfig
 * options defined in main/Kconfig.projbuild; they expand to quoted string
 * literals here (e.g. "Freezer Temp Sensor"). */
#define CHIP_DEVICE_CONFIG_DEVICE_PRODUCT_NAME CONFIG_DEVICE_PRODUCT_NAME
#define CHIP_DEVICE_CONFIG_DEVICE_VENDOR_NAME  CONFIG_DEVICE_VENDOR_NAME

/* Hardware version string — override SDK default of "TEST_VERSION". */
#define CHIP_DEVICE_CONFIG_DEFAULT_DEVICE_HARDWARE_VERSION_STRING "v1.0"
