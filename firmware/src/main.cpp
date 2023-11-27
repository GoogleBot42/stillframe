#include <SPI.h>
#include <WiFi.h>
#include <HTTPClient.h>
#include <WebServer.h>
#include <AutoConnect.h>

#include "eink.h"

void fetchAndDrawImage(const char *url);
void hibernateAndRestart();

WebServer Server;
AutoConnect Portal(Server);
AutoConnectConfig Config;

#define START_UP_DELAY 3 // Time to wait before connecting to WiFi and beginning

#define HIBERNATE_TIME_SEC 5 // hibernate time in seconds
// #define HIBERNATE_TIME_SEC 120 // hibernate time in seconds

// Pin for waking the CPU and preventing default loop (get image, draw image, and hibernate)
// Must be an RTC GPIO Pin
#define SLEEP_WAKE_PIN 25
#define SLEEP_WAKE_PIN_RTC GPIO_NUM_25

const char *clearImage = "http://192.168.3.192:8080/clearImage";
const char *fetchImage = "http://192.168.3.192:8080/fetchImage";
const char *calibrationImage = "http://192.168.3.192:8080/calibrationImage";

bool sleepWakeButtonPressed()
{
  return !digitalRead(SLEEP_WAKE_PIN);
}

void rootPage()
{
  Server.sendHeader("Location", String("http://") + WiFi.localIP().toString() + String("/_ac"));
  Server.send(302, "text/plain", "");
  Server.client().flush();
  Server.client().stop();
}

void setup()
{
  // configure as a pullup because it made wiring a tad bit simplier
  pinMode(SLEEP_WAKE_PIN, INPUT_PULLUP);

  Serial.begin(9600);

  delay(START_UP_DELAY * 1000);

  bool supressDrawAndSleep = sleepWakeButtonPressed();

  Config.ota = AC_OTA_BUILTIN;
  Portal.config(Config);

  Server.on("/", rootPage);

  Serial.println("Connecting to wireless...");
  if (Portal.begin())
  {
    Serial.println("WiFi connected: " + WiFi.localIP().toString());

    if (!supressDrawAndSleep)
    {
      Serial.println("Fetching and drawing image.");
      fetchAndDrawImage(fetchImage);
      hibernateAndRestart();
    }
    else
    {
      Serial.println("Default fetch, draw, sleep loop is supressed due to user pressing button.");
    }
  }
  else
  {
    // TODO: should we still hibernate after a while in this case?
    Serial.println("Cannot fetch image because network isn't connected / configured.");
  }
}

void fetchAndDrawImage(const char *url)
{
  HTTPClient http;

  http.begin(url);
  http.setTimeout(40000); // wait up to 40 seconds for response
  http.setAuthorization("username", "password");
  http.addHeader("Content-Type", "application/json");
  int httpCode = http.POST(einkDisplayProperties);

  if (httpCode > 0)
  {
    int length = http.getSize();

    Serial.print("Length of payload: ");
    Serial.println(length);

    uint8_t *payload = new uint8_t[length];
    http.getStream().readBytes(payload, length);

    drawImage(payload);

    delete[] payload;
  }
  else
  {
    Serial.println("Error on HTTP request");
  }

  http.end();
}

void hibernateAndRestart()
{
  // Go to sleep and wake up later
  Serial.print("Beginning hibernation. Waking up in ");
  Serial.print(HIBERNATE_TIME_SEC);
  Serial.println(" seconds");

  // Configure wake up source as timer
  esp_sleep_enable_timer_wakeup(HIBERNATE_TIME_SEC * 1000000);

  // Allow being woken up by pin
  esp_sleep_enable_ext0_wakeup(SLEEP_WAKE_PIN_RTC, 0);

  // Enter hibernation mode
  // esp_sleep_pd_config(ESP_PD_DOMAIN_RTC_PERIPH, ESP_PD_OPTION_OFF);
  esp_sleep_pd_config(ESP_PD_DOMAIN_RTC_SLOW_MEM, ESP_PD_OPTION_OFF);
  esp_sleep_pd_config(ESP_PD_DOMAIN_RTC_FAST_MEM, ESP_PD_OPTION_OFF);
  esp_deep_sleep_start();
}

void loop()
{
  Portal.handleClient();
}
