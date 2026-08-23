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
  if (!get_system_info_()) {
    ESP_LOGE(TAG, "IT8951 did not answer (HRDY never went high) — check power and wiring");
    this->mark_failed();
    return;
  }

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
  // A previous transfer may have given up on the panel; this one gets a fresh
  // verdict.
  comm_failed_ = false;

  if (!wait_for_display_ready_(REFRESH_TIMEOUT_MS) || !set_img_buf_base_addr_(img_buf_addr_)) {
    ESP_LOGE(TAG, "IT8951 is not ready to accept an image — abandoning transfer");
    this->mark_transfer_failed_();
    return;
  }

  uint16_t args[5];
  args[0] = (IT8951_LDIMG_L_ENDIAN << 8) | (IT8951_4BPP << 4) | IT8951_ROTATE_0;
  args[1] = 0;        // x
  args[2] = 0;        // y
  args[3] = width_;   // width
  args[4] = height_;  // height
  if (!lcd_send_cmd_arg_(IT8951_TCON_LD_IMG_AREA, args, 5)) {
    ESP_LOGE(TAG, "IT8951 did not accept the image-load command — abandoning transfer");
    this->mark_transfer_failed_();
  }
}

// The controller keeps its own write pointer inside the loaded image area, so
// the offset within the image is not needed here.
void IT8951SPI::on_image_data_(size_t /*offset*/, const uint8_t *data, size_t len) {
  packer_.feed(data, len, burst_, BURST_SIZE, [this](const uint8_t *out, size_t out_len) {
    this->write_burst_(out, out_len);
    eink_frame::feed_watchdog();
  });

  // One dead burst is enough: fail the transfer so the base class drops the
  // rest of the download instead of grinding through it burst by burst.
  if (comm_failed_) {
    ESP_LOGE(TAG, "IT8951 stopped accepting image data — abandoning transfer");
    this->mark_transfer_failed_();
  }
}

// Each burst is a separate "write data" SPI cycle (preamble + payload) with an
// HRDY check in front, so the IT8951 input FIFO can never overrun.
void IT8951SPI::write_burst_(const uint8_t *data, size_t len) {
  if (!wait_ready_(HRDY_TIMEOUT_MS))
    return;
  this->enable();
  this->transfer_byte(PREAMBLE_WRITE >> 8);
  this->transfer_byte(PREAMBLE_WRITE & 0xFF);
  if (!wait_ready_(HRDY_TIMEOUT_MS)) {
    this->disable();
    return;
  }
  this->write_array(data, len);
  this->disable();
}

void IT8951SPI::on_image_end_() { lcd_write_cmd_(IT8951_TCON_LD_IMG_END); }

bool IT8951SPI::on_finish_image_(bool complete) {
  if (!complete)
    return false;

  // Display area with mode 2 (fast gray clear)
  bool sent = lcd_write_cmd_(USDEF_I80_CMD_DPY_AREA) && lcd_write_data_(0) && lcd_write_data_(0) &&
              lcd_write_data_(width_) && lcd_write_data_(height_) && lcd_write_data_(2);  // display mode 2
  if (!sent) {
    ESP_LOGE(TAG, "IT8951 did not accept the display command");
    return false;
  }

  // Wait for the refresh to actually finish so a following sleep()/deep sleep
  // doesn't abort it mid-update.
  return wait_for_display_ready_(REFRESH_TIMEOUT_MS);
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
//
// The first timeout latches comm_failed_, and every later wait then fails
// immediately: a panel that is unpowered or unplugged holds HRDY low forever,
// and each of the dozen call sites below would otherwise pay the full 5 s.
bool IT8951SPI::wait_ready_(uint32_t timeout_ms) {
  if (comm_failed_)
    return false;
  if (eink_frame::wait_for_pin(hrdy_pin_, true, timeout_ms, TAG, "HRDY"))
    return true;
  comm_failed_ = true;
  return false;
}

bool IT8951SPI::lcd_write_cmd_(uint16_t cmd) {
  if (!wait_ready_(HRDY_TIMEOUT_MS))
    return false;
  this->enable();
  this->transfer_byte(PREAMBLE_CMD >> 8);
  this->transfer_byte(PREAMBLE_CMD & 0xFF);
  if (!wait_ready_(HRDY_TIMEOUT_MS)) {
    this->disable();
    return false;
  }
  this->transfer_byte(cmd >> 8);
  this->transfer_byte(cmd & 0xFF);
  this->disable();
  return true;
}

bool IT8951SPI::lcd_write_data_(uint16_t data) {
  if (!wait_ready_(HRDY_TIMEOUT_MS))
    return false;
  this->enable();
  this->transfer_byte(PREAMBLE_WRITE >> 8);
  this->transfer_byte(PREAMBLE_WRITE & 0xFF);
  if (!wait_ready_(HRDY_TIMEOUT_MS)) {
    this->disable();
    return false;
  }
  this->transfer_byte(data >> 8);
  this->transfer_byte(data & 0xFF);
  this->disable();
  return true;
}

// Reads report failure through comm_failed_ rather than in-band: 0 is a
// perfectly valid register value. Callers that care check it (see
// wait_for_display_ready_()).
uint16_t IT8951SPI::lcd_read_data_() {
  uint16_t data = 0;
  lcd_read_n_data_(&data, 1);
  return data;
}

void IT8951SPI::lcd_read_n_data_(uint16_t *buf, uint32_t word_count) {
  for (uint32_t i = 0; i < word_count; i++)
    buf[i] = 0;

  if (!wait_ready_(HRDY_TIMEOUT_MS))
    return;
  this->enable();
  this->transfer_byte(PREAMBLE_READ >> 8);
  this->transfer_byte(PREAMBLE_READ & 0xFF);
  if (!wait_ready_(HRDY_TIMEOUT_MS)) {
    this->disable();
    return;
  }
  // Dummy read (2 bytes)
  this->transfer_byte(0x00);
  this->transfer_byte(0x00);
  if (!wait_ready_(HRDY_TIMEOUT_MS)) {
    this->disable();
    return;
  }
  for (uint32_t i = 0; i < word_count; i++) {
    buf[i] = this->transfer_byte(0x00) << 8;
    buf[i] |= this->transfer_byte(0x00);
  }
  this->disable();
}

bool IT8951SPI::lcd_send_cmd_arg_(uint16_t cmd, uint16_t *args, uint16_t num_args) {
  if (!lcd_write_cmd_(cmd))
    return false;
  for (uint16_t i = 0; i < num_args; i++) {
    if (!lcd_write_data_(args[i]))
      return false;
  }
  return true;
}

bool IT8951SPI::get_system_info_() {
  if (!lcd_write_cmd_(USDEF_I80_CMD_GET_DEV_INFO))
    return false;
  lcd_read_n_data_((uint16_t *) &dev_info_, sizeof(IT8951DevInfo) / 2);
  return !comm_failed_;
}

bool IT8951SPI::write_reg_(uint16_t addr, uint16_t value) {
  return lcd_write_cmd_(IT8951_TCON_REG_WR) && lcd_write_data_(addr) && lcd_write_data_(value);
}

uint16_t IT8951SPI::read_reg_(uint16_t addr) {
  if (!lcd_write_cmd_(IT8951_TCON_REG_RD) || !lcd_write_data_(addr))
    return 0;
  return lcd_read_data_();
}

bool IT8951SPI::set_img_buf_base_addr_(uint32_t addr) {
  uint16_t word_h = (uint16_t) ((addr >> 16) & 0xFFFF);
  uint16_t word_l = (uint16_t) (addr & 0xFFFF);
  return write_reg_(LISAR + 2, word_h) && write_reg_(LISAR, word_l);
}

// Poll the LUT engine until the previous display update has finished.
bool IT8951SPI::wait_for_display_ready_(uint32_t timeout_ms) {
  uint32_t start = millis();
  while (true) {
    uint16_t busy = read_reg_(LUTAFSR);
    // A failed read returns 0, which is exactly the "idle" value — so the
    // comm check has to come before the idle check, or an unplugged panel
    // would look permanently ready.
    if (comm_failed_) {
      ESP_LOGE(TAG, "IT8951 stopped answering while waiting for the display to become ready");
      return false;
    }
    if (!busy)
      return true;
    if (millis() - start > timeout_ms) {
      ESP_LOGE(TAG, "Timeout (%" PRIu32 " ms) waiting for display refresh", timeout_ms);
      return false;
    }
    eink_frame::feed_watchdog();
    delay(10);
  }
}

}  // namespace it8951_spi
}  // namespace esphome
