#include "flashing_mode.h"

#include "esphome/core/defines.h"
#include "esphome/core/log.h"

#ifdef USE_ESP32
#include "esphome/core/application.h"
#include "esphome/core/hal.h"

// esp_rom_software_reset_system(), and RTC_CNTL_OPTION1_REG /
// RTC_CNTL_FORCE_DOWNLOAD_BOOT where the chip has them (soc/rtc_cntl_reg.h
// itself pulls in soc/soc.h for REG_WRITE).
#include <esp_rom_sys.h>
#include <soc/rtc_cntl_reg.h>
#include <soc/soc.h>
#endif

namespace esphome {
namespace flashing_mode {

static const char *const TAG = "flashing_mode";

// The force-download-boot bit arrived with the ESP32-S2, whose on-chip USB has
// no external bridge to emulate the DTR/RTS auto-reset circuit for it. So it
// exists on the S2/S3 (and on the C-series, where it moved into LP_AON under a
// different name) but NOT on the classic ESP32, whose soc/rtc_cntl_reg.h
// consequently does not define RTC_CNTL_OPTION1_REG at all — for that chip,
// GPIO0-low-at-reset is the only way in, which is why every classic-ESP32 board
// carries a BOOT button. Feature-detecting the macro is therefore exactly the
// right test: present on every chip that supports this, absent on every chip
// that does not, with no per-variant #ifdef ladder to maintain.
#if defined(USE_ESP32) && defined(RTC_CNTL_OPTION1_REG) && defined(RTC_CNTL_FORCE_DOWNLOAD_BOOT)
#define STILLFRAME_HAS_FORCE_DOWNLOAD_BOOT
#endif

void enter_flashing_mode() {
#ifdef STILLFRAME_HAS_FORCE_DOWNLOAD_BOOT
  ESP_LOGI(TAG, "Rebooting into flashing mode — reflash via USB, power-cycle to exit");
  // Let the log line (and any API/OTA socket) drain before the chip goes away,
  // the same courtesy delay the stock restart button takes.
  delay(100);  // NOLINT
  // The shutdown half of App.safe_reboot(): flush preferences, disconnect the
  // API/WiFi, power components down. Only the restart itself is done by hand
  // below.
  App.run_safe_shutdown_hooks();
  App.teardown_components(TEARDOWN_TIMEOUT_REBOOT_MS);
  App.run_powerdown_hooks();
  // RTC_CNTL_OPTION1_REG lives in the always-on RTC domain, so it outlives a
  // digital-system reset, and the ROM reads it on the way back up: bit set ->
  // start the serial downloader instead of the app (the boot log then reads
  // `boot:0x21 (DOWNLOAD(USB/UART0))`). ESP-IDF and arduino-esp32 both set this
  // very bit to implement "reboot into the bootloader" over USB
  // (esp_usb_console_before_restart(), usb_persist_shutdown_handler()).
  REG_WRITE(RTC_CNTL_OPTION1_REG, RTC_CNTL_FORCE_DOWNLOAD_BOOT);
  // Reset through the ROM rather than esp_restart(), deliberately.
  // esp_restart_noos() arms the RTC watchdog for 1 s with flash-boot protection
  // on its way out (components/esp_system/port/soc/esp32s3/system_internal.c),
  // expecting a bootloader to disable it — but nothing does when the ROM stops
  // in the downloader instead of booting the app, and only *one* of the two
  // flashing tools cleans up after that: python esptool disables the RTC WDT
  // solely on a USB-JTAG/Serial connection (esptool/targets/esp32s3.py
  // disable_watchdogs()), and esptool-js — the browser installer, i.e. the
  // whole point of this feature — never does. A watchdog left ticking would
  // reset the chip out from under the flash a second after it arrives.
  // esp_rom_software_reset_system() performs the same system reset without
  // arming anything, and the shutdown hooks above already did the cleanup that
  // makes esp_restart() the polite choice elsewhere.
  esp_rom_software_reset_system();
#else
  ESP_LOGE(TAG,
           "This chip has no software-forced download mode — hold the BOOT button while tapping RESET to flash it");
#endif
}

void FlashingModeComponent::setup() {
#ifdef STILLFRAME_HAS_FORCE_DOWNLOAD_BOOT
  // Getting *out* of download mode is the part that needs care: the bit is
  // sticky. A power cycle or a RESET button (both pull EN/CHIP_PU low and reset
  // the whole RTC domain) clear it, and esptool writes a zero here after
  // flashing for exactly this reason — but a software reset does not, and
  // neither does a deep-sleep wake, since deep sleep is what the RTC domain is
  // *for*. Reaching this line means the app booted, i.e. the bit either was
  // never set or has already done its job, so clear it unconditionally and keep
  // a one-time request for the downloader from becoming a permanent one.
  REG_WRITE(RTC_CNTL_OPTION1_REG, 0);
#endif
}

void FlashingModeComponent::dump_config() {
#ifdef STILLFRAME_HAS_FORCE_DOWNLOAD_BOOT
  ESP_LOGCONFIG(TAG, "Flashing mode: software-forced download boot available");
#else
  ESP_LOGCONFIG(TAG, "Flashing mode: not supported by this chip (use the BOOT button)");
#endif
}

}  // namespace flashing_mode
}  // namespace esphome
