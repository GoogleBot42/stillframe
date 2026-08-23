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
// NOTE: feeding is no help at all for a call that blocks *without* returning
// to us — esp_http_client's synchronous post(), for instance, never lets the
// loop task reach a feed. Such calls are covered only by the watchdog timeout
// itself being long enough, which is what the global `esp32: watchdog_timeout:
// 60s` in esphome/common.yaml is for (see the http_request block there for why
// there is deliberately no per-request override).
void feed_watchdog();

// Poll `pin` until it reads `ready_level`, feeding the watchdog while waiting.
// Returns false on timeout.
//
// `phase` names what is being waited for and is the first thing in both the
// success (verbose) and the timeout (error) log line, so make it specific
// enough to identify the call site in a hardware log — e.g.
// "panel power-up (PON)" rather than "busy pin". The timeout message also
// reports how long was waited and the level actually read, and `hint`, if
// given, is logged as a follow-up line suggesting what to check on the bench.
bool wait_for_pin(GPIOPin *pin, bool ready_level, uint32_t timeout_ms, const char *tag, const char *phase,
                  const char *hint = nullptr);

}  // namespace eink_frame
}  // namespace esphome
