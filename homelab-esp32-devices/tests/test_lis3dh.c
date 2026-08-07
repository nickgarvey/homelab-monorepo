#include "unity.h"
#include "esp_idf_mocks.h"
#include "test_helpers.h"
#include "lis3dh.h"

/* LIS3DH register addresses */
#define LIS3DH_ADDR    0x18
#define REG_WHO_AM_I   0x0F
#define REG_CTRL_REG1  0x20
#define REG_CTRL_REG3  0x22
#define REG_CTRL_REG4  0x23
#define REG_INT1_CFG   0x30
#define REG_INT1_THS   0x32
#define REG_INT1_DUR   0x33
#define REG_OUT_X_L    0x28
#define REG_OUT_X_H    0x29
#define REG_OUT_Y_L    0x2A
#define REG_OUT_Y_H    0x2B
#define REG_OUT_Z_L    0x2C
#define REG_OUT_Z_H    0x2D

static void setup(void)
{
    i2c_mock_reset();
    lis3dh_reset();
}

/* ---- Init tests -------------------------------------------------------- */

static void test_lis3dh_init_reads_who_am_i(void)
{
    setup();
    i2c_mock_set_register(LIS3DH_ADDR, REG_WHO_AM_I, 0x33);

    lis3dh_config_t cfg = { .sda_gpio = 19, .scl_gpio = 18, .addr = LIS3DH_ADDR };
    TEST_ASSERT_TRUE(lis3dh_init(&cfg));
}

static void test_lis3dh_init_fails_wrong_who_am_i(void)
{
    setup();
    i2c_mock_set_register(LIS3DH_ADDR, REG_WHO_AM_I, 0xFF);

    lis3dh_config_t cfg = { .sda_gpio = 19, .scl_gpio = 18, .addr = LIS3DH_ADDR };
    TEST_ASSERT_FALSE(lis3dh_init(&cfg));
}

static void test_lis3dh_init_fails_i2c_error(void)
{
    setup();
    i2c_mock_set_transmit_receive_result(ESP_FAIL);

    lis3dh_config_t cfg = { .sda_gpio = 19, .scl_gpio = 18, .addr = LIS3DH_ADDR };
    TEST_ASSERT_FALSE(lis3dh_init(&cfg));
}

static void test_lis3dh_init_configures_ctrl_regs(void)
{
    setup();
    i2c_mock_set_register(LIS3DH_ADDR, REG_WHO_AM_I, 0x33);

    lis3dh_config_t cfg = { .sda_gpio = 19, .scl_gpio = 18, .addr = LIS3DH_ADDR };
    lis3dh_init(&cfg);

    /* CTRL_REG1: 100Hz ODR, all axes enabled = 0x57 */
    TEST_ASSERT_EQUAL_HEX8(0x57, i2c_mock_get_register(LIS3DH_ADDR, REG_CTRL_REG1));
    /* CTRL_REG4: +/-2g, high-resolution = 0x08 */
    TEST_ASSERT_EQUAL_HEX8(0x08, i2c_mock_get_register(LIS3DH_ADDR, REG_CTRL_REG4));
}

/* ---- Read tests -------------------------------------------------------- */

static void test_lis3dh_read_accel_returns_xyz(void)
{
    setup();
    i2c_mock_set_register(LIS3DH_ADDR, REG_WHO_AM_I, 0x33);

    lis3dh_config_t cfg = { .sda_gpio = 19, .scl_gpio = 18, .addr = LIS3DH_ADDR };
    lis3dh_init(&cfg);

    /* Set raw values: X=0x0100 (256), Y=0xFF00 (-256), Z=0x4000 (16384) */
    i2c_mock_set_register(LIS3DH_ADDR, REG_OUT_X_L, 0x00);
    i2c_mock_set_register(LIS3DH_ADDR, REG_OUT_X_H, 0x01);
    i2c_mock_set_register(LIS3DH_ADDR, REG_OUT_Y_L, 0x00);
    i2c_mock_set_register(LIS3DH_ADDR, REG_OUT_Y_H, 0xFF);
    i2c_mock_set_register(LIS3DH_ADDR, REG_OUT_Z_L, 0x00);
    i2c_mock_set_register(LIS3DH_ADDR, REG_OUT_Z_H, 0x40);

    lis3dh_accel_t accel;
    TEST_ASSERT_TRUE(lis3dh_read_accel(&accel));

    /* At +/-2g in high-res mode (12-bit): 1 LSB = 1 mg
     * Raw int16 values are left-justified: actual = raw >> 4
     * X: 0x0100 >> 4 = 16 → 16 mg
     * Y: 0xFF00 (signed -256) >> 4 = -16 → -16 mg
     * Z: 0x4000 >> 4 = 1024 → 1024 mg (~1g) */
    TEST_ASSERT_EQUAL_INT16(16, accel.x_mg);
    TEST_ASSERT_EQUAL_INT16(-16, accel.y_mg);
    TEST_ASSERT_EQUAL_INT16(1024, accel.z_mg);
}

static void test_lis3dh_read_accel_fails_before_init(void)
{
    setup();
    lis3dh_accel_t accel;
    TEST_ASSERT_FALSE(lis3dh_read_accel(&accel));
}

/* ---- Motion interrupt tests -------------------------------------------- */

static void test_lis3dh_configure_motion_interrupt(void)
{
    setup();
    i2c_mock_set_register(LIS3DH_ADDR, REG_WHO_AM_I, 0x33);

    lis3dh_config_t cfg = { .sda_gpio = 19, .scl_gpio = 18, .addr = LIS3DH_ADDR };
    lis3dh_init(&cfg);

    TEST_ASSERT_TRUE(lis3dh_configure_motion_interrupt(100, 10));

    /* CTRL_REG3: I1_AOI1 = 0x40 */
    TEST_ASSERT_EQUAL_HEX8(0x40, i2c_mock_get_register(LIS3DH_ADDR, REG_CTRL_REG3));
    /* INT1_CFG: OR combination of high events on XYZ = 0x2A */
    TEST_ASSERT_EQUAL_HEX8(0x2A, i2c_mock_get_register(LIS3DH_ADDR, REG_INT1_CFG));
    /* INT1_THS and INT1_DUR should be set (exact values depend on scaling) */
    TEST_ASSERT_NOT_EQUAL(0, i2c_mock_get_register(LIS3DH_ADDR, REG_INT1_THS));
    TEST_ASSERT_NOT_EQUAL(0, i2c_mock_get_register(LIS3DH_ADDR, REG_INT1_DUR));
}

/* ---- Reset test -------------------------------------------------------- */

static void test_lis3dh_reset_clears_state(void)
{
    setup();
    i2c_mock_set_register(LIS3DH_ADDR, REG_WHO_AM_I, 0x33);

    lis3dh_config_t cfg = { .sda_gpio = 19, .scl_gpio = 18, .addr = LIS3DH_ADDR };
    lis3dh_init(&cfg);

    lis3dh_reset();

    /* After reset, read should fail (not initialized) */
    lis3dh_accel_t accel;
    TEST_ASSERT_FALSE(lis3dh_read_accel(&accel));

    /* Re-init should work */
    i2c_mock_set_register(LIS3DH_ADDR, REG_WHO_AM_I, 0x33);
    TEST_ASSERT_TRUE(lis3dh_init(&cfg));
}

/* ---- Suite entry point ------------------------------------------------- */

void run_lis3dh_tests(void)
{
    RUN_TEST(test_lis3dh_init_reads_who_am_i);
    RUN_TEST(test_lis3dh_init_fails_wrong_who_am_i);
    RUN_TEST(test_lis3dh_init_fails_i2c_error);
    RUN_TEST(test_lis3dh_init_configures_ctrl_regs);
    RUN_TEST(test_lis3dh_read_accel_returns_xyz);
    RUN_TEST(test_lis3dh_read_accel_fails_before_init);
    RUN_TEST(test_lis3dh_configure_motion_interrupt);
    RUN_TEST(test_lis3dh_reset_clears_state);
}
