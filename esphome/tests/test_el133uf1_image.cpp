// EL133UF1 frame splitting: the buffered image has to be cut into the two
// controllers' halves, rotating on the way when the frame is landscape.

#include "esphome/components/el133uf1/el133uf1_image.h"
#include "test.h"

#include <cstring>

using namespace esphome::el133uf1;

namespace {

// The buffer holds the image the server sent: landscape (1600x1200) for
// rotation 90/270, native portrait (1200x1600) for rotation 0. Either way it is
// 4bpp, row-major, unpadded — 960000 bytes.
constexpr int LANDSCAPE_WIDTH = NATIVE_HEIGHT;
constexpr int LANDSCAPE_HEIGHT = NATIVE_WIDTH;
constexpr size_t IMAGE_BYTES = (size_t) NATIVE_WIDTH * NATIVE_HEIGHT / 2;

uint8_t pattern(int x, int y) { return (uint8_t) ((x * 7 + y * 13) & 0x0F); }

std::vector<uint8_t> make_image(int width, int height) {
  std::vector<uint8_t> buffer((size_t) width * height / 2);
  for (int y = 0; y < height; y++) {
    for (int x = 0; x < width; x += 2) {
      uint8_t hi = pattern(x, y);
      uint8_t lo = pattern(x + 1, y);
      buffer[(size_t) y * (width / 2) + x / 2] = (uint8_t) ((hi << 4) | lo);
    }
  }
  return buffer;
}

// Independent restatement of the rotation: which source pixel ends up at native
// panel position (px, py)?
uint8_t expected_native_pixel(int rotation, int px, int py) {
  if (rotation == 0)
    return pattern(px, py);
  if (rotation == 90)
    return pattern(py, NATIVE_WIDTH - 1 - px);
  return pattern(NATIVE_HEIGHT - 1 - py, px);  // 270
}

// Native panel position of a pixel, in the panel's own portrait frame: px is
// the native column (0..1199), py the native row (0..1599).
struct NativePos {
  int px, py;
};

// Reads native panel pixel (px, py) back out through the real split/rotate
// path — exactly the call send_image_half_() makes for that half-row.
uint8_t native_pixel(const uint8_t *buffer, int rotation, int px, int py) {
  int px_start = px < NATIVE_WIDTH / 2 ? 0 : NATIVE_WIDTH / 2;
  uint8_t row_buf[HALF_ROW_BYTES];
  build_half_row(buffer, rotation, py, px_start, row_buf);
  int i = (px - px_start) / 2;
  return (px & 1) ? (row_buf[i] & 0x0F) : (row_buf[i] >> 4);
}

// An otherwise blank frame with a single marker nibble at buffer pixel (0, 0).
std::vector<uint8_t> make_marked_image(int width, int height, uint8_t marker) {
  std::vector<uint8_t> buffer((size_t) width * height / 2, 0x00);
  buffer[0] = (uint8_t) (marker << 4);
  return buffer;
}

// Walks the whole transmitted frame and reports where the marker came out.
// Returns {-1, -1} unless it appears exactly once, so a wrong answer cannot
// hide behind a value that happens to occur elsewhere.
NativePos find_marker(const uint8_t *buffer, int rotation, uint8_t marker) {
  NativePos found{-1, -1};
  int count = 0;
  uint8_t row_buf[HALF_ROW_BYTES];
  for (int py = 0; py < NATIVE_HEIGHT; py++) {
    for (int px_start : {0, NATIVE_WIDTH / 2}) {
      build_half_row(buffer, rotation, py, px_start, row_buf);
      for (int i = 0; i < HALF_ROW_BYTES; i++) {
        if ((row_buf[i] >> 4) == marker) {
          found = {px_start + i * 2, py};
          count++;
        }
        if ((row_buf[i] & 0x0F) == marker) {
          found = {px_start + i * 2 + 1, py};
          count++;
        }
      }
    }
  }
  return count == 1 ? found : NativePos{-1, -1};
}

}  // namespace

TEST(get_pixel_reads_the_high_nibble_first) {
  const uint8_t buffer[] = {0xAB, 0xCD};
  CHECK_EQ_INT(0xA, get_pixel(buffer, 0, 0, 4));
  CHECK_EQ_INT(0xB, get_pixel(buffer, 1, 0, 4));
  CHECK_EQ_INT(0xC, get_pixel(buffer, 2, 0, 4));
  CHECK_EQ_INT(0xD, get_pixel(buffer, 3, 0, 4));

  // Second row of a 2px wide image.
  CHECK_EQ_INT(0xC, get_pixel(buffer, 0, 1, 2));
  CHECK_EQ_INT(0xD, get_pixel(buffer, 1, 1, 2));
}

TEST(half_row_geometry) {
  CHECK_EQ_INT(300, HALF_ROW_BYTES);                     // 600 px per controller
  CHECK_EQ_INT(960000, (long long) IMAGE_BYTES);         // full frame
  CHECK_EQ_INT(NATIVE_WIDTH / 2, 2 * HALF_ROW_BYTES);    // the two halves tile a row
}

// rotation 0: the master's half-row is the first 300 bytes of the native row,
// the slave's the last 300.
TEST(native_half_row_slices_the_buffer) {
  std::vector<uint8_t> image = make_image(NATIVE_WIDTH, NATIVE_HEIGHT);

  for (int py : {0, 1, 799, NATIVE_HEIGHT - 1}) {
    const uint8_t *master = native_half_row(image.data(), py, 0);
    const uint8_t *slave = native_half_row(image.data(), py, NATIVE_WIDTH / 2);
    CHECK_EQ_INT((size_t) py * (NATIVE_WIDTH / 2), master - image.data());
    CHECK_EQ_INT(HALF_ROW_BYTES, slave - master);

    for (int i = 0; i < HALF_ROW_BYTES; i++) {
      CHECK_EQ_INT(expected_native_pixel(0, i * 2, py), master[i] >> 4);
      CHECK_EQ_INT(expected_native_pixel(0, i * 2 + 1, py), master[i] & 0x0F);
      CHECK_EQ_INT(expected_native_pixel(0, NATIVE_WIDTH / 2 + i * 2, py), slave[i] >> 4);
      CHECK_EQ_INT(expected_native_pixel(0, NATIVE_WIDTH / 2 + i * 2 + 1, py), slave[i] & 0x0F);
    }
  }
}

// Whatever the rotation, the bytes the driver hands to write_array() are the
// caller's own row_buf, never a pointer into the frame buffer: that buffer is
// PSRAM and the SPI transfer is DMA-backed (issue #26). rotation 0 is the case
// that could have aliased, so check it really copies.
TEST(build_half_row_copies_rotation_0_out_of_the_frame_buffer) {
  std::vector<uint8_t> image = make_image(NATIVE_WIDTH, NATIVE_HEIGHT);

  uint8_t row_buf[HALF_ROW_BYTES];
  for (int py : {0, 1, 799, NATIVE_HEIGHT - 1}) {
    for (int px_start : {0, NATIVE_WIDTH / 2}) {
      std::memset(row_buf, 0x5A, sizeof(row_buf));
      build_half_row(image.data(), 0, py, px_start, row_buf);

      const uint8_t *slice = native_half_row(image.data(), py, px_start);
      for (int i = 0; i < HALF_ROW_BYTES; i++)
        CHECK_EQ_INT(slice[i], row_buf[i]);
    }
  }

  // A copy, not a view: scribbling over the source leaves row_buf alone.
  build_half_row(image.data(), 0, 0, 0, row_buf);
  uint8_t first = row_buf[0];
  image[0] = (uint8_t) ~image[0];
  CHECK_EQ_INT(first, row_buf[0]);
  CHECK(native_half_row(image.data(), 0, 0)[0] != row_buf[0]);
}

// rotation 90/270: every native pixel is gathered from the landscape buffer.
TEST(rotated_half_rows_place_every_pixel) {
  std::vector<uint8_t> image = make_image(LANDSCAPE_WIDTH, LANDSCAPE_HEIGHT);
  CHECK_EQ_INT((long long) IMAGE_BYTES, (long long) image.size());

  uint8_t row_buf[HALF_ROW_BYTES];
  for (int rotation : {90, 270}) {
    for (int py : {0, 1, 42, NATIVE_HEIGHT / 2, NATIVE_HEIGHT - 1}) {
      for (int px_start : {0, NATIVE_WIDTH / 2}) {
        build_rotated_half_row(image.data(), rotation, py, px_start, row_buf);
        for (int i = 0; i < HALF_ROW_BYTES; i++) {
          CHECK_EQ_INT(expected_native_pixel(rotation, px_start + i * 2, py), row_buf[i] >> 4);
          CHECK_EQ_INT(expected_native_pixel(rotation, px_start + i * 2 + 1, py), row_buf[i] & 0x0F);
        }
      }
    }
  }
}

// 90 and 270 differ by a 180 degree turn, so one is the other read backwards.
TEST(rotations_90_and_270_are_opposites) {
  std::vector<uint8_t> image = make_image(LANDSCAPE_WIDTH, LANDSCAPE_HEIGHT);

  uint8_t row_90[HALF_ROW_BYTES];
  uint8_t row_270[HALF_ROW_BYTES];
  for (int py : {0, 7, NATIVE_HEIGHT - 1}) {
    build_rotated_half_row(image.data(), 90, py, 0, row_90);
    build_rotated_half_row(image.data(), 270, NATIVE_HEIGHT - 1 - py, NATIVE_WIDTH / 2, row_270);
    for (int i = 0; i < HALF_ROW_BYTES; i++) {
      // Pixel (px, py) at 90 is pixel (NATIVE_WIDTH-1-px, NATIVE_HEIGHT-1-py) at 270.
      int mirrored = HALF_ROW_BYTES - 1 - i;
      CHECK_EQ_INT(row_90[i] >> 4, row_270[mirrored] & 0x0F);
      CHECK_EQ_INT(row_90[i] & 0x0F, row_270[mirrored] >> 4);
    }
  }
}

// Which physical corner the top-left of the picture ends up in.
//
// 90 and 270 are the two landscape mounts and differ by exactly 180 degrees, so
// they are the one pair of values a refactor could swap with every other test
// in this file still passing — and getting it wrong means every photo hangs
// upside down (issue #13, which is why spectra13-inkplate.yaml ships 270). The
// marker is unique in the frame, so these are exact, not incidental, matches.
//
// Native panel coordinates are the panel's own portrait frame: px 0..1199 left
// to right, py 0..1599 top to bottom. Transmission order is row py=0 first,
// master controller (px 0..599) then slave (px 600..1199), so native (0, 0) is
// the first pixel of the whole frame and (1199, 1599) the last.
TEST(logical_origin_lands_on_the_expected_panel_corner) {
  // rotation 0: portrait buffer, no rotation — the picture's top-left is the
  // panel's top-left, the first pixel the master controller is sent.
  std::vector<uint8_t> portrait = make_marked_image(NATIVE_WIDTH, NATIVE_HEIGHT, 0xF);
  NativePos p0 = find_marker(portrait.data(), 0, 0xF);
  CHECK_EQ_INT(0, p0.px);
  CHECK_EQ_INT(0, p0.py);

  // Both landscape rotations read the same 1600x1200 buffer.
  std::vector<uint8_t> landscape = make_marked_image(LANDSCAPE_WIDTH, LANDSCAPE_HEIGHT, 0xF);

  // rotation 90: the picture's top-left is the panel's top-RIGHT — the last
  // pixel of the slave controller's first row.
  NativePos p90 = find_marker(landscape.data(), 90, 0xF);
  CHECK_EQ_INT(NATIVE_WIDTH - 1, p90.px);
  CHECK_EQ_INT(0, p90.py);

  // rotation 270: the opposite corner, 180 degrees away — the picture's
  // top-left is the panel's bottom-LEFT, the first pixel of the master
  // controller's last row.
  NativePos p270 = find_marker(landscape.data(), 270, 0xF);
  CHECK_EQ_INT(0, p270.px);
  CHECK_EQ_INT(NATIVE_HEIGHT - 1, p270.py);
}

// The same mapping read the other way round: which pixel of the picture is the
// first one each controller is handed for the panel's first row.
TEST(first_transmitted_pixel_of_each_half) {
  // rotation 0 (portrait buffer): native (0, 0) and (600, 0) are just the
  // picture's own (0, 0) and (600, 0).
  std::vector<uint8_t> portrait = make_image(NATIVE_WIDTH, NATIVE_HEIGHT);
  CHECK_EQ_INT(pattern(0, 0), native_pixel(portrait.data(), 0, 0, 0));
  CHECK_EQ_INT(pattern(NATIVE_WIDTH / 2, 0), native_pixel(portrait.data(), 0, NATIVE_WIDTH / 2, 0));

  std::vector<uint8_t> landscape = make_image(LANDSCAPE_WIDTH, LANDSCAPE_HEIGHT);

  // rotation 90: native (px, py) is picture pixel (py, 1199 - px), so the
  // panel's top-left corner comes from the picture's BOTTOM-left corner...
  CHECK_EQ_INT(pattern(0, LANDSCAPE_HEIGHT - 1), native_pixel(landscape.data(), 90, 0, 0));
  // ...and the slave's first pixel from the middle of the picture's left edge.
  CHECK_EQ_INT(pattern(0, LANDSCAPE_HEIGHT / 2 - 1), native_pixel(landscape.data(), 90, NATIVE_WIDTH / 2, 0));

  // rotation 270: native (px, py) is picture pixel (1599 - py, px), so the
  // panel's top-left corner comes from the picture's TOP-right corner...
  CHECK_EQ_INT(pattern(LANDSCAPE_WIDTH - 1, 0), native_pixel(landscape.data(), 270, 0, 0));
  // ...and the slave's first pixel from the middle of the picture's right edge.
  CHECK_EQ_INT(pattern(LANDSCAPE_WIDTH - 1, NATIVE_WIDTH / 2), native_pixel(landscape.data(), 270, NATIVE_WIDTH / 2, 0));

  // Guard against the assertions above passing on a coincidence of the 4-bit
  // pattern: the two landscape mounts really do disagree at that corner.
  CHECK(native_pixel(landscape.data(), 90, 0, 0) != native_pixel(landscape.data(), 270, 0, 0));
}
