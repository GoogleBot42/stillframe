#pragma once

// Driver-side helpers that do need the ESPHome runtime (GPIO + watchdog).
// Kept out of eink_frame.h so that header stays host-testable.

#include <cstdint>

#include "esphome/core/gpio.h"

namespace esphome {
namespace eink_frame {

// Poll `pin` until it reads `ready_level`, feeding the watchdog while waiting.
// Returns false (and logs under `tag`) on timeout; `what` names the pin in that
// message, e.g. "busy pin" or "HRDY".
bool wait_for_pin(GPIOPin *pin, bool ready_level, uint32_t timeout_ms, const char *tag, const char *what);

}  // namespace eink_frame
}  // namespace esphome
