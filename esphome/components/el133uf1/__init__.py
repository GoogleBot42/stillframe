import esphome.codegen as cg
import esphome.config_validation as cv
from esphome import pins
from esphome.components import spi
from esphome.const import CONF_DC_PIN, CONF_ID, CONF_RESET_PIN, CONF_ROTATION

DEPENDENCIES = ["spi"]
AUTO_LOAD = ["spi"]

CONF_CS_M_PIN = "cs_m_pin"
CONF_CS_S_PIN = "cs_s_pin"
CONF_BUSY_PIN = "busy_pin"
CONF_PWR_EN_PIN = "pwr_en_pin"
CONF_BS0_PIN = "bs0_pin"
CONF_BS1_PIN = "bs1_pin"
CONF_FLIP_VERTICAL = "flip_vertical"
CONF_FLIP_HORIZONTAL = "flip_horizontal"

el133uf1_ns = cg.esphome_ns.namespace("el133uf1")
EL133UF1 = el133uf1_ns.class_("EL133UF1", cg.Component, spi.SPIDevice)

CONFIG_SCHEMA = (
    cv.Schema(
        {
            cv.GenerateID(): cv.declare_id(EL133UF1),
            cv.Required(CONF_CS_M_PIN): pins.gpio_output_pin_schema,
            cv.Required(CONF_CS_S_PIN): pins.gpio_output_pin_schema,
            cv.Required(CONF_DC_PIN): pins.gpio_output_pin_schema,
            cv.Required(CONF_RESET_PIN): pins.gpio_output_pin_schema,
            cv.Required(CONF_BUSY_PIN): pins.gpio_input_pin_schema,
            cv.Required(CONF_PWR_EN_PIN): pins.gpio_output_pin_schema,
            cv.Optional(CONF_BS0_PIN): pins.gpio_output_pin_schema,
            cv.Optional(CONF_BS1_PIN): pins.gpio_output_pin_schema,
            # 0 = portrait (native), 90/270 = landscape. If the landscape image
            # comes out upside down, switch 90 <-> 270.
            cv.Optional(CONF_ROTATION, default=90): cv.one_of(0, 90, 270, int=True),
            cv.Optional(CONF_FLIP_VERTICAL, default=False): cv.boolean,
            cv.Optional(CONF_FLIP_HORIZONTAL, default=False): cv.boolean,
        }
    )
    .extend(cv.COMPONENT_SCHEMA)
    .extend(spi.spi_device_schema(cs_pin_required=False))
)


async def to_code(config):
    var = cg.new_Pvariable(config[CONF_ID])
    await cg.register_component(var, config)
    await spi.register_spi_device(var, config)

    for key, setter in [
        (CONF_CS_M_PIN, var.set_cs_m_pin),
        (CONF_CS_S_PIN, var.set_cs_s_pin),
        (CONF_DC_PIN, var.set_dc_pin),
        (CONF_RESET_PIN, var.set_reset_pin),
        (CONF_BUSY_PIN, var.set_busy_pin),
        (CONF_PWR_EN_PIN, var.set_pwr_en_pin),
    ]:
        pin = await cg.gpio_pin_expression(config[key])
        cg.add(setter(pin))

    for key, setter in [
        (CONF_BS0_PIN, var.set_bs0_pin),
        (CONF_BS1_PIN, var.set_bs1_pin),
    ]:
        if key in config:
            pin = await cg.gpio_pin_expression(config[key])
            cg.add(setter(pin))

    cg.add(var.set_rotation(config[CONF_ROTATION]))
    cg.add(var.set_flip_vertical(config[CONF_FLIP_VERTICAL]))
    cg.add(var.set_flip_horizontal(config[CONF_FLIP_HORIZONTAL]))
