#include "eink_frame.h"

#include <cstdio>

namespace esphome {
namespace eink_frame {

// The pigment values are the measured Spectra/ACeP primaries; the panels that
// share a pigment share the value (yellow/red/blue/green below).
const ColorSpaceEntry COLOR7_ENTRIES[7] = {
    {0, "0", "0", "0"},                    // black
    {1, "1", "1", "1"},                    // white
    {2, "0.059", "0.329", "0.119"},        // green
    {3, "0.061", "0.147", "0.336"},        // blue
    {4, "0.574", "0.066", "0.010"},        // red
    {5, "0.982", "0.756", "0.004"},        // yellow
    {6, "0.795", "0.255", "0.018"},        // orange
};
const ColorSpace COLOR7_COLOR_SPACE{COLOR7_ENTRIES, 7};

// Gray level i is i/15 formatted with four decimals, matching what the driver
// used to compute at runtime with snprintf("%.4f", i / 15.0f).
const ColorSpaceEntry GREY16_ENTRIES[16] = {
    {0, "0.0000", "0.0000", "0.0000"},   {1, "0.0667", "0.0667", "0.0667"},   {2, "0.1333", "0.1333", "0.1333"},
    {3, "0.2000", "0.2000", "0.2000"},   {4, "0.2667", "0.2667", "0.2667"},   {5, "0.3333", "0.3333", "0.3333"},
    {6, "0.4000", "0.4000", "0.4000"},   {7, "0.4667", "0.4667", "0.4667"},   {8, "0.5333", "0.5333", "0.5333"},
    {9, "0.6000", "0.6000", "0.6000"},   {10, "0.6667", "0.6667", "0.6667"},  {11, "0.7333", "0.7333", "0.7333"},
    {12, "0.8000", "0.8000", "0.8000"},  {13, "0.8667", "0.8667", "0.8667"},  {14, "0.9333", "0.9333", "0.9333"},
    {15, "1.0000", "1.0000", "1.0000"},
};
const ColorSpace GREY16_COLOR_SPACE{GREY16_ENTRIES, 16};

const ColorSpaceEntry SPECTRA6_ENTRIES[6] = {
    {0, "0", "0", "0"},                    // black
    {1, "1", "1", "1"},                    // white
    {2, "0.982", "0.756", "0.004"},        // yellow
    {3, "0.574", "0.066", "0.010"},        // red
    {5, "0.061", "0.147", "0.336"},        // blue
    {6, "0.059", "0.329", "0.119"},        // green
};
const ColorSpace SPECTRA6_COLOR_SPACE{SPECTRA6_ENTRIES, 6};

std::string build_image_request_body(int width, int height, bool flip_vertical, bool flip_horizontal,
                                     const ColorSpace &color_space) {
  char head[128];
  snprintf(head, sizeof(head), "{\"width\":%d,\"height\":%d,\"flip_vertical\":%s,\"flip_horizonal\":%s,", width, height,
           flip_vertical ? "true" : "false", flip_horizontal ? "true" : "false");

  std::string body(head);
  body += "\"color_space\":[";
  for (size_t i = 0; i < color_space.count; i++) {
    const ColorSpaceEntry &entry = color_space.entries[i];
    char buf[96];
    snprintf(buf, sizeof(buf), "%s{\"color_code\":%u,\"rgb_color\":[%s,%s,%s]}", i ? "," : "", (unsigned) entry.code,
             entry.r, entry.g, entry.b);
    body += buf;
  }
  body += "]}";
  return body;
}

void EinkFrameDisplay::begin_image() {
  this->bytes_written_ = 0;
  this->transfer_failed_ = false;
  this->session_active_ = true;
  this->on_begin_image_();
}

void EinkFrameDisplay::write_image_data(const uint8_t *data, size_t len) {
  // No transfer open: the driver's frame buffer may not exist (finish_image()
  // frees it), so nothing may reach the panel hooks.
  if (!this->session_active_ || this->transfer_failed_)
    return;

  // Drop anything past the image the panel asked for: a server that sends more
  // than expected must not scribble past the end of the panel/frame buffer.
  size_t expected = this->get_image_byte_count();
  if (this->bytes_written_ >= expected)
    return;
  if (this->bytes_written_ + len > expected)
    len = expected - this->bytes_written_;
  if (len == 0)
    return;

  // The hook is handed the offset explicitly, so it does not depend on whether
  // bytes_written_ has been updated yet.
  this->on_image_data_(this->bytes_written_, data, len);
  this->bytes_written_ += len;
}

void EinkFrameDisplay::finish_image(bool ok) {
  this->session_active_ = false;
  this->on_image_end_();

  size_t expected = this->get_image_byte_count();
  bool complete = ok && !this->transfer_failed_ && this->bytes_written_ >= expected;
  if (!complete) {
    EINK_LOGE(this->frame_tag_(), "Image transfer failed (%u/%u bytes) — skipping refresh",
              (unsigned) this->bytes_written_, (unsigned) expected);
    this->on_finish_image_(false);
    return;
  }

  EINK_LOGI(this->frame_tag_(), "Image data sent, refreshing display...");
  if (!this->on_finish_image_(true)) {
    // The image arrived intact but the panel never finished the update: a
    // busy/ready line that stayed stuck, or a command sequence the driver had
    // to abandon. Saying "complete" here is what let an unplugged panel look
    // like a working one in the log.
    this->transfer_failed_ = true;
    EINK_LOGE(this->frame_tag_(), "Display refresh failed — the panel did not complete the update");
    return;
  }
  EINK_LOGI(this->frame_tag_(), "Display refresh complete");
}

}  // namespace eink_frame
}  // namespace esphome
