// EL133UF1 frame splitting: the buffered image has to be cut into the two
// controllers' halves, rotating on the way when the frame is landscape.

#include "esphome/components/el133uf1/el133uf1_image.h"
#include "test.h"

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
