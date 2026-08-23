#include "el133uf1.h"
#include "esphome/components/eink_frame/eink_wait.h"
#include "esphome/core/hal.h"
#include "esphome/core/helpers.h"
#include "esphome/core/log.h"

#include <cinttypes>
#include <cstring>

namespace esphome {
namespace el133uf1 {

static const char *const TAG = "el133uf1";

// Busy-wait budgets, per phase. Both of Soldered's own drivers wait for BUSY
// *forever* (Inkplate-Arduino-library src/boards/Inkplate13SPECTRA/
// Inkplate13SPECTRADriver.cpp waitForBusy(); Soldered-Inkplate-ESPHome
// components/inkplate_spi/inkplate13.cpp is_busy_() polled from a state
// machine that simply never advances), so any bound here is stricter than the
// reference. These are set generously enough that they only ever trip on a
// genuine fault — a panel that is not powered, not out of reset, or not
// connected — rather than on a slow-but-working panel.
static const uint32_t BUSY_TIMEOUT_PON_MS = 30000;      // power-up: DC-DC + VCOM ramp
static const uint32_t BUSY_TIMEOUT_DATA_MS = 15000;     // one controller latching its half
static const uint32_t BUSY_TIMEOUT_REFRESH_MS = 60000;  // full Spectra 6 refresh is ~19-25 s
static const uint32_t BUSY_TIMEOUT_POF_MS = 15000;      // power-down

// Not a wait we depend on: Soldered go straight from the reset delay to the
// init registers. Waveshare's reference driver for the same panel (e-Paper
// E-paper_Separate_Program/13.3inch_e-Paper_E/.../EPD_13in3e.c, EPD_13IN3E_Init)
// does wait for BUSY here, so sampling it is free information about whether the
// controller ever came out of reset at all. Never fatal.
static const uint32_t BUSY_PROBE_AFTER_RESET_MS = 2000;

// Power-on timing, from Soldered's setPanelState(true)/resetPanel():
// pins low, 50 ms; PWR_EN high, 100 ms; RST low, 100 ms; RST high, 100 ms
// (+ a further 100 ms in the Arduino driver, which is the longer of the two).
static const uint32_t DISCHARGE_MS = 50;
static const uint32_t PWR_EN_SETTLE_MS = 100;
static const uint32_t RESET_LOW_MS = 100;
static const uint32_t RESET_SETTLE_MS = 200;

// Waveshare's TurnOnDisplay() pauses between the data phase and DRF; harmless
// and cheap insurance against issuing the refresh into a still-settling panel.
static const uint32_t PRE_REFRESH_MS = 50;

static const char *const HINT_POWER =
    "check panel power (PWR_EN), the FFC cable seating and the BS0/BS1 strapping — BUSY is pulled up on the ESP "
    "side, so a LOW reading means a powered controller is holding it, not a dead one";
static const char *const HINT_DATA = "the controller accepted the DTM command but never released BUSY";

// Register/command bytes (Soldered Inkplate13SPECTRA driver / Waveshare EPD_13in3e)
static const uint8_t REG_PSR = 0x00;
static const uint8_t REG_PWR = 0x01;
static const uint8_t REG_POF = 0x02;
static const uint8_t REG_PON = 0x04;
static const uint8_t REG_BTST_N = 0x05;
static const uint8_t REG_BTST_P = 0x06;
static const uint8_t REG_DTM = 0x10;
static const uint8_t REG_DRF = 0x12;
static const uint8_t REG_PLL = 0x30;
static const uint8_t REG_CDI = 0x50;
static const uint8_t REG_TCON = 0x60;
static const uint8_t REG_TRES = 0x61;
static const uint8_t REG_AN_TM = 0x74;
static const uint8_t REG_AGID = 0x86;
static const uint8_t REG_BUCK_BOOST_VDDN = 0xB0;
static const uint8_t REG_TFT_VCOM_POWER = 0xB1;
static const uint8_t REG_EN_BUF = 0xB6;
static const uint8_t REG_BOOST_VDDP_EN = 0xB7;
static const uint8_t REG_CCSET = 0xE0;
static const uint8_t REG_PWS = 0xE3;
static const uint8_t REG_CMD66 = 0xF0;

// Init values from the Soldered Inkplate 13 Spectra Arduino driver
static const uint8_t AN_TM_V[] = {0xC0, 0x1C, 0x1C, 0xCC, 0xCC, 0xCC, 0x15, 0x15, 0x55};
static const uint8_t CMD66_V[] = {0x49, 0x55, 0x13, 0x5D, 0x05, 0x10};
static const uint8_t PSR_V[] = {0xDF, 0x6B};
static const uint8_t PLL_V[] = {0x08};
static const uint8_t CDI_V[] = {0xF7};
static const uint8_t TCON_V[] = {0x03, 0x03};
static const uint8_t AGID_V[] = {0x10};
static const uint8_t PWS_V[] = {0x22};
static const uint8_t CCSET_V[] = {0x01};
static const uint8_t TRES_V[] = {0x04, 0xB0, 0x03, 0x20};
static const uint8_t PWR_V[] = {0x0F, 0x00, 0x28, 0x2C, 0x28, 0x38};
static const uint8_t EN_BUF_V[] = {0x07};
static const uint8_t BTST_P_V[] = {0xD8, 0x18};
static const uint8_t BOOST_VDDP_EN_V[] = {0x01};
static const uint8_t BTST_N_V[] = {0xD8, 0x18};
static const uint8_t BUCK_BOOST_VDDN_V[] = {0x01};
static const uint8_t TFT_VCOM_POWER_V[] = {0x02};
static const uint8_t POF_V[] = {0x00};
static const uint8_t DRF_V[] = {0x00};

void EL133UF1::setup() {
  ESP_LOGI(TAG, "Setting up EL133UF1 (Spectra 6 13.3\", %dx%d native)", NATIVE_WIDTH, NATIVE_HEIGHT);

  cs_m_pin_->setup();
  cs_s_pin_->setup();
  dc_pin_->setup();
  reset_pin_->setup();
  busy_pin_->setup();
  pwr_en_pin_->setup();
  if (bs0_pin_ != nullptr)
    bs0_pin_->setup();
  if (bs1_pin_ != nullptr)
    bs1_pin_->setup();

  // The panel stays unpowered until an image transfer starts, so park every
  // panel-facing pin LOW rather than at its idle-active level: with the rail
  // off, a pin driven high back-feeds the panel through the controller's ESD
  // diodes and can leave it partially powered and never properly reset. This
  // is the state Soldered's setPanelPinsToLow()/set_all_pins_low_() establishes
  // ("Function helps empty capacitors, without this sometimes the panel refuses
  // to refresh"), and the state power_on_and_init_() starts every cycle from.
  discharge_pins_();

  this->spi_setup();
}

const char *EL133UF1::frame_tag_() const { return TAG; }

void EL133UF1::on_begin_image_() {
  if (buffer_ == nullptr) {
    RAMAllocator<uint8_t> allocator(RAMAllocator<uint8_t>::ALLOC_EXTERNAL);
    buffer_ = allocator.allocate(get_image_byte_count());
  }
  if (buffer_ == nullptr) {
    ESP_LOGE(TAG, "Failed to allocate %u byte image buffer in PSRAM", (unsigned) get_image_byte_count());
    this->mark_transfer_failed_();
    return;
  }

  // Bring the panel up while the image downloads.
  power_on_and_init_();
}

void EL133UF1::on_image_data_(size_t offset, const uint8_t *data, size_t len) {
  // The base class only calls this between begin_image() and finish_image(),
  // so the buffer normally exists; the check is a cheap belt-and-braces guard
  // against ever memcpying through the freed pointer.
  if (buffer_ == nullptr)
    return;
  memcpy(buffer_ + offset, data, len);
}

bool EL133UF1::on_finish_image_(bool complete) {
  bool refreshed = false;

  // `powered_` is false if power_on_and_init_() gave up: there is nothing on
  // the other end of the bus worth talking to, so go straight to the power-off
  // path (which also puts the pins back in the discharge state).
  if (complete && powered_) {
    // Left half to the master controller, right half to the slave. Each
    // controller has to finish latching its half before the other is addressed
    // — Soldered wait for BUSY between the two DTM frames, and again before
    // DRF (Inkplate13SPECTRADriver.cpp display(); inkplate13.cpp TRF_WAIT_MASTER
    // / TRF_WAIT_SLAVE).
    send_image_half_(CHIP_MASTER, 0);
    if (wait_busy_(BUSY_TIMEOUT_DATA_MS, "master controller latching left half (DTM)", HINT_DATA)) {
      send_image_half_(CHIP_SLAVE, NATIVE_WIDTH / 2);
      if (wait_busy_(BUSY_TIMEOUT_DATA_MS, "slave controller latching right half (DTM)", HINT_DATA)) {
        delay(PRE_REFRESH_MS);
        send_command_(REG_DRF, DRF_V, sizeof(DRF_V), CHIP_BOTH);
        refreshed = wait_busy_(BUSY_TIMEOUT_REFRESH_MS, "display refresh (DRF)",
                               "a full Spectra 6 refresh normally takes 19-25 s");
      }
    }
  }

  power_off_();
  free_buffer_();
  return refreshed;
}

void EL133UF1::sleep() { power_off_(); }

void EL133UF1::power_on_and_init_() {
  if (powered_)
    return;

  // Discharge every panel pin (including BUSY and the BS strapping pins, which
  // are otherwise held at their idle level and keep back-feeding the unpowered
  // controller), then bring the rail up and release reset.
  discharge_pins_();
  delay(DISCHARGE_MS);

  restore_pins_();
  pwr_en_pin_->digital_write(true);
  delay(PWR_EN_SETTLE_MS);
  reset_pin_->digital_write(false);
  delay(RESET_LOW_MS);
  reset_pin_->digital_write(true);
  delay(RESET_SETTLE_MS);

  // Diagnostic only — never fatal, see BUSY_PROBE_AFTER_RESET_MS.
  uint32_t probe_start = millis();
  while (!busy_pin_->digital_read() && millis() - probe_start < BUSY_PROBE_AFTER_RESET_MS) {
    eink_frame::feed_watchdog();
    delay(1);
  }
  if (busy_pin_->digital_read()) {
    ESP_LOGD(TAG, "BUSY high %" PRIu32 " ms after reset release", millis() - probe_start);
  } else {
    ESP_LOGW(TAG, "BUSY still LOW %" PRIu32 " ms after reset release — controller may not have started",
             BUSY_PROBE_AFTER_RESET_MS);
  }

  send_command_(REG_AN_TM, AN_TM_V, sizeof(AN_TM_V), CHIP_MASTER);
  send_command_(REG_CMD66, CMD66_V, sizeof(CMD66_V), CHIP_BOTH);
  send_command_(REG_PSR, PSR_V, sizeof(PSR_V), CHIP_BOTH);
  send_command_(REG_PLL, PLL_V, sizeof(PLL_V), CHIP_BOTH);
  send_command_(REG_CDI, CDI_V, sizeof(CDI_V), CHIP_BOTH);
  send_command_(REG_TCON, TCON_V, sizeof(TCON_V), CHIP_BOTH);
  send_command_(REG_AGID, AGID_V, sizeof(AGID_V), CHIP_BOTH);
  send_command_(REG_PWS, PWS_V, sizeof(PWS_V), CHIP_BOTH);
  send_command_(REG_CCSET, CCSET_V, sizeof(CCSET_V), CHIP_BOTH);
  send_command_(REG_TRES, TRES_V, sizeof(TRES_V), CHIP_BOTH);
  send_command_(REG_PWR, PWR_V, sizeof(PWR_V), CHIP_MASTER);
  send_command_(REG_EN_BUF, EN_BUF_V, sizeof(EN_BUF_V), CHIP_MASTER);
  send_command_(REG_BTST_P, BTST_P_V, sizeof(BTST_P_V), CHIP_MASTER);
  send_command_(REG_BOOST_VDDP_EN, BOOST_VDDP_EN_V, sizeof(BOOST_VDDP_EN_V), CHIP_MASTER);
  send_command_(REG_BTST_N, BTST_N_V, sizeof(BTST_N_V), CHIP_MASTER);
  send_command_(REG_BUCK_BOOST_VDDN, BUCK_BOOST_VDDN_V, sizeof(BUCK_BOOST_VDDN_V), CHIP_MASTER);
  send_command_(REG_TFT_VCOM_POWER, TFT_VCOM_POWER_V, sizeof(TFT_VCOM_POWER_V), CHIP_MASTER);

  send_command_(REG_PON, nullptr, 0, CHIP_BOTH);
  if (!wait_busy_(BUSY_TIMEOUT_PON_MS, "panel power-up (PON) during init", HINT_POWER)) {
    // Do not leave the rail energized behind a failed init: cut it back to the
    // discharge state so the next attempt starts from a real power-on reset.
    power_off_();
    this->mark_transfer_failed_();
    return;
  }

  powered_ = true;
  ESP_LOGI(TAG, "Panel powered on and initialized");
}

void EL133UF1::power_off_() {
  if (powered_) {
    send_command_(REG_POF, POF_V, sizeof(POF_V), CHIP_BOTH);
    wait_busy_(BUSY_TIMEOUT_POF_MS, "panel power-down (POF)");
    powered_ = false;
    ESP_LOGI(TAG, "Panel powered off");
  }
  // Also reached with powered_ == false (failed init, sleep() before the first
  // update): the point is to end with the rail off and nothing driving the
  // panel's inputs high.
  discharge_pins_();
}

// Every panel-facing pin driven LOW with the rail off, so nothing back-feeds
// the controller. Mirrors Soldered's setPanelPinsToLow()/set_all_pins_low_(),
// BUSY included — it is briefly an output here.
void EL133UF1::discharge_pins_() {
  GPIOPin *const pins[] = {cs_m_pin_, cs_s_pin_, dc_pin_, reset_pin_, busy_pin_, pwr_en_pin_, bs0_pin_, bs1_pin_};
  for (GPIOPin *pin : pins) {
    if (pin == nullptr)
      continue;
    pin->pin_mode(gpio::FLAG_OUTPUT);
    pin->digital_write(false);
  }
}

// Undo discharge_pins_(): the idle levels the panel expects while powered.
// Mirrors Soldered's setIO()/set_io_pins_(). BUSY goes back to whatever input
// mode the YAML configured (INPUT_PULLUP on the Inkplate 13 Spectra) via its
// own setup(), rather than a hardcoded mode.
void EL133UF1::restore_pins_() {
  GPIOPin *const outputs[] = {cs_m_pin_, cs_s_pin_, dc_pin_, reset_pin_, pwr_en_pin_, bs0_pin_, bs1_pin_};
  for (GPIOPin *pin : outputs) {
    if (pin != nullptr)
      pin->pin_mode(gpio::FLAG_OUTPUT);
  }
  busy_pin_->setup();

  cs_m_pin_->digital_write(true);
  cs_s_pin_->digital_write(true);
  dc_pin_->digital_write(true);
  reset_pin_->digital_write(false);
  pwr_en_pin_->digital_write(false);
  if (bs0_pin_ != nullptr)
    bs0_pin_->digital_write(false);
  if (bs1_pin_ != nullptr)
    bs1_pin_->digital_write(true);
}

// BUSYN is LOW while busy. Returns false on timeout.
//
// There are only a handful of these per refresh cycle, so the successful ones
// are logged at INFO too: a hardware log that says which phase took how long is
// what turns "it hung" into "it hung *here*" next time.
bool EL133UF1::wait_busy_(uint32_t timeout_ms, const char *phase, const char *hint) {
  uint32_t start = millis();
  if (!eink_frame::wait_for_pin(busy_pin_, true, timeout_ms, TAG, phase, hint))
    return false;
  ESP_LOGI(TAG, "BUSY released after %" PRIu32 " ms — %s", millis() - start, phase);
  return true;
}

// Chip select is active LOW and driven by hand: one SPI bus, two controllers,
// and a command can address either or both of them.
void EL133UF1::select_chips_(uint8_t chips, bool selected) {
  if (chips & CHIP_MASTER)
    cs_m_pin_->digital_write(!selected);
  if (chips & CHIP_SLAVE)
    cs_s_pin_->digital_write(!selected);
}

// Command byte and parameters are one CS frame; DC stays high (interface mode
// selected by the BS0/BS1 strapping).
void EL133UF1::send_command_(uint8_t cmd, const uint8_t *data, size_t len, uint8_t chips) {
  select_chips_(chips, true);
  this->enable();
  this->transfer_byte(cmd);
  if (len > 0 && data != nullptr)
    this->write_array(data, len);
  this->disable();
  select_chips_(chips, false);
}

// Stream one controller's half of the frame: DTM, then for each of the 1600
// native rows the 300 bytes covering native columns [px_start, px_start+600).
void EL133UF1::send_image_half_(uint8_t chips, int px_start) {
  // Word-aligned: IDF's SPI DMA path bounces a transfer whose address or length
  // is not aligned to the controller's requirement, and 300 bytes already is.
  alignas(4) uint8_t row_buf[HALF_ROW_BYTES];

  select_chips_(chips, true);
  this->enable();
  this->transfer_byte(REG_DTM);

  for (int py = 0; py < NATIVE_HEIGHT; py++) {
    // Always via row_buf: buffer_ is PSRAM and write_array() is DMA-backed, so
    // the bus never sees an external-RAM pointer (rotation 0 copies, 90/270
    // gather from the landscape buffer while splitting).
    build_half_row(buffer_, rotation_, py, px_start, row_buf);
    this->write_array(row_buf, HALF_ROW_BYTES);
    if ((py & 0x3F) == 0)
      eink_frame::feed_watchdog();
  }

  this->disable();
  select_chips_(chips, false);
}

void EL133UF1::free_buffer_() {
  if (buffer_ != nullptr) {
    RAMAllocator<uint8_t> allocator(RAMAllocator<uint8_t>::ALLOC_EXTERNAL);
    allocator.deallocate(buffer_, get_image_byte_count());
    buffer_ = nullptr;
  }
}

}  // namespace el133uf1
}  // namespace esphome
