import esphome.codegen as cg
import esphome.config_validation as cv
from esphome import pins
from esphome.components import spi
from esphome.const import CONF_ID, CONF_RESET_PIN

DEPENDENCIES = ["spi"]
AUTO_LOAD = ["spi"]

CONF_HRDY_PIN = "hrdy_pin"
CONF_WIDTH = "width"
CONF_HEIGHT = "height"
CONF_FLIP_VERTICAL = "flip_vertical"
CONF_FLIP_HORIZONTAL = "flip_horizontal"

it8951_spi_ns = cg.esphome_ns.namespace("it8951_spi")
IT8951SPI = it8951_spi_ns.class_("IT8951SPI", cg.Component, spi.SPIDevice)

CONFIG_SCHEMA = (
    cv.Schema(
        {
            cv.GenerateID(): cv.declare_id(IT8951SPI),
            cv.Required(CONF_RESET_PIN): pins.gpio_output_pin_schema,
            cv.Required(CONF_HRDY_PIN): pins.gpio_input_pin_schema,
            cv.Optional(CONF_WIDTH, default=1872): cv.int_range(min=1),
            cv.Optional(CONF_HEIGHT, default=1404): cv.int_range(min=1),
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

    reset = await cg.gpio_pin_expression(config[CONF_RESET_PIN])
    cg.add(var.set_reset_pin(reset))

    hrdy = await cg.gpio_pin_expression(config[CONF_HRDY_PIN])
    cg.add(var.set_hrdy_pin(hrdy))

    cg.add(var.set_width(config[CONF_WIDTH]))
    cg.add(var.set_height(config[CONF_HEIGHT]))
    cg.add(var.set_flip_vertical(config[CONF_FLIP_VERTICAL]))
    cg.add(var.set_flip_horizontal(config[CONF_FLIP_HORIZONTAL]))
