# Laundry vibration sensor netlist (SKiDL): ESP32-C6-MINI-1, ADXL362, SHT40, 2xAA through a TPS63900.
# Run in `nix develop .#pcb`: python3 circuit.py  ->  laundry-sensor.net, ERC report on stdout.
from pathlib import Path

import builtins

import skidl
from skidl import ERC, Net, Part, Pin, generate_netlist, set_default_tool

KICAD9, SKIDL = skidl.KICAD9, skidl.SKIDL
NC = builtins.NC  # SKiDL publishes its no-connect net as a builtin

HERE = Path(__file__).parent
set_default_tool(KICAD9)

# --- footprints -------------------------------------------------------------
R0402 = "Resistor_SMD:R_0402_1005Metric"
C0402 = "Capacitor_SMD:C_0402_1005Metric"
C0603 = "Capacitor_SMD:C_0603_1608Metric"
C0805 = "Capacitor_SMD:C_0805_2012Metric"

# LCSC numbers for JLC assembly.
LCSC = {
    "100nF": "C1525", "1uF": "C52923", "10uF": "C19702",
    "22uF_0603": "C59461", "22uF_0805": "C45783",
    "0": "C17168", "1k": "C11702", "5.1k": "C25905", "10k": "C25744",
    "16.2k": "C27176", "36.5k": "C25887", "100k": "C25741", "10M": "C144806",
}


def R(value):
    return Part("Device", "R", value=value, footprint=R0402,
                fields={"LCSC": LCSC[value]})


def C(value, fp=C0402, lcsc_key=None):
    return Part("Device", "C", value=value.replace("_0603", "").replace("_0805", ""),
                footprint=fp, fields={"LCSC": LCSC[lcsc_key or value]})


def TP(name):
    return Part("Connector", "TestPoint", value=name, footprint="TestPoint:TestPoint_Pad_D1.0mm")


# --- nets -------------------------------------------------------------------
gnd = Net("GND")
v3 = Net("+3V3")
vin = Net("VIN")          # converter input: battery (via Q1) or USB (via D1)
vbat = Net("VBAT")        # battery + after the PPK2 jumper
bat_p = Net("BAT+")       # battery holder + terminal
vbus = Net("VBUS")
for n in (gnd, v3, vin, vbat, vbus):
    n.drive = skidl.POWER

# --- battery, PPK2 jumper, power path ---------------------------------------
bt = Part("Device", "Battery", value="2xAA", footprint="Battery:BatteryHolder_Keystone_2462_2xAA",
          fields={"LCSC": "C3029547"})
bt["+"] += bat_p
bt["-"] += gnd

j_ppk = Part("Connector_Generic", "Conn_01x02", value="PPK2", ref="J2",
             footprint="Connector_PinHeader_2.54mm:PinHeader_1x02_P2.54mm_Vertical",
             fields={"LCSC": "C492401"})
j_ppk[1] += bat_p
j_ppk[2] += vbat

# Q1: P-MOSFET load switch. Off while VBUS is present (gate pulled up to VBUS), so USB never
# drives current into the cells; on otherwise (gate pulled to GND). Blocks a reversed battery.
q1 = Part("Transistor_FET", "AO3401A", footprint="Package_TO_SOT_SMD:SOT-23", fields={"LCSC": "C15127"})
q1["D"] += vbat
q1["S"] += vin
q1["G"] += vbus
r_gate = R("100k")
r_gate[1, 2] += vbus, gnd

# D1: USB into VIN. A silicon diode: a Schottky's reverse leakage would drain the cells in sleep.
d1 = Part("Diode", "1N4148WS", footprint="Diode_SMD:D_SOD-323", fields={"LCSC": "C2128"})
d1["A"] += vbus
d1["K"] += vin

# --- TPS63900 buck-boost, 3.3 V out -----------------------------------------
# Footprint without the 0.2 mm thermal vias (below JLC standard drill); low current, no heat to spread.
u_reg = Part("Regulator_Switching", "TPS63900", fields={"LCSC": "C1518762"},
             footprint="Package_SON:WSON-10-1EP_2.5x2.5mm_P0.5mm_EP1.2x2mm")
u_reg["VIN"] += vin
u_reg["EN"] += vin
u_reg["SEL"] += vin            # SEL high: VO(2) from CFG1
u_reg["GND"] += gnd            # pin 8 and the exposed pad
r_cfg1 = R("36.5k")            # CFG1 36.5k -> VO(2) = 3.3 V (datasheet Table 7-2)
r_cfg2 = R("0")                # CFG2 0R   -> no input current limit
r_cfg3 = R("16.2k")            # CFG3 16.2k -> VO(1) = 3.3 V as well (Table 7-3)
r_cfg1[1, 2] += u_reg["CFG1"], gnd
r_cfg2[1, 2] += u_reg["CFG2"], gnd
r_cfg3[1, 2] += u_reg["CFG3"], gnd
l1 = Part("Device", "L", value="2.2uH", footprint="Inductor_SMD:L_1008_2520Metric",
          fields={"LCSC": "C1017131"})
l1[1, 2] += u_reg["LX1"], u_reg["LX2"]
u_reg["VOUT"] += v3
c_in = C("10uF", C0603)
c_in_bulk = C("22uF_0805", C0805)
c_out = C("22uF_0603", C0603)
for c in (c_in, c_in_bulk):
    c[1, 2] += vin, gnd
c_out[1, 2] += v3, gnd

# Battery voltage sense: 10M/10M, 0.16 uA at 3.2 V. 100 nF holds the node for the ADC sample.
r_vb_top, r_vb_bot = R("10M"), R("10M")
vbat_sense = Net("VBAT_SENSE")
r_vb_top[1, 2] += vbat, vbat_sense
r_vb_bot[1, 2] += vbat_sense, gnd
c_vb = C("100nF")
c_vb[1, 2] += vbat_sense, gnd

# --- ESP32-C6-MINI-1 ---------------------------------------------------------
u_mcu = Part("RF_Module", "ESP32-C6-MINI-1", fields={"LCSC": "C5736265"})
u_mcu["3V3"] += v3
u_mcu["GND"] += gnd
c_mcu_bulk = C("22uF_0805", C0805)
c_mcu = C("100nF")
c_mcu_bulk[1, 2] += v3, gnd
c_mcu[1, 2] += v3, gnd

# EN: RC reset delay (module datasheet: 10k / 1uF) and RESET button.
en = Net("EN")
u_mcu["EN"] += en
r_en, c_en = R("10k"), C("1uF")
r_en[1, 2] += v3, en
c_en[1, 2] += en, gnd


def button(name):
    return Part("Switch", "SW_Push", value=name, footprint="Button_Switch_SMD:SW_SPST_TS-1088-xR020",
                fields={"LCSC": "C720477"})


sw_reset, sw_boot, sw_user = button("RESET"), button("BOOT"), button("USER")
sw_reset[1, 2] += en, gnd

# GPIO9 strap: BOOT button to GND (internal pull-up). GPIO8 strap: pulled high.
u_mcu["IO9"] += Net("BOOT")
sw_boot[1, 2] += u_mcu["IO9"], gnd
r_io8 = R("10k")
r_io8[1, 2] += v3, u_mcu["IO8"]

# USER button on LP GPIO3, wakes from deep sleep.
user_btn = Net("USER_BTN")
u_mcu["IO3"] += user_btn
r_user = R("100k")
r_user[1, 2] += v3, user_btn
sw_user[1, 2] += user_btn, gnd

u_mcu["IO2"] += vbat_sense

# LED on GPIO14.
led = Part("Device", "LED", value="red", footprint="LED_SMD:LED_0603_1608Metric", fields={"LCSC": "C2286"})
r_led = R("1k")
led_a = Net("LED_A")
r_led[1, 2] += u_mcu["IO14"], led_a
led["A"] += led_a
led["K"] += gnd

# --- USB-C (flashing and logs only) ------------------------------------------
j_usb = Part("Connector", "USB_C_Receptacle_USB2.0_16P", ref="J1",
             footprint="Connector_USB:USB_C_Receptacle_HRO_TYPE-C-31-M-12", fields={"LCSC": "C165948"})
j_usb["VBUS"] += vbus
j_usb["GND"] += gnd
j_usb["SHIELD"] += gnd
usb_dp, usb_dm = Net("USB_D+"), Net("USB_D-")
j_usb["D+"] += usb_dp
j_usb["D-"] += usb_dm
u_mcu["IO13"] += usb_dp
u_mcu["IO12"] += usb_dm
j_usb["SBU1", "SBU2"] += NC
r_cc1, r_cc2 = R("5.1k"), R("5.1k")
r_cc1[1, 2] += j_usb["CC1"], gnd
r_cc2[1, 2] += j_usb["CC2"], gnd

# --- ADXL362 (SPI) -----------------------------------------------------------
P = Pin.types
u_acc = Part(
    name="ADXL362", tool=SKIDL, ref_prefix="U", dest=skidl.NETLIST,
    footprint="laundry:ADXL362_LGA-16_3x3.25mm_P0.5mm", fields={"LCSC": "C59641"},
    pins=[
        Pin(num=1, name="VDDIO", func=P.PWRIN), Pin(num=2, name="NC1", func=P.NOCONNECT),
        Pin(num=3, name="RES3", func=P.PASSIVE), Pin(num=4, name="SCLK", func=P.INPUT),
        Pin(num=5, name="RES5", func=P.PASSIVE), Pin(num=6, name="MOSI", func=P.INPUT),
        Pin(num=7, name="MISO", func=P.OUTPUT), Pin(num=8, name="CS", func=P.INPUT),
        Pin(num=9, name="INT2", func=P.OUTPUT), Pin(num=10, name="RES10", func=P.PASSIVE),
        Pin(num=11, name="INT1", func=P.OUTPUT), Pin(num=12, name="GND12", func=P.PWRIN),
        Pin(num=13, name="GND13", func=P.PWRIN), Pin(num=14, name="VS", func=P.PWRIN),
        Pin(num=15, name="NC15", func=P.NOCONNECT), Pin(num=16, name="GND16", func=P.PWRIN),
    ],
)
u_acc["VDDIO", "VS"] += v3
u_acc["GND12", "GND13", "GND16"] += gnd
u_acc["RES3", "RES5", "RES10"] += gnd   # datasheet: reserved, leave open or tie to GND
c_acc_vs, c_acc_io = C("100nF"), C("100nF")
c_acc_vs[1, 2] += v3, gnd
c_acc_io[1, 2] += v3, gnd

spi = {name: Net(f"ACC_{name}") for name in ("CS", "SCLK", "MOSI", "MISO", "INT1", "INT2")}
for name, net in spi.items():
    u_acc[name] += net
u_mcu["IO18"] += spi["CS"]
u_mcu["IO19"] += spi["SCLK"]
u_mcu["IO20"] += spi["MOSI"]
u_mcu["IO21"] += spi["MISO"]
u_mcu["IO0"] += spi["INT1"]   # activity/inactivity: deep-sleep wake (LP GPIO)
u_mcu["IO1"] += spi["INT2"]   # FIFO watermark: light-sleep wake
r_cs = R("100k")              # holds CS high while the C6 sleeps
r_cs[1, 2] += v3, spi["CS"]

# --- SHT40 (I2C 0x44) --------------------------------------------------------
u_rh = Part("Sensor_Humidity", "SHT4x", value="SHT40-AD1B", fields={"LCSC": "C2909890"})
sda, scl = Net("I2C_SDA"), Net("I2C_SCL")
u_rh["VDD"] += v3
u_rh["VSS"] += gnd
u_rh["SDA"] += sda
u_rh["SCL"] += scl
u_mcu["IO22"] += sda
u_mcu["IO23"] += scl
r_sda, r_scl = R("10k"), R("10k")
r_sda[1, 2] += v3, sda
r_scl[1, 2] += v3, scl
c_rh = C("100nF")
c_rh[1, 2] += v3, gnd

# --- test pads ---------------------------------------------------------------
for name, net in (("TX", Net("UART_TX")), ("RX", Net("UART_RX")), ("3V3", v3), ("GND", gnd),
                  ("VBAT", vbat)):
    TP(name)[1] += net
u_mcu["TXD0"] += Net.fetch("UART_TX")
u_mcu["RXD0"] += Net.fetch("UART_RX")

# Unused module pins (GPIO4-7 are JTAG pins, unused with USB JTAG; GPIO15 floats = default JTAG source).
u_mcu["IO4", "IO5", "IO6", "IO7", "IO15"] += NC
for p in u_mcu.get_pins("NC"):
    p += NC

# Stable tags keep each footprint tied to its part across regenerations of the netlist.
for part in builtins.default_circuit.parts:
    part.tag = part.ref

if __name__ == "__main__":
    ERC()
    generate_netlist(file_=str(HERE / "laundry-sensor.net"))
