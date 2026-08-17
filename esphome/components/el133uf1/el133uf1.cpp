#include "el133uf1.h"
#include "esphome/components/eink_frame/eink_wait.h"
#include "esphome/core/hal.h"
#include "esphome/core/helpers.h"
#include "esphome/core/log.h"

#include <cstring>

namespace esphome {
namespace el133uf1 {

static const char *const TAG = "el133uf1";

static const uint32_t BUSY_TIMEOUT_INIT_MS = 10000;
// A full Spectra 6 refresh takes ~19-25 seconds.
static const uint32_t BUSY_TIMEOUT_REFRESH_MS = 60000;

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

  // Panel stays unpowered until an image transfer starts.
  cs_m_pin_->digital_write(true);
  cs_s_pin_->digital_write(true);
  dc_pin_->digital_write(true);
  reset_pin_->digital_write(false);
  pwr_en_pin_->digital_write(false);
  if (bs0_pin_ != nullptr)
    bs0_pin_->digital_write(false);
  if (bs1_pin_ != nullptr)
    bs1_pin_->digital_write(true);

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

void EL133UF1::on_finish_image_(bool complete) {
  if (complete) {
    // Left half to the master controller, right half to the slave.
    send_image_half_(CHIP_MASTER, 0);
    send_image_half_(CHIP_SLAVE, NATIVE_WIDTH / 2);
    wait_busy_(BUSY_TIMEOUT_INIT_MS);

    delay(50);
    send_command_(REG_DRF, DRF_V, sizeof(DRF_V), CHIP_BOTH);
    wait_busy_(BUSY_TIMEOUT_REFRESH_MS);
  }

  power_off_();
  free_buffer_();
}

void EL133UF1::sleep() { power_off_(); }

void EL133UF1::power_on_and_init_() {
  if (powered_)
    return;

  // Discharge, then power the panel rail and release reset.
  cs_m_pin_->digital_write(false);
  cs_s_pin_->digital_write(false);
  dc_pin_->digital_write(false);
  reset_pin_->digital_write(false);
  pwr_en_pin_->digital_write(false);
  delay(50);

  cs_m_pin_->digital_write(true);
  cs_s_pin_->digital_write(true);
  dc_pin_->digital_write(true);
  pwr_en_pin_->digital_write(true);
  delay(100);
  reset_pin_->digital_write(false);
  delay(100);
  reset_pin_->digital_write(true);
  delay(100);

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
  if (!wait_busy_(BUSY_TIMEOUT_INIT_MS)) {
    this->mark_transfer_failed_();
    return;
  }

  powered_ = true;
  ESP_LOGI(TAG, "Panel powered on and initialized");
}

void EL133UF1::power_off_() {
  if (!powered_)
    return;
  send_command_(REG_POF, POF_V, sizeof(POF_V), CHIP_BOTH);
  wait_busy_(BUSY_TIMEOUT_INIT_MS);
  reset_pin_->digital_write(false);
  pwr_en_pin_->digital_write(false);
  powered_ = false;
  ESP_LOGI(TAG, "Panel powered off");
}

// BUSYN is LOW while busy. Returns false on timeout.
bool EL133UF1::wait_busy_(uint32_t timeout_ms) {
  return eink_frame::wait_for_pin(busy_pin_, true, timeout_ms, TAG, "busy pin");
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
  uint8_t row_buf[HALF_ROW_BYTES];

  select_chips_(chips, true);
  this->enable();
  this->transfer_byte(REG_DTM);

  for (int py = 0; py < NATIVE_HEIGHT; py++) {
    if (rotation_ == 0) {
      // Buffer rows are already native portrait rows.
      this->write_array(native_half_row(buffer_, py, px_start), HALF_ROW_BYTES);
    } else {
      // Buffer is landscape (1600x1200); rotate while splitting.
      build_rotated_half_row(buffer_, rotation_, py, px_start, row_buf);
      this->write_array(row_buf, HALF_ROW_BYTES);
    }
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
