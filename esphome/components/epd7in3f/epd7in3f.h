#pragma once

#include "esphome/components/eink_frame/eink_frame.h"
#include "esphome/components/spi/spi.h"
#include "esphome/core/component.h"
#include "esphome/core/gpio.h"

namespace esphome {
namespace epd7in3f {

// Waveshare EPD7IN3F, 7-color ACeP, 800x480. Image data is streamed straight to
// the panel as it arrives, so no frame buffer is needed.
class EPD7IN3F : public Component,
                 public spi::SPIDevice<spi::BIT_ORDER_MSB_FIRST, spi::CLOCK_POLARITY_LOW,
                                       spi::CLOCK_PHASE_LEADING, spi::DATA_RATE_2MHZ>,
                 public eink_frame::EinkFrameDisplay {
 public:
  void setup() override;
  float get_setup_priority() const override { return setup_priority::HARDWARE; }

  void set_dc_pin(GPIOPin *pin) { dc_pin_ = pin; }
  void set_reset_pin(GPIOPin *pin) { reset_pin_ = pin; }
  void set_busy_pin(GPIOPin *pin) { busy_pin_ = pin; }

  void wake() override;
  void sleep() override;

 protected:
  const char *frame_tag_() const override;
  const eink_frame::ColorSpace &get_color_space() const override { return eink_frame::COLOR7_COLOR_SPACE; }
  void on_begin_image_() override;
  void on_image_data_(const uint8_t *data, size_t len) override;
  void on_finish_image_(bool complete) override;

  void init_panel_();
  void reset_();
  void send_command_(uint8_t command);
  void send_data_(uint8_t data);
  bool wait_busy_(uint32_t timeout_ms);
  void turn_on_display_();

  GPIOPin *dc_pin_;
  GPIOPin *reset_pin_;
  GPIOPin *busy_pin_;
  bool sleeping_{false};
};

}  // namespace epd7in3f
}  // namespace esphome
