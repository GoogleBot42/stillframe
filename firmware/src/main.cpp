#include <SPI.h>
#include <WiFi.h>
#include <HTTPClient.h>

#include "eink.h"
#include "network.h"

void fetchAndDrawImage();

const char *ssid = WIFI_SSID;
const char *password = WIFI_PASSWORD;

const char *serverName = "http://192.168.3.133:8080/fetchImage";

void setup()
{
  // put your setup code here, to run once:
  Serial.begin(9600);

  initDisplay();

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

void fetchAndDrawImage() {
  HTTPClient http;

  http.begin(serverName);
  http.setTimeout(40000); // wait up to 40 seconds for response
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
