#include "epd7in3f.h"
#include "esphome/core/application.h"
#include "esphome/core/hal.h"
#include "esphome/core/log.h"

#include <cinttypes>
#include <cstdio>

namespace esphome {
namespace epd7in3f {

static const char *const TAG = "epd7in3f";

// Full refresh of the EPD7IN3F takes ~35 seconds.
static const uint32_t BUSY_TIMEOUT_INIT_MS = 5000;
static const uint32_t BUSY_TIMEOUT_REFRESH_MS = 60000;

void EPD7IN3F::setup() {
  ESP_LOGI(TAG, "Initializing EPD7IN3F display (%dx%d)", width_, height_);

  dc_pin_->setup();
  reset_pin_->setup();
  busy_pin_->setup();
  this->spi_setup();

  init_panel_();

  ESP_LOGI(TAG, "EPD7IN3F initialized");
}

void EPD7IN3F::init_panel_() {
  // Hardware reset
  reset_();
  delay(20);
  wait_busy_(BUSY_TIMEOUT_INIT_MS);

  // Init sequence from Waveshare datasheet
  send_command_(0xAA);  // CMDH
  send_data_(0x49);
  send_data_(0x55);
  send_data_(0x20);
  send_data_(0x08);
  send_data_(0x09);
  send_data_(0x18);

  send_command_(0x01);
  send_data_(0x3F);
  send_data_(0x00);
  send_data_(0x32);
  send_data_(0x2A);
  send_data_(0x0E);
  send_data_(0x2A);

  send_command_(0x00);
  send_data_(0x5F);
  send_data_(0x69);

  send_command_(0x03);
  send_data_(0x00);
  send_data_(0x54);
  send_data_(0x00);
  send_data_(0x44);

  send_command_(0x05);
  send_data_(0x40);
  send_data_(0x1F);
  send_data_(0x1F);
  send_data_(0x2C);

  send_command_(0x06);
  send_data_(0x6F);
  send_data_(0x1F);
  send_data_(0x1F);
  send_data_(0x22);

  send_command_(0x08);
  send_data_(0x6F);
  send_data_(0x1F);
  send_data_(0x1F);
  send_data_(0x22);

  send_command_(0x13);  // IPC
  send_data_(0x00);
  send_data_(0x04);

  send_command_(0x30);
  send_data_(0x3C);

  send_command_(0x41);  // TSE
  send_data_(0x00);

  send_command_(0x50);
  send_data_(0x3F);

  send_command_(0x60);
  send_data_(0x02);
  send_data_(0x00);

  send_command_(0x61);
  send_data_(width_ >> 8);
  send_data_(width_ & 0xFF);
  send_data_(height_ >> 8);
  send_data_(height_ & 0xFF);

  send_command_(0x82);
  send_data_(0x1E);

  send_command_(0x84);
  send_data_(0x00);

  send_command_(0x86);  // AGID
  send_data_(0x00);

  send_command_(0xE3);
  send_data_(0x2F);

  send_command_(0xE0);  // CCSET
  send_data_(0x00);

  send_command_(0xE6);  // TSSET
  send_data_(0x00);

  sleeping_ = false;
}

std::string EPD7IN3F::get_image_request_body() const {
  char head[128];
  snprintf(head, sizeof(head), "{\"width\":%d,\"height\":%d,\"flip_vertical\":%s,\"flip_horizonal\":%s,",
           width_, height_, flip_vertical_ ? "true" : "false", flip_horizontal_ ? "true" : "false");

  std::string body(head);
  body += "\"color_space\":["
          "{\"color_code\":0,\"rgb_color\":[0,0,0]},"
          "{\"color_code\":1,\"rgb_color\":[1,1,1]},"
          "{\"color_code\":2,\"rgb_color\":[0.059,0.329,0.119]},"
          "{\"color_code\":3,\"rgb_color\":[0.061,0.147,0.336]},"
          "{\"color_code\":4,\"rgb_color\":[0.574,0.066,0.010]},"
          "{\"color_code\":5,\"rgb_color\":[0.982,0.756,0.004]},"
          "{\"color_code\":6,\"rgb_color\":[0.795,0.255,0.018]}]}";
  return body;
}

void EPD7IN3F::begin_image() {
  if (sleeping_)
    wake();
  bytes_written_ = 0;
  send_command_(0x10);  // start data transmission
}

void EPD7IN3F::write_image_data(const uint8_t *data, size_t len) {
  size_t expected = get_image_byte_count();
  if (bytes_written_ + len > expected)
    len = expected - bytes_written_;
  if (len == 0)
    return;

  dc_pin_->digital_write(true);
  this->enable();
  this->write_array(data, len);
  this->disable();

  bytes_written_ += len;
}

void EPD7IN3F::finish_image(bool ok) {
  if (!ok || bytes_written_ < get_image_byte_count()) {
    ESP_LOGE(TAG, "Image transfer failed (%u/%u bytes) — skipping refresh", (unsigned) bytes_written_,
             (unsigned) get_image_byte_count());
    return;
  }

  ESP_LOGI(TAG, "Image data sent, refreshing display...");
  turn_on_display_();
  ESP_LOGI(TAG, "Display refresh complete");
}

void EPD7IN3F::wake() {
  if (!sleeping_)
    return;
  ESP_LOGI(TAG, "Waking display (re-init)");
  init_panel_();
}

void EPD7IN3F::sleep() {
  send_command_(0x07);
  send_data_(0xA5);
  delay(10);
  reset_pin_->digital_write(false);
  sleeping_ = true;
  ESP_LOGI(TAG, "Display entered sleep mode");
}

void EPD7IN3F::reset_() {
  reset_pin_->digital_write(true);
  delay(20);
  reset_pin_->digital_write(false);
  delay(1);
  reset_pin_->digital_write(true);
  delay(20);
}

void EPD7IN3F::send_command_(uint8_t command) {
  dc_pin_->digital_write(false);
  this->enable();
  this->transfer_byte(command);
  this->disable();
}

void EPD7IN3F::send_data_(uint8_t data) {
  dc_pin_->digital_write(true);
  this->enable();
  this->transfer_byte(data);
  this->disable();
}

// Busy pin is active LOW — wait while LOW. Returns false on timeout.
bool EPD7IN3F::wait_busy_(uint32_t timeout_ms) {
  uint32_t start = millis();
  while (!busy_pin_->digital_read()) {
    if (millis() - start > timeout_ms) {
      ESP_LOGE(TAG, "Timeout (%" PRIu32 " ms) waiting for busy pin", timeout_ms);
      return false;
    }
    App.feed_wdt();
    delay(1);
  }
  return true;
}

void EPD7IN3F::turn_on_display_() {
  send_command_(0x04);  // POWER_ON
  wait_busy_(BUSY_TIMEOUT_INIT_MS);

  send_command_(0x12);  // DISPLAY_REFRESH
  send_data_(0x00);
  wait_busy_(BUSY_TIMEOUT_REFRESH_MS);

  send_command_(0x02);  // POWER_OFF
  send_data_(0x00);
  wait_busy_(BUSY_TIMEOUT_INIT_MS);
}

}  // namespace epd7in3f
}  // namespace esphome
