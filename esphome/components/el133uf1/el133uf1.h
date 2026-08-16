#pragma once

#include "esphome/core/component.h"
#include "esphome/core/gpio.h"
#include "esphome/components/spi/spi.h"

#include <string>

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

static const int NATIVE_WIDTH = 1200;   // panel native portrait width
static const int NATIVE_HEIGHT = 1600;  // panel native portrait height

class EL133UF1 : public Component,
                 public spi::SPIDevice<spi::BIT_ORDER_MSB_FIRST, spi::CLOCK_POLARITY_LOW,
                                       spi::CLOCK_PHASE_LEADING, spi::DATA_RATE_10MHZ> {
 public:
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
  void set_rotation(int rotation) { rotation_ = rotation; }
  void set_flip_vertical(bool flip) { flip_vertical_ = flip; }
  void set_flip_horizontal(bool flip) { flip_horizontal_ = flip; }

  // JSON body describing this display's capabilities, sent to the frame server.
  std::string get_image_request_body() const;
  size_t get_image_byte_count() const { return ((size_t) NATIVE_WIDTH / 2) * NATIVE_HEIGHT; }

  // Streaming image interface: begin_image(), then any number of
  // write_image_data() chunks, then finish_image(true) to refresh the panel
  // (or finish_image(false) to abandon a failed transfer).
  //
  // Unlike the single-controller panels, data cannot be forwarded to the
  // panel as it arrives: each controller needs its half of every row, so the
  // image is buffered (in PSRAM) and split/rotated during finish_image().
  void begin_image();
  void write_image_data(const uint8_t *data, size_t len);
  void finish_image(bool ok);

  void wake() {}  // panel is powered on per-update in begin_image()
  void sleep();

 protected:
  enum ChipId : uint8_t {
    CHIP_MASTER = 1 << 0,
    CHIP_SLAVE = 1 << 1,
    CHIP_BOTH = CHIP_MASTER | CHIP_SLAVE,
  };

  void power_on_and_init_();
  void power_off_();
  bool wait_busy_(uint32_t timeout_ms);
  void send_command_(uint8_t cmd, const uint8_t *data, size_t len, uint8_t chips);
  void send_image_half_(uint8_t chips, int px_start);
  uint8_t get_pixel_(int sx, int sy, int src_width) const;
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
  bool flip_vertical_{false};
  bool flip_horizontal_{false};

  uint8_t *buffer_{nullptr};
  size_t bytes_written_{0};
  bool powered_{false};
  bool transfer_failed_{false};
};

}  // namespace el133uf1
}  // namespace esphome
