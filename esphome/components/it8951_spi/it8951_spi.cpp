#include "it8951_spi.h"
#include "esphome/components/eink_frame/eink_wait.h"
#include "esphome/core/hal.h"
#include "esphome/core/log.h"

#include <cinttypes>

namespace esphome {
namespace it8951_spi {

static const char *const TAG = "it8951_spi";

static const uint32_t HRDY_TIMEOUT_MS = 5000;
static const uint32_t REFRESH_TIMEOUT_MS = 40000;
static const size_t BURST_SIZE = 2048;

void IT8951SPI::setup() {
  ESP_LOGI(TAG, "Initializing IT8951 display");

  reset_pin_->setup();
  hrdy_pin_->setup();
  this->spi_setup();

  // Hardware reset — 1000ms low pulse (100ms per spec is insufficient)
  reset_pin_->digital_write(true);
  delay(10);
  reset_pin_->digital_write(false);
  delay(1000);
  reset_pin_->digital_write(true);
  delay(100);

  // Get device info
  get_system_info_();

  ESP_LOGI(TAG, "Panel: %dx%d", dev_info_.panel_w, dev_info_.panel_h);
  if (!dev_info_.panel_w || !dev_info_.panel_h) {
    ESP_LOGE(TAG, "IT8951 returned invalid panel dimensions");
    this->mark_failed();
    return;
  }

  img_buf_addr_ = dev_info_.img_buf_addr_l | ((uint32_t) dev_info_.img_buf_addr_h << 16);
  ESP_LOGI(TAG, "Image buffer address: 0x%08" PRIX32, img_buf_addr_);

  // Enable I80 packed mode
  write_reg_(I80CPCR, 0x0001);

  ESP_LOGI(TAG, "IT8951 initialized");
}

const char *IT8951SPI::frame_tag_() const { return TAG; }

void IT8951SPI::on_begin_image_() {
  packer_.reset();

  wait_for_display_ready_(REFRESH_TIMEOUT_MS);

  set_img_buf_base_addr_(img_buf_addr_);

  uint16_t args[5];
  args[0] = (IT8951_LDIMG_L_ENDIAN << 8) | (IT8951_4BPP << 4) | IT8951_ROTATE_0;
  args[1] = 0;        // x
  args[2] = 0;        // y
  args[3] = width_;   // width
  args[4] = height_;  // height
  lcd_send_cmd_arg_(IT8951_TCON_LD_IMG_AREA, args, 5);
}

// The controller keeps its own write pointer inside the loaded image area, so
// the offset within the image is not needed here.
void IT8951SPI::on_image_data_(size_t /*offset*/, const uint8_t *data, size_t len) {
  uint8_t burst[BURST_SIZE];
  packer_.feed(data, len, burst, BURST_SIZE, [this](const uint8_t *out, size_t out_len) {
    this->write_burst_(out, out_len);
    eink_frame::feed_watchdog();
  });
}

// Each burst is a separate "write data" SPI cycle (preamble + payload) with an
// HRDY check in front, so the IT8951 input FIFO can never overrun.
void IT8951SPI::write_burst_(const uint8_t *data, size_t len) {
  wait_ready_(HRDY_TIMEOUT_MS);
  this->enable();
  this->transfer_byte(PREAMBLE_WRITE >> 8);
  this->transfer_byte(PREAMBLE_WRITE & 0xFF);
  wait_ready_(HRDY_TIMEOUT_MS);
  this->write_array(data, len);
  this->disable();
}

void IT8951SPI::on_image_end_() { lcd_write_cmd_(IT8951_TCON_LD_IMG_END); }

void IT8951SPI::on_finish_image_(bool complete) {
  if (!complete)
    return;

  // Display area with mode 2 (fast gray clear)
  lcd_write_cmd_(USDEF_I80_CMD_DPY_AREA);
  lcd_write_data_(0);
  lcd_write_data_(0);
  lcd_write_data_(width_);
  lcd_write_data_(height_);
  lcd_write_data_(2);  // display mode 2

  // Wait for the refresh to actually finish so a following sleep()/deep sleep
  // doesn't abort it mid-update.
  wait_for_display_ready_(REFRESH_TIMEOUT_MS);
}

void IT8951SPI::wake() {
  lcd_write_cmd_(IT8951_TCON_SYS_RUN);
  ESP_LOGI(TAG, "Display woken up");
}

void IT8951SPI::sleep() {
  lcd_write_cmd_(IT8951_TCON_SLEEP);
  ESP_LOGI(TAG, "Display entered sleep mode");
}

// HRDY is HIGH when ready. Returns false on timeout.
bool IT8951SPI::wait_ready_(uint32_t timeout_ms) {
  return eink_frame::wait_for_pin(hrdy_pin_, true, timeout_ms, TAG, "HRDY");
}

void IT8951SPI::lcd_write_cmd_(uint16_t cmd) {
  wait_ready_(HRDY_TIMEOUT_MS);
  this->enable();
  this->transfer_byte(PREAMBLE_CMD >> 8);
  this->transfer_byte(PREAMBLE_CMD & 0xFF);
  wait_ready_(HRDY_TIMEOUT_MS);
  this->transfer_byte(cmd >> 8);
  this->transfer_byte(cmd & 0xFF);
  this->disable();
}

void IT8951SPI::lcd_write_data_(uint16_t data) {
  wait_ready_(HRDY_TIMEOUT_MS);
  this->enable();
  this->transfer_byte(PREAMBLE_WRITE >> 8);
  this->transfer_byte(PREAMBLE_WRITE & 0xFF);
  wait_ready_(HRDY_TIMEOUT_MS);
  this->transfer_byte(data >> 8);
  this->transfer_byte(data & 0xFF);
  this->disable();
}

uint16_t IT8951SPI::lcd_read_data_() {
  uint16_t data;

  wait_ready_(HRDY_TIMEOUT_MS);
  this->enable();
  this->transfer_byte(PREAMBLE_READ >> 8);
  this->transfer_byte(PREAMBLE_READ & 0xFF);
  wait_ready_(HRDY_TIMEOUT_MS);
  // Dummy read (2 bytes)
  this->transfer_byte(0x00);
  this->transfer_byte(0x00);
  wait_ready_(HRDY_TIMEOUT_MS);
  data = this->transfer_byte(0x00) << 8;
  data |= this->transfer_byte(0x00);
  this->disable();

  return data;
}

void IT8951SPI::lcd_read_n_data_(uint16_t *buf, uint32_t word_count) {
  wait_ready_(HRDY_TIMEOUT_MS);
  this->enable();
  this->transfer_byte(PREAMBLE_READ >> 8);
  this->transfer_byte(PREAMBLE_READ & 0xFF);
  wait_ready_(HRDY_TIMEOUT_MS);
  // Dummy read (2 bytes)
  this->transfer_byte(0x00);
  this->transfer_byte(0x00);
  wait_ready_(HRDY_TIMEOUT_MS);
  for (uint32_t i = 0; i < word_count; i++) {
    buf[i] = this->transfer_byte(0x00) << 8;
    buf[i] |= this->transfer_byte(0x00);
  }
  this->disable();
}

void IT8951SPI::lcd_send_cmd_arg_(uint16_t cmd, uint16_t *args, uint16_t num_args) {
  lcd_write_cmd_(cmd);
  for (uint16_t i = 0; i < num_args; i++) {
    lcd_write_data_(args[i]);
  }
}

void IT8951SPI::get_system_info_() {
  lcd_write_cmd_(USDEF_I80_CMD_GET_DEV_INFO);
  lcd_read_n_data_((uint16_t *) &dev_info_, sizeof(IT8951DevInfo) / 2);
}

void IT8951SPI::write_reg_(uint16_t addr, uint16_t value) {
  lcd_write_cmd_(IT8951_TCON_REG_WR);
  lcd_write_data_(addr);
  lcd_write_data_(value);
}

uint16_t IT8951SPI::read_reg_(uint16_t addr) {
  lcd_write_cmd_(IT8951_TCON_REG_RD);
  lcd_write_data_(addr);
  return lcd_read_data_();
}

void IT8951SPI::set_img_buf_base_addr_(uint32_t addr) {
  uint16_t word_h = (uint16_t) ((addr >> 16) & 0xFFFF);
  uint16_t word_l = (uint16_t) (addr & 0xFFFF);
  write_reg_(LISAR + 2, word_h);
  write_reg_(LISAR, word_l);
}

// Poll the LUT engine until the previous display update has finished.
bool IT8951SPI::wait_for_display_ready_(uint32_t timeout_ms) {
  uint32_t start = millis();
  while (read_reg_(LUTAFSR)) {
    if (millis() - start > timeout_ms) {
      ESP_LOGE(TAG, "Timeout (%" PRIu32 " ms) waiting for display refresh", timeout_ms);
      return false;
    }
    eink_frame::feed_watchdog();
    delay(10);
  }
  return true;
}

}  // namespace it8951_spi
}  // namespace esphome
