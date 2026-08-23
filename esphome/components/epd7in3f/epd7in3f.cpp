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

  if (!init_panel_()) {
    // Not mark_failed(): a panel that is unplugged or slow to power up at boot
    // must not disable the component for the rest of this wake cycle — the
    // next image transfer re-runs the init through wake_panel_().
    ESP_LOGE(TAG, "EPD7IN3F did not respond during init — retrying at the next image");
    return;
  }

  ESP_LOGI(TAG, "EPD7IN3F initialized");
}

bool EPD7IN3F::init_panel_() {
  // Hardware reset
  reset_();
  delay(20);
  if (!wait_busy_(BUSY_TIMEOUT_INIT_MS, "panel reset (init)")) {
    // Nothing on the other end of the bus is answering. Leave the panel marked
    // as sleeping so the next transfer redoes the whole reset + init sequence
    // rather than continuing into registers the controller never saw.
    sleeping_ = true;
    return false;
  }

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
  return true;
}

const char *EPD7IN3F::frame_tag_() const { return TAG; }

void EPD7IN3F::on_begin_image_() {
  if (!wake_panel_()) {
    // Streaming a whole image into a controller that never came out of reset
    // wastes the download and would still end in a "refresh complete" log.
    ESP_LOGE(TAG, "Display did not come up — abandoning transfer");
    this->mark_transfer_failed_();
    return;
  }
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

bool EPD7IN3F::on_finish_image_(bool complete) {
  if (!complete)
    return false;
  return turn_on_display_();
}

void EPD7IN3F::wake() { wake_panel_(); }

bool EPD7IN3F::wake_panel_() {
  if (!sleeping_)
    return true;
  ESP_LOGI(TAG, "Waking display (re-init)");
  return init_panel_();
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
//
// `phase` names the wait in the log, so a hardware log says which step of the
// sequence hung rather than just "busy pin".
bool EPD7IN3F::wait_busy_(uint32_t timeout_ms, const char *phase) {
  return eink_frame::wait_for_pin(busy_pin_, true, timeout_ms, TAG, phase);
}

// Power up, refresh, power down. Returns false if any phase timed out, so
// finish_image() reports a failed update instead of "Display refresh complete".
bool EPD7IN3F::turn_on_display_() {
  bool refreshed = false;

  send_command_(0x04);  // POWER_ON
  if (wait_busy_(BUSY_TIMEOUT_INIT_MS, "panel power-up (POWER_ON)")) {
    send_command_(0x12);  // DISPLAY_REFRESH
    send_data_(0x00);
    refreshed = wait_busy_(BUSY_TIMEOUT_REFRESH_MS, "display refresh (DISPLAY_REFRESH)");
  }

  // Power the panel back down on every path, including after a failed refresh:
  // the frame is about to deep sleep and the rail must not be left energized.
  // A timeout here also fails the update — it means the panel stopped
  // answering, so nothing about this cycle can be trusted.
  send_command_(0x02);  // POWER_OFF
  send_data_(0x00);
  if (!wait_busy_(BUSY_TIMEOUT_INIT_MS, "panel power-down (POWER_OFF)"))
    refreshed = false;

  return refreshed;
}

}  // namespace epd7in3f
}  // namespace esphome
