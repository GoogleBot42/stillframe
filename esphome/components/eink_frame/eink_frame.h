#pragma once

// Panel-independent half of a DynamicFrame e-paper display driver.
//
// Every panel driver (epd7in3f, it8951_spi, el133uf1) exposes the same
// interface to the shared `fetch_and_display` script in esphome/common.yaml:
//
//   get_image_request_body()  JSON body describing the panel to the server
//   get_image_byte_count()    how many bytes of image data that request yields
//   begin_image()             start a transfer
//   write_image_data()        feed one chunk of the HTTP response
//   finish_image(ok)          refresh the panel (or abandon the transfer)
//   wake() / sleep()          panel power state around deep sleep
//
// All of that except the panel-specific parts is implemented once here.
// Drivers derive from EinkFrameDisplay and implement the on_*_() hooks, which
// are the only places SPI/command sequences live.
//
// This header (and eink_frame.cpp) must stay free of ESPHome runtime includes
// so the logic can be unit tested on the host — see eink_log.h and
// esphome/tests/. Driver-side helpers that do need the runtime live in
// eink_wait.h.

#include <cstddef>
#include <cstdint>
#include <string>

#include "eink_log.h"

namespace esphome {
namespace eink_frame {

// One entry of a panel's color space, as sent to the frame server. The channel
// values are pre-formatted decimal strings rather than floats so the JSON body
// is byte-identical everywhere and needs no float formatting at runtime.
struct ColorSpaceEntry {
  uint8_t code;
  const char *r;
  const char *g;
  const char *b;
};

struct ColorSpace {
  const ColorSpaceEntry *entries;
  size_t count;
};

// Waveshare EPD7IN3F (7-color ACeP): black, white, green, blue, red, yellow,
// orange. Color codes are the nibble values the panel itself expects.
extern const ColorSpaceEntry COLOR7_ENTRIES[7];
extern const ColorSpace COLOR7_COLOR_SPACE;

// IT8951 (16 levels of gray): code i is the linear gray i/15.
extern const ColorSpaceEntry GREY16_ENTRIES[16];
extern const ColorSpace GREY16_COLOR_SPACE;

// E Ink Spectra 6 (EL133UF1): black, white, yellow, red, blue, green. Code 4 is
// deliberately absent — the panel does not use it.
extern const ColorSpaceEntry SPECTRA6_ENTRIES[6];
extern const ColorSpace SPECTRA6_COLOR_SPACE;

// Number of bytes the server returns for a width x height image: 4 bits per
// pixel, two pixels per byte, packed as one continuous row-major stream with no
// per-row padding (see packBytesIntoNibbles in server/einkimage.go).
inline size_t packed_4bpp_byte_count(int width, int height) {
  if (width <= 0 || height <= 0)
    return 0;
  return ((size_t) width * (size_t) height) / 2;
}

// Build the JSON body posted to /fetchImage.
//
// NOTE: "flip_horizonal" is misspelled on purpose — that is the key the server
// has always parsed (see server/main.go), so it must not be "fixed" here.
std::string build_image_request_body(int width, int height, bool flip_vertical, bool flip_horizontal,
                                     const ColorSpace &color_space);

class EinkFrameDisplay {
 public:
  virtual ~EinkFrameDisplay() = default;

  void set_width(int width) { this->width_ = width; }
  void set_height(int height) { this->height_ = height; }
  void set_flip_vertical(bool flip) { this->flip_vertical_ = flip; }
  void set_flip_horizontal(bool flip) { this->flip_horizontal_ = flip; }

  // --- interface used by the fetch_and_display script in common.yaml ---

  // JSON body describing this display's capabilities, sent to the frame server.
  std::string get_image_request_body() const {
    return build_image_request_body(this->width_, this->height_, this->flip_vertical_, this->flip_horizontal_,
                                    this->get_color_space());
  }
  size_t get_image_byte_count() const { return packed_4bpp_byte_count(this->width_, this->height_); }

  // Streaming image interface: begin_image(), then any number of
  // write_image_data() chunks, then finish_image(true) to refresh the panel
  // (or finish_image(false) to abandon a failed transfer). Bytes past the
  // expected image size are dropped, and a short image is never shown.
  // Chunks that arrive outside a begin/finish pair are dropped too, so a
  // driver's buffers are only ever touched while a transfer is open.
  void begin_image();
  void write_image_data(const uint8_t *data, size_t len);
  void finish_image(bool ok);

  virtual void wake() = 0;
  virtual void sleep() = 0;

 protected:
  // --- panel-specific hooks ---

  // Log tag of the driver, so shared code logs under the driver's own tag.
  virtual const char *frame_tag_() const = 0;
  virtual const ColorSpace &get_color_space() const = 0;

  // Prepare the panel for a new frame (power up, image-load command, ...).
  virtual void on_begin_image_() = 0;
  // Consume one chunk. Already clamped to the remaining image size, only
  // called between begin_image() and finish_image(), and never once the
  // transfer has been marked failed.
  //
  // `offset` is where this chunk starts within the image: the number of bytes
  // already accepted for this transfer, i.e. 0 for the first chunk and the sum
  // of all previous `len`s after that. Drivers that buffer the frame (el133uf1)
  // write at `offset`; drivers that stream straight to the panel (epd7in3f,
  // it8951_spi) ignore it. Passing it explicitly keeps the hook independent of
  // when the base class updates its own byte counter.
  virtual void on_image_data_(size_t offset, const uint8_t *data, size_t len) = 0;
  // Optional end-of-data marker, sent whether or not the transfer succeeded.
  virtual void on_image_end_() {}
  // Refresh the panel when `complete` is true, otherwise clean up only.
  virtual void on_finish_image_(bool complete) = 0;

  // Abort the current transfer (allocation failure, panel did not come up).
  // Remaining chunks are dropped and finish_image() takes the failure path.
  void mark_transfer_failed_() { this->transfer_failed_ = true; }

  int width_{0};
  int height_{0};
  bool flip_vertical_{false};
  bool flip_horizontal_{false};

  // Transfer accounting, reset by begin_image().
  size_t bytes_written_{0};
  bool transfer_failed_{false};
  // True between begin_image() and finish_image(). Guards the drivers against
  // chunks that arrive before a transfer was opened or after it was closed
  // (finish_image() may have freed the buffer they would write into).
  bool session_active_{false};
};

}  // namespace eink_frame
}  // namespace esphome
