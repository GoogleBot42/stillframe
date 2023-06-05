#include <SPI.h>
#include <WiFi.h>
#include <HTTPClient.h>
#include "epd7in3f.h"

void fetchAndDrawImage();

const char* ssid = "";
const char* password =  "";
const char* serverName = "http://192.168.3.133:8080/getImage";

Epd epd;

const char* einkDisplayProperties = R"json(
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

void setup()
{
  // put your setup code here, to run once:
  Serial.begin(9600);
  if (epd.Init() != 0)
  {
    Serial.print("e-Paper init failed");
    return;
  }

  delay(3000);

  WiFi.begin(ssid, password);
  Serial.println("Connecting");
  while(WiFi.status() != WL_CONNECTED) {
      delay(1000);
      Serial.print(".");
  }
  Serial.println("");
  Serial.print("Connected! IP Address: ");
  Serial.println(WiFi.localIP());

  fetchAndDrawImage();
}

void drawImage(uint8_t* image) {
  // Serial.print("Wake up display\n");
  // epd.Reset();
  // Serial.print("Clear display\n");
  // epd.Clear(EPD_7IN3F_WHITE);
  Serial.print("Draw image\n");
  epd.EPD_7IN3F_Display(image);
  Serial.print("Put display to sleep\n");
  epd.Sleep();
}

void fetchAndDrawImage() {
  HTTPClient http;

  http.begin(serverName);
  http.setTimeout(20000); // wait up to 20 seconds for response
  http.setAuthorization("username", "password");
  http.addHeader("Content-Type", "application/json");
  int httpCode = http.POST(einkDisplayProperties);

  if (httpCode > 0) {
      int length = http.getSize();

      Serial.print("Length of payload: ");
      Serial.println(length);

      uint8_t* payload = new uint8_t[length];
      http.getStream().readBytes(payload, length);

      drawImage(payload);

      delete[] payload;
  } else {
    Serial.println("Error on HTTP request");
  }

  http.end();
}

void loop()
{
}
