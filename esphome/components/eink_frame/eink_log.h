#pragma once

// Logging shim for the panel-independent frame logic.
//
// Everything in eink_frame.h / eink_frame.cpp is deliberately free of ESPHome
// runtime dependencies so it can be compiled and unit tested on the host (see
// esphome/tests/). Logging is the one thing that would drag the runtime in, so
// it goes through these two macros: on device they are ESPHome's log macros, on
// the host they append to an in-memory log the tests can assert on.

#ifdef EINK_FRAME_HOST_TEST

#include <cstdarg>
#include <cstdio>
#include <string>
#include <vector>

namespace esphome {
namespace eink_frame {

// Captured log lines, as "<level>/<tag>: <message>". Tests read and clear this.
inline std::vector<std::string> &host_log_lines() {
  static std::vector<std::string> lines;
  return lines;
}

inline void host_log(const char *level, const char *tag, const char *format, ...) {
  char message[512];
  va_list args;
  va_start(args, format);
  vsnprintf(message, sizeof(message), format, args);
  va_end(args);
  host_log_lines().push_back(std::string(level) + "/" + tag + ": " + message);
}

}  // namespace eink_frame
}  // namespace esphome

#define EINK_LOGE(tag, ...) ::esphome::eink_frame::host_log("E", tag, __VA_ARGS__)
#define EINK_LOGI(tag, ...) ::esphome::eink_frame::host_log("I", tag, __VA_ARGS__)

#else

#include "esphome/core/log.h"

#define EINK_LOGE ESP_LOGE
#define EINK_LOGI ESP_LOGI

#endif
