#pragma once

#include "esphome/components/eink_frame/eink_frame.h"
#include "esphome/components/spi/spi.h"
#include "esphome/core/component.h"
#include "esphome/core/gpio.h"

#include "el133uf1_image.h"

namespace esphome {
namespace el133uf1 {

// E Ink Spectra 6 13.3" (EL133UF1), 1600x1200, as used in the Soldered
// Inkplate 13 Spectra and the Waveshare 13.3" e-Paper HAT+ (E).
//
// The panel is driven by two cascaded controllers over one SPI bus with
// separate chip selects: the "master" drives the left 600 columns and the
// "slave" the right 600 columns (in the panel's native 1200x1600 portrait
// orientation). Commands are a single CS frame of command byte + parameter
// bytes with DC held high (BS0/BS1 strapping selects this interface mode).

class EL133UF1 : public Component,
                 public spi::SPIDevice<spi::BIT_ORDER_MSB_FIRST, spi::CLOCK_POLARITY_LOW,
                                       spi::CLOCK_PHASE_LEADING, spi::DATA_RATE_10MHZ>,
                 public eink_frame::EinkFrameDisplay {
 public:
  EL133UF1() { this->set_rotation(rotation_); }

  void setup() override;
  float get_setup_priority() const override { return setup_priority::HARDWARE; }

  void set_cs_m_pin(GPIOPin *pin) { cs_m_pin_ = pin; }
  void set_cs_s_pin(GPIOPin *pin) { cs_s_pin_ = pin; }
  void set_dc_pin(GPIOPin *pin) { dc_pin_ = pin; }
  void set_reset_pin(GPIOPin *pin) { reset_pin_ = pin; }
  void set_busy_pin(GPIOPin *pin) { busy_pin_ = pin; }
  void set_pwr_en_pin(GPIOPin *pin) { pwr_en_pin_ = pin; }
  void set_bs0_pin(GPIOPin *pin) { bs0_pin_ = pin; }
  void set_bs1_pin(GPIOPin *pin) { bs1_pin_ = pin; }

  // The panel resolution is fixed; the rotation decides which way round the
  // image is requested from the server (the byte count is the same either way).
  void set_rotation(int rotation) {
    rotation_ = rotation;
    this->set_width(rotation == 0 ? NATIVE_WIDTH : NATIVE_HEIGHT);
    this->set_height(rotation == 0 ? NATIVE_HEIGHT : NATIVE_WIDTH);
  }

  void wake() override {}  // panel is powered on per-update in begin_image()
  void sleep() override;

 protected:
  enum ChipId : uint8_t {
    CHIP_MASTER = 1 << 0,
    CHIP_SLAVE = 1 << 1,
    CHIP_BOTH = CHIP_MASTER | CHIP_SLAVE,
  };

  const char *frame_tag_() const override;
  const eink_frame::ColorSpace &get_color_space() const override { return eink_frame::SPECTRA6_COLOR_SPACE; }

  // Unlike the single-controller panels, data cannot be forwarded to the panel
  // as it arrives: each controller needs its half of every row, so the image is
  // buffered (in PSRAM) and split/rotated during finish_image().
  void on_begin_image_() override;
  void on_image_data_(const uint8_t *data, size_t len) override;
  void on_finish_image_(bool complete) override;

  void power_on_and_init_();
  void power_off_();
  bool wait_busy_(uint32_t timeout_ms);
  void send_command_(uint8_t cmd, const uint8_t *data, size_t len, uint8_t chips);
  void select_chips_(uint8_t chips, bool selected);
  void send_image_half_(uint8_t chips, int px_start);
  void free_buffer_();

  GPIOPin *cs_m_pin_;
  GPIOPin *cs_s_pin_;
  GPIOPin *dc_pin_;
  GPIOPin *reset_pin_;
  GPIOPin *busy_pin_;
  GPIOPin *pwr_en_pin_;
  GPIOPin *bs0_pin_{nullptr};
  GPIOPin *bs1_pin_{nullptr};

  int rotation_{90};  // 0 (portrait), 90 or 270 (landscape)

  uint8_t *buffer_{nullptr};
  bool powered_{false};
};

}  // namespace el133uf1
}  // namespace esphome
