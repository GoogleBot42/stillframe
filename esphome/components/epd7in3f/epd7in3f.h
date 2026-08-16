#pragma once

#include "esphome/core/component.h"
#include "esphome/core/gpio.h"
#include "esphome/components/spi/spi.h"

#include <string>

namespace esphome {
namespace epd7in3f {

class EPD7IN3F : public Component,
                 public spi::SPIDevice<spi::BIT_ORDER_MSB_FIRST, spi::CLOCK_POLARITY_LOW,
                                       spi::CLOCK_PHASE_LEADING, spi::DATA_RATE_2MHZ> {
 public:
  void setup() override;
  float get_setup_priority() const override { return setup_priority::HARDWARE; }

  void set_dc_pin(GPIOPin *pin) { dc_pin_ = pin; }
  void set_reset_pin(GPIOPin *pin) { reset_pin_ = pin; }
  void set_busy_pin(GPIOPin *pin) { busy_pin_ = pin; }
  void set_width(int width) { width_ = width; }
  void set_height(int height) { height_ = height; }
  void set_flip_vertical(bool flip) { flip_vertical_ = flip; }
  void set_flip_horizontal(bool flip) { flip_horizontal_ = flip; }

  // JSON body describing this display's capabilities, sent to the frame server.
  std::string get_image_request_body() const;
  size_t get_image_byte_count() const { return ((size_t) width_ / 2) * height_; }

  // Streaming image interface: begin_image(), then any number of
  // write_image_data() chunks, then finish_image(true) to refresh the panel
  // (or finish_image(false) to abandon a failed transfer).
  void begin_image();
  void write_image_data(const uint8_t *data, size_t len);
  void finish_image(bool ok);

  void wake();
  void sleep();

 protected:
  void init_panel_();
  void reset_();
  void send_command_(uint8_t command);
  void send_data_(uint8_t data);
  bool wait_busy_(uint32_t timeout_ms);
  void turn_on_display_();

  GPIOPin *dc_pin_;
  GPIOPin *reset_pin_;
  GPIOPin *busy_pin_;
  int width_{800};
  int height_{480};
  bool flip_vertical_{false};
  bool flip_horizontal_{false};
  size_t bytes_written_{0};
  bool sleeping_{false};
};

}  // namespace epd7in3f
}  // namespace esphome
