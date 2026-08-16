#pragma once

#include "esphome/core/component.h"
#include "esphome/core/gpio.h"
#include "esphome/components/spi/spi.h"

#include <string>

namespace esphome {
namespace it8951_spi {

// IT8951 command codes
static const uint16_t IT8951_TCON_SYS_RUN = 0x0001;
static const uint16_t IT8951_TCON_STANDBY = 0x0002;
static const uint16_t IT8951_TCON_SLEEP = 0x0003;
static const uint16_t IT8951_TCON_REG_RD = 0x0010;
static const uint16_t IT8951_TCON_REG_WR = 0x0011;
static const uint16_t IT8951_TCON_LD_IMG_AREA = 0x0021;
static const uint16_t IT8951_TCON_LD_IMG_END = 0x0022;
static const uint16_t USDEF_I80_CMD_DPY_AREA = 0x0034;
static const uint16_t USDEF_I80_CMD_GET_DEV_INFO = 0x0302;

// Register addresses
static const uint16_t DISPLAY_REG_BASE = 0x1000;
static const uint16_t LUTAFSR = DISPLAY_REG_BASE + 0x224;
static const uint16_t SYS_REG_BASE = 0x0000;
static const uint16_t I80CPCR = SYS_REG_BASE + 0x04;
static const uint16_t MCSR_BASE_ADDR = 0x0200;
static const uint16_t LISAR = MCSR_BASE_ADDR + 0x0008;

// Pixel/endian constants
static const uint16_t IT8951_4BPP = 2;
static const uint16_t IT8951_LDIMG_L_ENDIAN = 0;
static const uint16_t IT8951_ROTATE_0 = 0;

// SPI preambles
static const uint16_t PREAMBLE_CMD = 0x6000;
static const uint16_t PREAMBLE_WRITE = 0x0000;
static const uint16_t PREAMBLE_READ = 0x1000;

struct IT8951DevInfo {
  uint16_t panel_w;
  uint16_t panel_h;
  uint16_t img_buf_addr_l;
  uint16_t img_buf_addr_h;
  uint16_t fw_version[8];
  uint16_t lut_version[8];
};

class IT8951SPI : public Component,
                  public spi::SPIDevice<spi::BIT_ORDER_MSB_FIRST, spi::CLOCK_POLARITY_LOW,
                                        spi::CLOCK_PHASE_LEADING, spi::DATA_RATE_20MHZ> {
 public:
  void setup() override;
  float get_setup_priority() const override { return setup_priority::HARDWARE; }

  void set_reset_pin(GPIOPin *pin) { reset_pin_ = pin; }
  void set_hrdy_pin(GPIOPin *pin) { hrdy_pin_ = pin; }
  void set_width(int width) { width_ = width; }
  void set_height(int height) { height_ = height; }
  void set_flip_vertical(bool flip) { flip_vertical_ = flip; }
  void set_flip_horizontal(bool flip) { flip_horizontal_ = flip; }

  // JSON body describing this display's capabilities, sent to the frame server.
  std::string get_image_request_body() const;
  size_t get_image_byte_count() const { return ((size_t) width_ * height_) / 2; }

  // Streaming image interface: begin_image(), then any number of
  // write_image_data() chunks, then finish_image(true) to refresh the panel
  // (or finish_image(false) to abandon a failed transfer).
  void begin_image();
  void write_image_data(const uint8_t *data, size_t len);
  void finish_image(bool ok);

  void wake();
  void sleep();

 protected:
  bool wait_ready_(uint32_t timeout_ms);
  void lcd_write_cmd_(uint16_t cmd);
  void lcd_write_data_(uint16_t data);
  uint16_t lcd_read_data_();
  void lcd_read_n_data_(uint16_t *buf, uint32_t word_count);
  void lcd_send_cmd_arg_(uint16_t cmd, uint16_t *args, uint16_t num_args);

  void get_system_info_();
  void write_reg_(uint16_t addr, uint16_t value);
  uint16_t read_reg_(uint16_t addr);
  void set_img_buf_base_addr_(uint32_t addr);
  bool wait_for_display_ready_(uint32_t timeout_ms);

  GPIOPin *reset_pin_;
  GPIOPin *hrdy_pin_;
  int width_{1872};
  int height_{1404};
  bool flip_vertical_{false};
  bool flip_horizontal_{false};

  IT8951DevInfo dev_info_{};
  uint32_t img_buf_addr_{0};
  size_t bytes_written_{0};
  uint8_t carry_byte_{0};
  bool have_carry_{false};
};

}  // namespace it8951_spi
}  // namespace esphome
