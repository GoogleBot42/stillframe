#include <SPI.h>
#include <WiFi.h>
#include <HTTPClient.h>

#include "eink.h"
#include "network.h"

void fetchAndDrawImage(const char *url);
void hibernate_and_restart();

const char *ssid = WIFI_SSID;
const char *password = WIFI_PASSWORD;

#define HIBERNATE_TIME_SEC 5 // hibernate time in seconds
// #define HIBERNATE_TIME_SEC 120 // hibernate time in seconds

const char *clearImage = "http://192.168.3.192:8080/clearImage";
const char *fetchImage = "http://192.168.3.192:8080/fetchImage";
const char *calibrationImage = "http://192.168.3.192:8080/calibrationImage";

void setup()
{
  Serial.begin(9600);

  delay(3000);

  initDisplay();

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

  hibernate_and_restart();
}

void hibernate_and_restart() {
  // Go to sleep and wake up later

  // Configure wake up source as timer
  esp_sleep_enable_timer_wakeup(HIBERNATE_TIME_SEC * 1000000);

  // Enter hibernation mode
  esp_sleep_pd_config(ESP_PD_DOMAIN_RTC_PERIPH, ESP_PD_OPTION_OFF);
  esp_sleep_pd_config(ESP_PD_DOMAIN_RTC_SLOW_MEM, ESP_PD_OPTION_OFF);
  esp_sleep_pd_config(ESP_PD_DOMAIN_RTC_FAST_MEM, ESP_PD_OPTION_OFF);
  esp_deep_sleep_start();
}

void loop()
{
}
