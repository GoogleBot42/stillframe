// The begin/write/finish accounting that used to be copy-pasted into each of
// the three drivers.

#include "esphome/components/eink_frame/eink_frame.h"
#include "test.h"

#include <algorithm>

using namespace esphome::eink_frame;

namespace {

// Stands in for a panel driver: records the hook calls the base class makes.
class FakeDisplay : public EinkFrameDisplay {
 public:
  FakeDisplay(int width, int height) {
    this->set_width(width);
    this->set_height(height);
  }

  void wake() override { this->events.push_back("wake"); }
  void sleep() override { this->events.push_back("sleep"); }

  // Make the transfer fail from inside on_begin_image_(), the way el133uf1 does
  // when the PSRAM buffer cannot be allocated.
  bool fail_on_begin{false};

  std::vector<std::string> events;
  std::vector<uint8_t> received;
  std::vector<size_t> chunk_sizes;
  int finish_calls{0};
  bool last_complete{false};

 protected:
  const char *frame_tag_() const override { return "fake"; }
  const ColorSpace &get_color_space() const override { return COLOR7_COLOR_SPACE; }

  void on_begin_image_() override {
    this->events.push_back("begin");
    if (this->fail_on_begin)
      this->mark_transfer_failed_();
  }

  void on_image_data_(const uint8_t *data, size_t len) override {
    this->events.push_back("data");
    this->chunk_sizes.push_back(len);
    this->received.insert(this->received.end(), data, data + len);
  }

  void on_image_end_() override { this->events.push_back("end"); }

  void on_finish_image_(bool complete) override {
    this->events.push_back(complete ? "finish(true)" : "finish(false)");
    this->finish_calls++;
    this->last_complete = complete;
  }
};

std::string join(const std::vector<std::string> &parts) {
  std::string out;
  for (const auto &part : parts) {
    if (!out.empty())
      out += ",";
    out += part;
  }
  return out;
}

std::string join_sizes(const std::vector<size_t> &sizes) {
  std::string out;
  for (size_t size : sizes) {
    if (!out.empty())
      out += ",";
    out += std::to_string(size);
  }
  return out;
}

std::vector<uint8_t> ramp(size_t len, uint8_t start = 0) {
  std::vector<uint8_t> data(len);
  for (size_t i = 0; i < len; i++)
    data[i] = (uint8_t) (start + i);
  return data;
}

}  // namespace

// Every test below uses a 6x4 "panel", i.e. 12 bytes of image data — small
// enough to reason about chunk boundaries by hand.
TEST(image_byte_count_drives_the_session) {
  FakeDisplay display(6, 4);
  CHECK_EQ_INT(12, display.get_image_byte_count());
}

TEST(complete_transfer_refreshes_the_panel) {
  FakeDisplay display(6, 4);
  std::vector<uint8_t> data = ramp(12);

  display.begin_image();
  display.write_image_data(data.data(), 6);
  display.write_image_data(data.data() + 6, 6);
  display.finish_image(true);

  CHECK_EQ_STR("begin,data,data,end,finish(true)", join(display.events));
  CHECK_EQ_STR("6,6", join_sizes(display.chunk_sizes));
  CHECK(display.received == data);
  CHECK(display.last_complete);
}

TEST(chunks_are_passed_through_unchanged) {
  FakeDisplay display(6, 4);
  std::vector<uint8_t> data = ramp(12, 0x40);

  display.begin_image();
  for (size_t offset = 0; offset < data.size(); offset += 5)
    display.write_image_data(data.data() + offset, std::min<size_t>(5, data.size() - offset));
  display.finish_image(true);

  // Last chunk is clamped from 5 to the 2 bytes that are still missing.
  CHECK_EQ_STR("5,5,2", join_sizes(display.chunk_sizes));
  CHECK(display.received == data);
  CHECK(display.last_complete);
}

TEST(overrun_is_clamped_at_the_chunk_that_crosses_the_end) {
  FakeDisplay display(6, 4);
  std::vector<uint8_t> data = ramp(40);

  display.begin_image();
  display.write_image_data(data.data(), 8);
  display.write_image_data(data.data() + 8, 8);   // only 4 bytes still fit
  display.write_image_data(data.data() + 16, 8);  // dropped entirely
  display.finish_image(true);

  CHECK_EQ_STR("8,4", join_sizes(display.chunk_sizes));
  CHECK_EQ_INT(12, display.received.size());
  CHECK(display.received == std::vector<uint8_t>(data.begin(), data.begin() + 12));
  CHECK(display.last_complete);
}

TEST(zero_length_writes_are_ignored) {
  FakeDisplay display(6, 4);
  std::vector<uint8_t> data = ramp(12);

  display.begin_image();
  display.write_image_data(data.data(), 0);
  display.write_image_data(data.data(), 12);
  display.write_image_data(data.data(), 0);
  display.finish_image(true);

  CHECK_EQ_STR("begin,data,end,finish(true)", join(display.events));
}

TEST(short_transfer_is_not_shown) {
  FakeDisplay display(6, 4);
  std::vector<uint8_t> data = ramp(11);

  display.begin_image();
  display.write_image_data(data.data(), 11);
  display.finish_image(true);

  CHECK_EQ_STR("begin,data,end,finish(false)", join(display.events));
  CHECK(!display.last_complete);
}

TEST(failed_download_is_not_shown_even_when_all_bytes_arrived) {
  FakeDisplay display(6, 4);
  std::vector<uint8_t> data = ramp(12);

  display.begin_image();
  display.write_image_data(data.data(), 12);
  display.finish_image(false);

  CHECK_EQ_STR("begin,data,end,finish(false)", join(display.events));
}

TEST(driver_side_failure_drops_the_remaining_chunks) {
  FakeDisplay display(6, 4);
  display.fail_on_begin = true;
  std::vector<uint8_t> data = ramp(12);

  display.begin_image();
  display.write_image_data(data.data(), 12);
  display.finish_image(true);

  CHECK_EQ_STR("begin,end,finish(false)", join(display.events));
  CHECK_EQ_INT(0, display.received.size());
}

TEST(begin_image_resets_the_previous_transfer) {
  FakeDisplay display(6, 4);
  display.fail_on_begin = true;
  std::vector<uint8_t> data = ramp(12);

  display.begin_image();
  display.write_image_data(data.data(), 4);
  display.finish_image(true);
  CHECK(!display.last_complete);

  // Second attempt succeeds: the failure flag and the byte counter are cleared.
  display.fail_on_begin = false;
  display.events.clear();
  display.chunk_sizes.clear();
  display.received.clear();

  display.begin_image();
  display.write_image_data(data.data(), 12);
  display.finish_image(true);

  CHECK_EQ_STR("begin,data,end,finish(true)", join(display.events));
  CHECK_EQ_STR("12", join_sizes(display.chunk_sizes));
  CHECK(display.last_complete);
}

// it8951 has to close its image-load command whether or not the download
// worked, so the end hook always runs, and always before the finish hook.
TEST(end_hook_runs_before_finish_on_every_path) {
  for (bool ok : {true, false}) {
    FakeDisplay display(6, 4);
    display.begin_image();
    display.finish_image(ok);
    CHECK_EQ_STR("begin,end,finish(false)", join(display.events));
    CHECK_EQ_INT(1, display.finish_calls);
  }
}

TEST(failure_is_logged_with_the_byte_counts) {
  host_log_lines().clear();
  FakeDisplay display(6, 4);
  std::vector<uint8_t> data = ramp(5);

  display.begin_image();
  display.write_image_data(data.data(), 5);
  display.finish_image(true);

  CHECK_EQ_INT(1, host_log_lines().size());
  if (!host_log_lines().empty())
    CHECK_EQ_STR("E/fake: Image transfer failed (5/12 bytes) — skipping refresh", host_log_lines()[0]);
  host_log_lines().clear();
}

TEST(success_is_logged_around_the_refresh) {
  host_log_lines().clear();
  FakeDisplay display(6, 4);
  std::vector<uint8_t> data = ramp(12);

  display.begin_image();
  display.write_image_data(data.data(), 12);
  display.finish_image(true);

  CHECK_EQ_INT(2, host_log_lines().size());
  if (host_log_lines().size() == 2) {
    CHECK_EQ_STR("I/fake: Image data sent, refreshing display...", host_log_lines()[0]);
    CHECK_EQ_STR("I/fake: Display refresh complete", host_log_lines()[1]);
  }
  host_log_lines().clear();
}
