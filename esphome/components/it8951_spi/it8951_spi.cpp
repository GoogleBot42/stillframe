#include "it8951_spi.h"
#include "esphome/core/application.h"
#include "esphome/core/hal.h"
#include "esphome/core/log.h"

#include <cinttypes>
#include <cstdio>

namespace esphome {
namespace it8951_spi {

static const char *const TAG = "it8951_spi";

static const uint32_t HRDY_TIMEOUT_MS = 5000;
static const uint32_t REFRESH_TIMEOUT_MS = 40000;
// Each burst is a separate "write data" SPI cycle (preamble + payload) with an
// HRDY check in front, so the IT8951 input FIFO can never overrun.
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

std::string IT8951SPI::get_image_request_body() const {
  char head[128];
  snprintf(head, sizeof(head), "{\"width\":%d,\"height\":%d,\"flip_vertical\":%s,\"flip_horizonal\":%s,",
           width_, height_, flip_vertical_ ? "true" : "false", flip_horizontal_ ? "true" : "false");

  std::string body(head);
  body += "\"color_space\":[";
  for (int i = 0; i < 16; i++) {
    char entry[80];
    float v = i / 15.0f;
    snprintf(entry, sizeof(entry), "%s{\"color_code\":%d,\"rgb_color\":[%.4f,%.4f,%.4f]}", i ? "," : "", i, v, v, v);
    body += entry;
  }
  body += "]}";
  return body;
}

void IT8951SPI::begin_image() {
  bytes_written_ = 0;
  have_carry_ = false;

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

void IT8951SPI::write_image_data(const uint8_t *data, size_t len) {
  size_t expected = get_image_byte_count();
  if (bytes_written_ + len > expected)
    len = expected - bytes_written_;
  if (len == 0)
    return;

  // The IT8951 expects 16-bit words; each pair of server bytes is sent
  // high-byte-swapped (b1, b0), matching the legacy firmware's word writes.
  uint8_t burst[BURST_SIZE];
  size_t burst_len = 0;

  for (size_t i = 0; i < len; i++) {
    if (!have_carry_) {
      carry_byte_ = data[i];
      have_carry_ = true;
      continue;
    }
    burst[burst_len++] = data[i];
    burst[burst_len++] = carry_byte_;
    have_carry_ = false;

    if (burst_len == BURST_SIZE) {
      wait_ready_(HRDY_TIMEOUT_MS);
      this->enable();
      this->transfer_byte(PREAMBLE_WRITE >> 8);
      this->transfer_byte(PREAMBLE_WRITE & 0xFF);
      wait_ready_(HRDY_TIMEOUT_MS);
      this->write_array(burst, burst_len);
      this->disable();
      burst_len = 0;
      App.feed_wdt();
    }
  }

  if (burst_len > 0) {
    wait_ready_(HRDY_TIMEOUT_MS);
    this->enable();
    this->transfer_byte(PREAMBLE_WRITE >> 8);
    this->transfer_byte(PREAMBLE_WRITE & 0xFF);
    wait_ready_(HRDY_TIMEOUT_MS);
    this->write_array(burst, burst_len);
    this->disable();
  }

  bytes_written_ += len;
}

void IT8951SPI::finish_image(bool ok) {
  lcd_write_cmd_(IT8951_TCON_LD_IMG_END);

  if (!ok || bytes_written_ < get_image_byte_count()) {
    ESP_LOGE(TAG, "Image transfer failed (%u/%u bytes) — skipping refresh", (unsigned) bytes_written_,
             (unsigned) get_image_byte_count());
    return;
  }

  ESP_LOGI(TAG, "Image data sent, refreshing display...");

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
  ESP_LOGI(TAG, "Display refresh complete");
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
  uint32_t start = millis();
  while (!hrdy_pin_->digital_read()) {
    if (millis() - start > timeout_ms) {
      ESP_LOGE(TAG, "Timeout (%" PRIu32 " ms) waiting for HRDY", timeout_ms);
      return false;
    }
    App.feed_wdt();
    delay(1);
  }
  return true;
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
    App.feed_wdt();
    delay(10);
  }
  return true;
}

}  // namespace it8951_spi
}  // namespace esphome
