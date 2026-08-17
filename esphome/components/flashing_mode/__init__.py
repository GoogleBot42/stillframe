"""Software-forced entry into the ESP32 ROM serial bootloader.

Reflashing an ESP32 over USB normally needs GPIO0 pulled low while the chip
comes out of reset — either by hand (hold BOOT, tap RESET) or by the
auto-download circuit the USB-UART bridge drives with DTR/RTS. Neither is
available on every board: the Soldered Inkplate 13 Spectra has no BOOT button,
does not break IO0 out at all, and its auto-download circuit is marginal (the
CH340K's RTS# has only weak high-side drive), while browsers cannot even
produce esptool's atomic DTR/RTS transition.

The escape hatch is in the chip itself: the ROM checks a bit in the always-on
RTC domain on every boot and, when it is set, starts the serial downloader
instead of the application. Setting that bit and rebooting therefore puts the
chip into a flashable state with no strapping pin, button or timing involved.
This is the same mechanism ESP-IDF and arduino-esp32 use internally to
implement "reboot into the bootloader" over USB.

Naming this component in a config does two things: it compiles
esphome::flashing_mode::enter_flashing_mode() into the build for lambdas to
call (see the "Enter Flashing Mode" button and the wake-button hold in
esphome/common.yaml), and it registers a component that clears the bit on every
boot, so the request can never outlive the flash session that wanted it.
"""

import esphome.codegen as cg
import esphome.config_validation as cv
from esphome.const import CONF_ID

CODEOWNERS = ["@GoogleBot42"]

flashing_mode_ns = cg.esphome_ns.namespace("flashing_mode")
FlashingModeComponent = flashing_mode_ns.class_("FlashingModeComponent", cg.Component)

CONFIG_SCHEMA = cv.Schema(
    {
        cv.GenerateID(): cv.declare_id(FlashingModeComponent),
    }
).extend(cv.COMPONENT_SCHEMA)


async def to_code(config):
    var = cg.new_Pvariable(config[CONF_ID])
    await cg.register_component(var, config)
