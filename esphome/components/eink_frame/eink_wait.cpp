#include "eink_wait.h"

#include "esphome/core/application.h"
#include "esphome/core/hal.h"
#include "esphome/core/log.h"

#include <cinttypes>

#ifdef USE_ESP32
#include <esp_task_wdt.h>
#endif

namespace esphome {
namespace eink_frame {

void feed_watchdog() {
#ifdef USE_ESP32
  // esp_task_wdt_status() reports an unsubscribed task quietly
  // (ESP_ERR_NOT_FOUND); esp_task_wdt_reset() would log an error instead.
  if (esp_task_wdt_status(nullptr) != ESP_OK)
    return;
#endif
  App.feed_wdt();
}

bool wait_for_pin(GPIOPin *pin, bool ready_level, uint32_t timeout_ms, const char *tag, const char *what) {
  uint32_t start = millis();
  while (pin->digital_read() != ready_level) {
    if (millis() - start > timeout_ms) {
      ESP_LOGE(tag, "Timeout (%" PRIu32 " ms) waiting for %s", timeout_ms, what);
      return false;
    }
    feed_watchdog();
    delay(1);
  }
  return true;
}

}  // namespace eink_frame
}  // namespace esphome
