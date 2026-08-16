"""Shared base for the DynamicFrame e-paper display drivers.

This component has no configuration of its own: the panel drivers
(``epd7in3f``, ``it8951_spi``, ``el133uf1``) pull it in with ``AUTO_LOAD`` so
its C++ sources land in the build, extend :data:`EINK_FRAME_SCHEMA` with the
config keys every panel shares, and call :func:`register_eink_frame` from their
``to_code``.
"""

import esphome.codegen as cg
import esphome.config_validation as cv
from esphome.const import CONF_HEIGHT, CONF_WIDTH

CODEOWNERS = ["@GoogleBot42"]

CONF_FLIP_VERTICAL = "flip_vertical"
CONF_FLIP_HORIZONTAL = "flip_horizontal"

# Upper bound on a configured panel dimension. Comfortably above every panel
# these drivers support (the largest is 1872x1404) while keeping a typo from
# asking the server for an image no ESP32 could ever hold.
MAX_DIMENSION = 4096

eink_frame_ns = cg.esphome_ns.namespace("eink_frame")
EinkFrameDisplay = eink_frame_ns.class_("EinkFrameDisplay")

CONFIG_SCHEMA = cv.Schema({})


async def to_code(config):
    pass


# Config keys understood by every panel driver. The frame server needs to know
# how the image is mounted, so the flips are part of the request body.
EINK_FRAME_SCHEMA = cv.Schema(
    {
        cv.Optional(CONF_FLIP_VERTICAL, default=False): cv.boolean,
        cv.Optional(CONF_FLIP_HORIZONTAL, default=False): cv.boolean,
    }
)


def eink_frame_schema(*, width=None, height=None):
    """EINK_FRAME_SCHEMA plus width/height, for panels whose size is not fixed.

    Panels with a hard-wired resolution (el133uf1) omit the arguments and set
    their geometry in C++ instead.
    """
    schema = EINK_FRAME_SCHEMA
    if width is not None or height is not None:
        schema = schema.extend(
            {
                cv.Optional(CONF_WIDTH, default=width): cv.int_range(
                    min=1, max=MAX_DIMENSION
                ),
                cv.Optional(CONF_HEIGHT, default=height): cv.int_range(
                    min=1, max=MAX_DIMENSION
                ),
            }
        )
    return schema


async def register_eink_frame(var, config):
    """Apply the shared config keys to a panel driver instance."""
    if CONF_WIDTH in config:
        cg.add(var.set_width(config[CONF_WIDTH]))
    if CONF_HEIGHT in config:
        cg.add(var.set_height(config[CONF_HEIGHT]))
    cg.add(var.set_flip_vertical(config[CONF_FLIP_VERTICAL]))
    cg.add(var.set_flip_horizontal(config[CONF_FLIP_HORIZONTAL]))
