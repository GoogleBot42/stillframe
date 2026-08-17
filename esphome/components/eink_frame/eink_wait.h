#pragma once

// Driver-side helpers that do need the ESPHome runtime (GPIO + watchdog).
// Kept out of eink_frame.h so that header stays host-testable.

#include <cstdint>

#include "esphome/core/gpio.h"

namespace esphome {
namespace eink_frame {

// Feed the task watchdog from a long blocking operation on the main loop
// (a panel busy-wait, an SPI frame transfer, the HTTP download loop).
//
// Prefer this over calling App.feed_wdt() directly. App.feed_wdt() ends up in
// esp_task_wdt_reset(), which on ESP32 logs
//
//     E task_wdt: esp_task_wdt_reset(707): task not found
//
// whenever the *calling* FreeRTOS task is not subscribed to the task watchdog.
// ESPHome subscribes exactly one task — its own loopTask, from arch_init()
// (esphome/components/esp32/hal.cpp: `esp_task_wdt_add(nullptr)`) — so a feed
// from any other task is both useless and an error log per feed interval.
// Checking esp_task_wdt_status() first is what upstream itself does in
// http_request (HttpContainerIDF::feed_wdt) and runtime_image (jpeg_decoder).
//
// The check is ESP32-only (both the arduino and esp-idf frameworks: ESPHome
// builds arduino-esp32 as an ESP-IDF component, so esp_task_wdt.h and the
// task WDT exist either way); elsewhere this is a plain App.feed_wdt().
//
// NOTE: feeding is not a substitute for raising the watchdog around a call
// that blocks *without* returning to us — for those, the timeout itself has to
// be raised for the duration (see `watchdog_timeout` on http_request in
// esphome/common.yaml, which wraps esp_http_client in a WatchdogManager).
void feed_watchdog();

// Poll `pin` until it reads `ready_level`, feeding the watchdog while waiting.
// Returns false (and logs under `tag`) on timeout; `what` names the pin in that
// message, e.g. "busy pin" or "HRDY".
bool wait_for_pin(GPIOPin *pin, bool ready_level, uint32_t timeout_ms, const char *tag, const char *what);

}  // namespace eink_frame
}  // namespace esphome
