#include "epd7in3f.h"
#include "esphome/components/eink_frame/eink_wait.h"
#include "esphome/core/hal.h"
#include "esphome/core/log.h"

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

const char *EPD7IN3F::frame_tag_() const { return TAG; }

void EPD7IN3F::on_begin_image_() {
  if (sleeping_)
    wake();
  send_command_(0x10);  // start data transmission
}

// Data is streamed straight into the panel's own pointer, so the offset within
// the image is not needed here.
void EPD7IN3F::on_image_data_(size_t /*offset*/, const uint8_t *data, size_t len) {
  dc_pin_->digital_write(true);
  this->enable();
  this->write_array(data, len);
  this->disable();
}

void EPD7IN3F::on_finish_image_(bool complete) {
  if (complete)
    turn_on_display_();
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
  return eink_frame::wait_for_pin(busy_pin_, true, timeout_ms, TAG, "busy pin");
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
