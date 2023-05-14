#include <SPI.h>
#include <WiFi.h>
#include <HTTPClient.h>
#include "epd7in3f.h"

void fetchAndDrawImage();

const char* ssid = "";
const char* password =  "";
const char* serverName = "http://192.168.3.133:8080/getImage";

Epd epd;

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
  // http.setAuthorization("username", "password");

  int httpCode = http.GET();

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
