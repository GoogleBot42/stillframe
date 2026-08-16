// IT8951 byte-pair swapping and burst splitting across HTTP chunk boundaries.

#include "esphome/components/it8951_spi/it8951_words.h"
#include "test.h"

#include <functional>

using esphome::it8951_spi::WordPacker;

namespace {

// Records what would have gone out over SPI.
struct Recorder {
  std::vector<std::vector<uint8_t>> bursts;
  std::vector<uint8_t> flat;

  void operator()(const uint8_t *data, size_t len) {
    this->bursts.push_back(std::vector<uint8_t>(data, data + len));
    this->flat.insert(this->flat.end(), data, data + len);
  }
};

std::vector<uint8_t> ramp(size_t len) {
  std::vector<uint8_t> data(len);
  for (size_t i = 0; i < len; i++)
    data[i] = (uint8_t) (i + 1);
  return data;
}

// What the packer should produce: every pair (b0, b1) swapped to (b1, b0).
std::vector<uint8_t> swapped_pairs(const std::vector<uint8_t> &input) {
  std::vector<uint8_t> out;
  for (size_t i = 0; i + 1 < input.size(); i += 2) {
    out.push_back(input[i + 1]);
    out.push_back(input[i]);
  }
  return out;
}

}  // namespace

TEST(word_packer_swaps_byte_pairs) {
  WordPacker packer;
  Recorder recorder;
  uint8_t buffer[8];
  const uint8_t input[] = {0x11, 0x22, 0x33, 0x44};

  packer.feed(input, sizeof(input), buffer, sizeof(buffer), std::ref(recorder));

  CHECK_EQ_INT(1, recorder.bursts.size());
  CHECK(recorder.flat == std::vector<uint8_t>({0x22, 0x11, 0x44, 0x33}));
}

TEST(word_packer_flushes_a_full_buffer) {
  WordPacker packer;
  Recorder recorder;
  uint8_t buffer[4];
  std::vector<uint8_t> input = ramp(12);

  packer.feed(input.data(), input.size(), buffer, sizeof(buffer), std::ref(recorder));

  // 12 bytes -> 12 output bytes -> three full 4-byte bursts, no remainder.
  CHECK_EQ_INT(3, recorder.bursts.size());
  for (const auto &burst : recorder.bursts)
    CHECK_EQ_INT(4, burst.size());
  CHECK(recorder.flat == swapped_pairs(input));
}

TEST(word_packer_flushes_a_partial_buffer_at_the_end_of_a_chunk) {
  WordPacker packer;
  Recorder recorder;
  uint8_t buffer[8];
  std::vector<uint8_t> input = ramp(6);

  packer.feed(input.data(), input.size(), buffer, sizeof(buffer), std::ref(recorder));

  CHECK_EQ_INT(1, recorder.bursts.size());
  CHECK_EQ_INT(6, recorder.bursts[0].size());
  CHECK(recorder.flat == swapped_pairs(input));
}

// The HTTP response arrives in chunks that do not respect pair boundaries: the
// odd byte at the end of a chunk has to wait for the next one.
TEST(word_packer_carries_the_odd_byte_across_chunks) {
  std::vector<uint8_t> input = ramp(64);

  for (size_t chunk : {1u, 3u, 5u, 7u, 16u, 63u}) {
    WordPacker packer;
    Recorder recorder;
    uint8_t buffer[8];

    for (size_t offset = 0; offset < input.size(); offset += chunk) {
      size_t len = input.size() - offset < chunk ? input.size() - offset : chunk;
      packer.feed(input.data() + offset, len, buffer, sizeof(buffer), std::ref(recorder));
    }

    CHECK(recorder.flat == swapped_pairs(input));
  }
}

// A one-byte chunk produces nothing until its partner arrives.
TEST(word_packer_emits_nothing_for_a_lone_byte) {
  WordPacker packer;
  Recorder recorder;
  uint8_t buffer[8];
  const uint8_t first[] = {0xAB};
  const uint8_t second[] = {0xCD};

  packer.feed(first, 1, buffer, sizeof(buffer), std::ref(recorder));
  CHECK_EQ_INT(0, recorder.bursts.size());

  packer.feed(second, 1, buffer, sizeof(buffer), std::ref(recorder));
  CHECK_EQ_INT(1, recorder.bursts.size());
  CHECK(recorder.flat == std::vector<uint8_t>({0xCD, 0xAB}));
}

// reset() runs at the start of every image, so a half pair left over from an
// aborted transfer must not shift the next image by one byte.
TEST(word_packer_reset_drops_a_pending_carry) {
  WordPacker packer;
  Recorder recorder;
  uint8_t buffer[8];
  const uint8_t leftover[] = {0xFF};
  const uint8_t image[] = {0x11, 0x22};

  packer.feed(leftover, 1, buffer, sizeof(buffer), std::ref(recorder));
  packer.reset();
  packer.feed(image, sizeof(image), buffer, sizeof(buffer), std::ref(recorder));

  CHECK(recorder.flat == std::vector<uint8_t>({0x22, 0x11}));
}
