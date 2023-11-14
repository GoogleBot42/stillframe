#ifdef GREY16

#define MISO 23
#define MOSI 19
#define SCK 18
#define CS 14
#define RESET 15
#define HRDY 27

#include "it8951.h"

#define DISPLAY_WIDTH 1872
#define DISPLAY_HEIGHT 1404

const char *einkDisplayProperties = R"json(
{
  "width": 1872,
  "height": 1404,
  "flip_horizonal": true,
  "color_space": [
    {
        "color_code": 0,
        "rgb_color": [0.0, 0.0, 0.0]
    },
    {
        "color_code": 1,
        "rgb_color": [0.06666666666666667, 0.06666666666666667, 0.06666666666666667]
    },
    {
        "color_code": 2,
        "rgb_color": [0.13333333333333333, 0.13333333333333333, 0.13333333333333333]
    },
    {
        "color_code": 3,
        "rgb_color": [0.2, 0.2, 0.2]
    },
    {
        "color_code": 4,
        "rgb_color": [0.26666666666666666, 0.26666666666666666, 0.26666666666666666]
    },
    {
        "color_code": 5,
        "rgb_color": [0.3333333333333333, 0.3333333333333333, 0.3333333333333333]
    },
    {
        "color_code": 6,
        "rgb_color": [0.4, 0.4, 0.4]
    },
    {
        "color_code": 7,
        "rgb_color": [0.4666666666666667, 0.4666666666666667, 0.4666666666666667]
    },
    {
        "color_code": 8,
        "rgb_color": [0.5333333333333333, 0.5333333333333333, 0.5333333333333333]
    },
    {
        "color_code": 9,
        "rgb_color": [0.6, 0.6, 0.6]
    },
    {
        "color_code": 10,
        "rgb_color": [0.6666666666666666, 0.6666666666666666, 0.6666666666666666]
    },
    {
        "color_code": 11,
        "rgb_color": [0.7333333333333333, 0.7333333333333333, 0.7333333333333333]
    },
    {
        "color_code": 12,
        "rgb_color": [0.8, 0.8, 0.8]
    },
    {
        "color_code": 13,
        "rgb_color": [0.8666666666666667, 0.8666666666666667, 0.8666666666666667]
    },
    {
        "color_code": 14,
        "rgb_color": [0.9333333333333333, 0.9333333333333333, 0.9333333333333333]
    },
    {
        "color_code": 15,
        "rgb_color": [1.0, 1.0, 1.0]
    }
  ]
}
)json";

void initDisplay()
{
    IT8951_Init();
}

void drawImage(uint8_t *image)
{
    gpFrameBuf = image;
    Serial.println("Sending image");
    IT8951_BMP_Example(0, 0, DISPLAY_WIDTH, DISPLAY_HEIGHT);
    Serial.println("Displaying image");
    IT8951DisplayArea(0, 0, DISPLAY_WIDTH, DISPLAY_HEIGHT, 2);
    Serial.println("Waiting for display ...");
    LCDWaitForReady();
    Serial.println("done");
}

#endif