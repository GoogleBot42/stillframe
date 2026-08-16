#pragma once

// Pure (host-testable) part of the IT8951 image path: turning the server's byte
// stream into the 16-bit words the controller expects.
//
// No ESPHome includes here — see esphome/tests/.

#include <cstddef>
#include <cstdint>

namespace esphome {
namespace it8951_spi {

// The IT8951 is fed 16-bit words, and each pair of server bytes (b0, b1) is
// sent high-byte-swapped as (b1, b0) — matching the legacy firmware's word
// writes. Chunks of the HTTP response do not respect pair boundaries, so an odd
// trailing byte is carried over to the next chunk.
class WordPacker {
 public:
  void reset() { this->have_carry_ = false; }

  // Feed `len` bytes, collecting swapped pairs into `out` (which must hold
  // `capacity` bytes, an even number). `flush(out, n)` is called whenever the
  // buffer fills and once more at the end of the call if anything is left.
  //
  // A byte held back as carry is not passed on until its partner arrives; if
  // the stream ends on an odd byte it is simply dropped (the frame server never
  // sends an odd number of bytes: every image is width*height/2 bytes).
  template<typename FlushFn>
  void feed(const uint8_t *data, size_t len, uint8_t *out, size_t capacity, FlushFn flush) {
    size_t out_len = 0;
    for (size_t i = 0; i < len; i++) {
      if (!this->have_carry_) {
        this->carry_byte_ = data[i];
        this->have_carry_ = true;
        continue;
      }
      out[out_len++] = data[i];
      out[out_len++] = this->carry_byte_;
      this->have_carry_ = false;

      if (out_len == capacity) {
        flush(out, out_len);
        out_len = 0;
      }
    }
    if (out_len > 0)
      flush(out, out_len);
  }

 protected:
  uint8_t carry_byte_{0};
  bool have_carry_{false};
};

}  // namespace it8951_spi
}  // namespace esphome
