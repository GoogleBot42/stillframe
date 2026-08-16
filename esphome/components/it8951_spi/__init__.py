import esphome.codegen as cg
import esphome.config_validation as cv
from esphome import pins
from esphome.components import eink_frame, spi
from esphome.const import CONF_ID, CONF_RESET_PIN

DEPENDENCIES = ["spi"]
AUTO_LOAD = ["spi", "eink_frame"]

CONF_HRDY_PIN = "hrdy_pin"

it8951_spi_ns = cg.esphome_ns.namespace("it8951_spi")
IT8951SPI = it8951_spi_ns.class_(
    "IT8951SPI", cg.Component, spi.SPIDevice, eink_frame.EinkFrameDisplay
)

CONFIG_SCHEMA = (
    cv.Schema(
        {
            cv.GenerateID(): cv.declare_id(IT8951SPI),
            cv.Required(CONF_RESET_PIN): pins.gpio_output_pin_schema,
            cv.Required(CONF_HRDY_PIN): pins.gpio_input_pin_schema,
        }
    )
    .extend(eink_frame.eink_frame_schema(width=1872, height=1404))
    .extend(cv.COMPONENT_SCHEMA)
    # cs_pin comes from the SPI device schema; enable()/disable() drive it
    .extend(spi.spi_device_schema(cs_pin_required=True))
)


async def to_code(config):
    var = cg.new_Pvariable(config[CONF_ID])
    await cg.register_component(var, config)
    await spi.register_spi_device(var, config)
    await eink_frame.register_eink_frame(var, config)

    for key, setter in [
        (CONF_RESET_PIN, var.set_reset_pin),
        (CONF_HRDY_PIN, var.set_hrdy_pin),
    ]:
        pin = await cg.gpio_pin_expression(config[key])
        cg.add(setter(pin))
