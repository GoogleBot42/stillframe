#pragma once

// Pure (host-testable) part of the EL133UF1 image path: geometry and the
// split/rotate that turns the buffered frame into per-controller rows.
//
// No ESPHome includes here — see esphome/tests/.

#include <cstddef>
#include <cstdint>

namespace esphome {
namespace el133uf1 {

static const int NATIVE_WIDTH = 1200;   // panel native portrait width
static const int NATIVE_HEIGHT = 1600;  // panel native portrait height

// Each controller drives half the native width; at 4bpp that is 300 bytes.
static const int HALF_ROW_BYTES = NATIVE_WIDTH / 4;

// One 4bpp pixel out of a `src_width` wide, row-major, unpadded buffer.
inline uint8_t get_pixel(const uint8_t *buffer, int sx, int sy, int src_width) {
  uint8_t b = buffer[(size_t) sy * (size_t) (src_width / 2) + (size_t) (sx / 2)];
  return (sx & 1) ? (b & 0x0F) : (b >> 4);
}

// rotation 0: the buffer already holds native portrait rows, so a controller's
// half-row is a plain slice of it (no copy needed).
inline const uint8_t *native_half_row(const uint8_t *buffer, int py, int px_start) {
  return buffer + (size_t) py * (size_t) (NATIVE_WIDTH / 2) + (size_t) (px_start / 2);
}

// rotation 90/270: the buffer holds a landscape (NATIVE_HEIGHT x NATIVE_WIDTH)
// image, so native row `py` starting at native column `px_start` has to be
// gathered pixel by pixel. Writes HALF_ROW_BYTES bytes into `row_buf`.
inline void build_rotated_half_row(const uint8_t *buffer, int rotation, int py, int px_start, uint8_t *row_buf) {
  for (int i = 0; i < HALF_ROW_BYTES; i++) {
    int px0 = px_start + i * 2;
    uint8_t hi, lo;
    if (rotation == 90) {
      hi = get_pixel(buffer, py, NATIVE_WIDTH - 1 - px0, NATIVE_HEIGHT);
      lo = get_pixel(buffer, py, NATIVE_WIDTH - 2 - px0, NATIVE_HEIGHT);
    } else {  // 270
      hi = get_pixel(buffer, NATIVE_HEIGHT - 1 - py, px0, NATIVE_HEIGHT);
      lo = get_pixel(buffer, NATIVE_HEIGHT - 1 - py, px0 + 1, NATIVE_HEIGHT);
    }
    row_buf[i] = (uint8_t) ((hi << 4) | lo);
  }
}

}  // namespace el133uf1
}  // namespace esphome
