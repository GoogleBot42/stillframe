#pragma once

#include "esphome/components/eink_frame/eink_frame.h"
#include "esphome/components/spi/spi.h"
#include "esphome/core/component.h"
#include "esphome/core/gpio.h"

#include "it8951_words.h"

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

// One "write data" SPI cycle's worth of packed words. Sized to keep the
// per-burst preamble overhead small without ever coming near the controller's
// input FIFO limit.
static const size_t BURST_SIZE = 2048;

struct IT8951DevInfo {
  uint16_t panel_w;
  uint16_t panel_h;
  uint16_t img_buf_addr_l;
  uint16_t img_buf_addr_h;
  uint16_t fw_version[8];
  uint16_t lut_version[8];
};

// IT8951 controller driving a 16-grayscale panel (1872x1404 by default). Image
// data is streamed into the controller's frame buffer as it arrives.
class IT8951SPI : public Component,
                  public spi::SPIDevice<spi::BIT_ORDER_MSB_FIRST, spi::CLOCK_POLARITY_LOW,
                                        spi::CLOCK_PHASE_LEADING, spi::DATA_RATE_20MHZ>,
                  public eink_frame::EinkFrameDisplay {
 public:
  void setup() override;
  float get_setup_priority() const override { return setup_priority::HARDWARE; }

  void set_reset_pin(GPIOPin *pin) { reset_pin_ = pin; }
  void set_hrdy_pin(GPIOPin *pin) { hrdy_pin_ = pin; }

  void wake() override;
  void sleep() override;

 protected:
  const char *frame_tag_() const override;
  const eink_frame::ColorSpace &get_color_space() const override { return eink_frame::GREY16_COLOR_SPACE; }
  void on_begin_image_() override;
  void on_image_data_(size_t offset, const uint8_t *data, size_t len) override;
  void on_image_end_() override;
  bool on_finish_image_(bool complete) override;

  // Every SPI helper below starts with an HRDY wait and returns false once the
  // controller has stopped answering, so a command sequence can be abandoned
  // instead of clocking bytes into nothing. The read helpers return data and
  // report failure through comm_failed_ instead.
  bool wait_ready_(uint32_t timeout_ms);
  bool lcd_write_cmd_(uint16_t cmd);
  bool lcd_write_data_(uint16_t data);
  uint16_t lcd_read_data_();
  void lcd_read_n_data_(uint16_t *buf, uint32_t word_count);
  bool lcd_send_cmd_arg_(uint16_t cmd, uint16_t *args, uint16_t num_args);

  bool get_system_info_();
  bool write_reg_(uint16_t addr, uint16_t value);
  uint16_t read_reg_(uint16_t addr);
  bool set_img_buf_base_addr_(uint32_t addr);
  bool wait_for_display_ready_(uint32_t timeout_ms);
  void write_burst_(const uint8_t *data, size_t len);

  GPIOPin *reset_pin_;
  GPIOPin *hrdy_pin_;

  IT8951DevInfo dev_info_{};
  uint32_t img_buf_addr_{0};
  WordPacker packer_;
  // Latched by wait_ready_() the first time HRDY does not come back, cleared at
  // the start of every transfer. Without it a disconnected panel burns two 5 s
  // HRDY timeouts per burst — ~1.8 hours for a 1872x1404 image — and still ends
  // in a success log.
  bool comm_failed_{false};
  // The burst buffer lives here rather than on the stack of on_image_data_():
  // 2 KiB is a quarter of the loop task's default 8 KiB stack, claimed at the
  // deepest point of the call chain once per 4 KB HTTP chunk.
  uint8_t burst_[BURST_SIZE];
};

}  // namespace it8951_spi
}  // namespace esphome
