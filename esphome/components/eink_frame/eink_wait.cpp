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

bool wait_for_pin(GPIOPin *pin, bool ready_level, uint32_t timeout_ms, const char *tag, const char *phase,
                  const char *hint) {
  uint32_t start = millis();
  while (pin->digital_read() != ready_level) {
    if (millis() - start > timeout_ms) {
      // Report the level actually read rather than just "timed out": a pin
      // stuck at the *wrong* level for the whole budget and a pin that is
      // floating/undriven look identical in a bare timeout message.
      ESP_LOGE(tag, "%s: timed out after %" PRIu32 " ms — pin still reads %s, expected %s", phase,
               millis() - start, pin->digital_read() ? "HIGH" : "LOW", ready_level ? "HIGH" : "LOW");
      if (hint != nullptr)
        ESP_LOGE(tag, "  %s", hint);
      return false;
    }
    feed_watchdog();
    delay(1);
  }
  ESP_LOGV(tag, "%s: ready after %" PRIu32 " ms", phase, millis() - start);
  return true;
}

}  // namespace eink_frame
}  // namespace esphome
