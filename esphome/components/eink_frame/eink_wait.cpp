#include "eink_wait.h"

#include "esphome/core/application.h"
#include "esphome/core/hal.h"
#include "esphome/core/log.h"

#include <cinttypes>

namespace esphome {
namespace eink_frame {

bool wait_for_pin(GPIOPin *pin, bool ready_level, uint32_t timeout_ms, const char *tag, const char *what) {
  uint32_t start = millis();
  while (pin->digital_read() != ready_level) {
    if (millis() - start > timeout_ms) {
      ESP_LOGE(tag, "Timeout (%" PRIu32 " ms) waiting for %s", timeout_ms, what);
      return false;
    }
    App.feed_wdt();
    delay(1);
  }
  return true;
}

}  // namespace eink_frame
}  // namespace esphome
