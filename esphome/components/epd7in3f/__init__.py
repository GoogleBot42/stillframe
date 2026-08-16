import esphome.codegen as cg
import esphome.config_validation as cv
from esphome import pins
from esphome.components import eink_frame, spi
from esphome.const import CONF_BUSY_PIN, CONF_DC_PIN, CONF_ID, CONF_RESET_PIN

DEPENDENCIES = ["spi"]
AUTO_LOAD = ["spi", "eink_frame"]

epd7in3f_ns = cg.esphome_ns.namespace("epd7in3f")
EPD7IN3F = epd7in3f_ns.class_(
    "EPD7IN3F", cg.Component, spi.SPIDevice, eink_frame.EinkFrameDisplay
)

CONFIG_SCHEMA = (
    cv.Schema(
        {
            cv.GenerateID(): cv.declare_id(EPD7IN3F),
            cv.Required(CONF_DC_PIN): pins.gpio_output_pin_schema,
            cv.Required(CONF_RESET_PIN): pins.gpio_output_pin_schema,
            cv.Required(CONF_BUSY_PIN): pins.gpio_input_pin_schema,
        }
    )
    .extend(eink_frame.eink_frame_schema(width=800, height=480))
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
        (CONF_DC_PIN, var.set_dc_pin),
        (CONF_RESET_PIN, var.set_reset_pin),
        (CONF_BUSY_PIN, var.set_busy_pin),
    ]:
        pin = await cg.gpio_pin_expression(config[key])
        cg.add(setter(pin))
