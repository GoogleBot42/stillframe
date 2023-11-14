#ifdef COLOR7

#include "epd7in3f.h"

#define DISPLAY_WIDTH 800
#define DISPLAY_HEIGHT 480

#define BUSY_PIN 26
#define RST_PIN 27
#define DC_PIN 15
#define CS_PIN 14

const char *einkDisplayProperties = R"json(
{
  "width": 800,
  "height": 480,
  "color_space": [
    {
      "rgb_color": [0, 0, 0],
      "color_code": 0
    },
    {
      "rgb_color": [1, 1, 1],
      "color_code": 1
    },
    {
      "rgb_color": [0.059, 0.329, 0.119],
      "color_code": 2
    },
    {
      "rgb_color": [0.061, 0.147, 0.336],
      "color_code": 3
    },
    {
      "rgb_color": [0.574, 0.066, 0.010],
      "color_code": 4
    },
    {
      "rgb_color": [0.982, 0.756, 0.004],
      "color_code": 5
    },
    {
      "rgb_color": [0.795, 0.255, 0.018],
      "color_code": 6
    }
  ]
}
)json";

Epd epd(DISPLAY_WIDTH, DISPLAY_HEIGHT, BUSY_PIN, RST_PIN, DC_PIN, CS_PIN);

void initDisplay()
{
  if (epd.Init() != 0)
  {
    Serial.println("e-Paper init failed");
    return;
  }
}

void drawImage(uint8_t *image)
{
  Serial.println("Wake up display");
  epd.Reset();
  Serial.println("Draw image");
  epd.EPD_7IN3F_Display(image);
  Serial.println("Put display to sleep");
  epd.Sleep();
}
#endif