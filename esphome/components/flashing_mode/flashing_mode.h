#pragma once

// Reboot into the ESP32 ROM's serial downloader ("download mode"), so the
// device can be reflashed over USB without a BOOT button or a working
// auto-download circuit. See __init__.py for why this exists.

#include "esphome/core/component.h"

namespace esphome {
namespace flashing_mode {

// Sets the force-download-boot bit in the RTC domain and reboots. Does not
// return on a supported chip; logs an error and returns on anything else.
void enter_flashing_mode();

// Clears the force-download-boot bit on every boot. That the app is running at
// all proves the bit did not take effect (or it was set by a flash session that
// is now over), and the bit is sticky across everything except a power cycle or
// an EN/RESET pulse — including deep-sleep wakes, which a picture frame does
// constantly. Clearing it here is what guarantees "reboot into flashing mode"
// stays a one-shot instead of arming every future wake-up.
class FlashingModeComponent : public Component {
 public:
  void setup() override;
  void dump_config() override;
  // Before anything else can reboot the device for its own reasons.
  float get_setup_priority() const override { return setup_priority::HARDWARE; }
};

}  // namespace flashing_mode
}  // namespace esphome
