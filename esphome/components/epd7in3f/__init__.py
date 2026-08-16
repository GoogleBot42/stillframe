import esphome.codegen as cg
import esphome.config_validation as cv
from esphome import pins
from esphome.components import spi
from esphome.const import CONF_DC_PIN, CONF_ID, CONF_RESET_PIN

DEPENDENCIES = ["spi"]
AUTO_LOAD = ["spi"]

CONF_BUSY_PIN = "busy_pin"
CONF_WIDTH = "width"
CONF_HEIGHT = "height"
CONF_FLIP_VERTICAL = "flip_vertical"
CONF_FLIP_HORIZONTAL = "flip_horizontal"

epd7in3f_ns = cg.esphome_ns.namespace("epd7in3f")
EPD7IN3F = epd7in3f_ns.class_("EPD7IN3F", cg.Component, spi.SPIDevice)

CONFIG_SCHEMA = (
    cv.Schema(
        {
            cv.GenerateID(): cv.declare_id(EPD7IN3F),
            cv.Required(CONF_DC_PIN): pins.gpio_output_pin_schema,
            cv.Required(CONF_RESET_PIN): pins.gpio_output_pin_schema,
            cv.Required(CONF_BUSY_PIN): pins.gpio_input_pin_schema,
            cv.Optional(CONF_WIDTH, default=800): cv.int_range(min=1),
            cv.Optional(CONF_HEIGHT, default=480): cv.int_range(min=1),
            cv.Optional(CONF_FLIP_VERTICAL, default=False): cv.boolean,
            cv.Optional(CONF_FLIP_HORIZONTAL, default=False): cv.boolean,
        }
    )
    .extend(cv.COMPONENT_SCHEMA)
    # cs_pin comes from the SPI device schema; enable()/disable() drive it
    .extend(spi.spi_device_schema(cs_pin_required=True))
)


async def to_code(config):
    var = cg.new_Pvariable(config[CONF_ID])
    await cg.register_component(var, config)
    await spi.register_spi_device(var, config)

    dc = await cg.gpio_pin_expression(config[CONF_DC_PIN])
    cg.add(var.set_dc_pin(dc))

    reset = await cg.gpio_pin_expression(config[CONF_RESET_PIN])
    cg.add(var.set_reset_pin(reset))

    busy = await cg.gpio_pin_expression(config[CONF_BUSY_PIN])
    cg.add(var.set_busy_pin(busy))

    cg.add(var.set_width(config[CONF_WIDTH]))
    cg.add(var.set_height(config[CONF_HEIGHT]))
    cg.add(var.set_flip_vertical(config[CONF_FLIP_VERTICAL]))
    cg.add(var.set_flip_horizontal(config[CONF_FLIP_HORIZONTAL]))
