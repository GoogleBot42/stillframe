// The JSON body the frame posts to the server is a wire format: the strings
// below are the exact bodies the three drivers sent before the shared
// eink_frame base existed, and the server parses them as-is (including the
// misspelled "flip_horizonal" key).

#include "esphome/components/eink_frame/eink_frame.h"
#include "test.h"

#include <cstdio>

using namespace esphome::eink_frame;

static const char *const COLOR7_BODY_800X480 =
    "{\"width\":800,\"height\":480,\"flip_vertical\":false,\"flip_horizonal\":false,"
    "\"color_space\":["
    "{\"color_code\":0,\"rgb_color\":[0,0,0]},"
    "{\"color_code\":1,\"rgb_color\":[1,1,1]},"
    "{\"color_code\":2,\"rgb_color\":[0.059,0.329,0.119]},"
    "{\"color_code\":3,\"rgb_color\":[0.061,0.147,0.336]},"
    "{\"color_code\":4,\"rgb_color\":[0.574,0.066,0.010]},"
    "{\"color_code\":5,\"rgb_color\":[0.982,0.756,0.004]},"
    "{\"color_code\":6,\"rgb_color\":[0.795,0.255,0.018]}]}";

// The shipped grey16 configs set flip_horizontal.
static const char *const GREY16_BODY_1872X1404 =
    "{\"width\":1872,\"height\":1404,\"flip_vertical\":false,\"flip_horizonal\":true,"
    "\"color_space\":["
    "{\"color_code\":0,\"rgb_color\":[0.0000,0.0000,0.0000]},"
    "{\"color_code\":1,\"rgb_color\":[0.0667,0.0667,0.0667]},"
    "{\"color_code\":2,\"rgb_color\":[0.1333,0.1333,0.1333]},"
    "{\"color_code\":3,\"rgb_color\":[0.2000,0.2000,0.2000]},"
    "{\"color_code\":4,\"rgb_color\":[0.2667,0.2667,0.2667]},"
    "{\"color_code\":5,\"rgb_color\":[0.3333,0.3333,0.3333]},"
    "{\"color_code\":6,\"rgb_color\":[0.4000,0.4000,0.4000]},"
    "{\"color_code\":7,\"rgb_color\":[0.4667,0.4667,0.4667]},"
    "{\"color_code\":8,\"rgb_color\":[0.5333,0.5333,0.5333]},"
    "{\"color_code\":9,\"rgb_color\":[0.6000,0.6000,0.6000]},"
    "{\"color_code\":10,\"rgb_color\":[0.6667,0.6667,0.6667]},"
    "{\"color_code\":11,\"rgb_color\":[0.7333,0.7333,0.7333]},"
    "{\"color_code\":12,\"rgb_color\":[0.8000,0.8000,0.8000]},"
    "{\"color_code\":13,\"rgb_color\":[0.8667,0.8667,0.8667]},"
    "{\"color_code\":14,\"rgb_color\":[0.9333,0.9333,0.9333]},"
    "{\"color_code\":15,\"rgb_color\":[1.0000,1.0000,1.0000]}]}";

// rotation: 90 asks for the landscape orientation of the 1200x1600 panel.
static const char *const SPECTRA6_BODY_1600X1200 =
    "{\"width\":1600,\"height\":1200,\"flip_vertical\":false,\"flip_horizonal\":false,"
    "\"color_space\":["
    "{\"color_code\":0,\"rgb_color\":[0,0,0]},"
    "{\"color_code\":1,\"rgb_color\":[1,1,1]},"
    "{\"color_code\":2,\"rgb_color\":[0.982,0.756,0.004]},"
    "{\"color_code\":3,\"rgb_color\":[0.574,0.066,0.010]},"
    "{\"color_code\":5,\"rgb_color\":[0.061,0.147,0.336]},"
    "{\"color_code\":6,\"rgb_color\":[0.059,0.329,0.119]}]}";

TEST(color7_request_body_matches_wire_format) {
  CHECK_EQ_STR(COLOR7_BODY_800X480, build_image_request_body(800, 480, false, false, COLOR7_COLOR_SPACE));
}

TEST(grey16_request_body_matches_wire_format) {
  CHECK_EQ_STR(GREY16_BODY_1872X1404, build_image_request_body(1872, 1404, false, true, GREY16_COLOR_SPACE));
}

TEST(spectra6_request_body_matches_wire_format) {
  CHECK_EQ_STR(SPECTRA6_BODY_1600X1200, build_image_request_body(1600, 1200, false, false, SPECTRA6_COLOR_SPACE));
}

TEST(flip_flags_are_json_booleans) {
  const struct {
    bool vertical;
    bool horizontal;
    const char *expected;
  } cases[] = {
      {false, false, "\"flip_vertical\":false,\"flip_horizonal\":false,"},
      {true, false, "\"flip_vertical\":true,\"flip_horizonal\":false,"},
      {false, true, "\"flip_vertical\":false,\"flip_horizonal\":true,"},
      {true, true, "\"flip_vertical\":true,\"flip_horizonal\":true,"},
  };
  for (const auto &c : cases) {
    std::string body = build_image_request_body(800, 480, c.vertical, c.horizontal, COLOR7_COLOR_SPACE);
    CHECK(body.find(c.expected) != std::string::npos);
  }
}

// The grey16 palette used to be formatted at runtime from i / 15.0f. It is now
// a static table; this pins the two together.
TEST(grey16_palette_matches_float_formula) {
  CHECK_EQ_INT(16, (int) GREY16_COLOR_SPACE.count);
  for (int i = 0; i < 16; i++) {
    char expected[16];
    snprintf(expected, sizeof(expected), "%.4f", i / 15.0f);
    const ColorSpaceEntry &entry = GREY16_ENTRIES[i];
    CHECK_EQ_INT(i, entry.code);
    CHECK_EQ_STR(expected, entry.r);
    CHECK_EQ_STR(expected, entry.g);
    CHECK_EQ_STR(expected, entry.b);
  }
}

TEST(color_space_tables_have_the_expected_codes) {
  CHECK_EQ_INT(7, (int) COLOR7_COLOR_SPACE.count);
  for (size_t i = 0; i < COLOR7_COLOR_SPACE.count; i++)
    CHECK_EQ_INT(i, COLOR7_COLOR_SPACE.entries[i].code);

  // Spectra 6 has six pigments but skips color code 4.
  const uint8_t spectra6_codes[] = {0, 1, 2, 3, 5, 6};
  CHECK_EQ_INT(6, (int) SPECTRA6_COLOR_SPACE.count);
  for (size_t i = 0; i < SPECTRA6_COLOR_SPACE.count; i++)
    CHECK_EQ_INT(spectra6_codes[i], SPECTRA6_COLOR_SPACE.entries[i].code);
}

// The pigments the 7-color and Spectra 6 panels have in common must describe
// the same RGB values to the server.
TEST(shared_pigments_agree_between_panels) {
  const struct {
    int color7_index;
    int spectra6_index;
  } shared[] = {
      {0, 0},  // black
      {1, 1},  // white
      {2, 5},  // green
      {3, 4},  // blue
      {4, 3},  // red
      {5, 2},  // yellow
  };
  for (const auto &pair : shared) {
    const ColorSpaceEntry &a = COLOR7_ENTRIES[pair.color7_index];
    const ColorSpaceEntry &b = SPECTRA6_ENTRIES[pair.spectra6_index];
    CHECK_EQ_STR(a.r, b.r);
    CHECK_EQ_STR(a.g, b.g);
    CHECK_EQ_STR(a.b, b.b);
  }
}

TEST(byte_count_is_two_pixels_per_byte) {
  CHECK_EQ_INT(192000, packed_4bpp_byte_count(800, 480));      // epd7in3f
  CHECK_EQ_INT(1314144, packed_4bpp_byte_count(1872, 1404));   // it8951
  CHECK_EQ_INT(960000, packed_4bpp_byte_count(1600, 1200));    // el133uf1, landscape
  CHECK_EQ_INT(960000, packed_4bpp_byte_count(1200, 1600));    // el133uf1, portrait
}

// The server packs nibbles as one continuous stream with no per-row padding
// (packBytesIntoNibbles in server/einkimage.go), so an odd pixel count simply
// rounds down — rows are not byte aligned.
//
// This is a deliberate behaviour change for epd7in3f, which used to compute
// (width / 2) * height (i.e. one padding nibble per odd row): for 801x481 that
// is 192400 bytes, not the 192640 the server actually streams. The (w * h) / 2
// math below is the fix, so the numbers here are pinned to the server, not to
// the old driver.
TEST(byte_count_of_odd_geometry_rounds_down) {
  CHECK_EQ_INT(192640, packed_4bpp_byte_count(801, 481));
  CHECK_EQ_INT(4, packed_4bpp_byte_count(3, 3));
  CHECK_EQ_INT(0, packed_4bpp_byte_count(1, 1));
  CHECK_EQ_INT(0, packed_4bpp_byte_count(0, 480));
  CHECK_EQ_INT(0, packed_4bpp_byte_count(-800, 480));
}
