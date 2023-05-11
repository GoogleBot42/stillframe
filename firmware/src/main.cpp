#include <SPI.h>
#include "imagedata.h"
#include "epd7in3f.h"

void setup()
{
  // put your setup code here, to run once:
  Serial.begin(115200);
  Epd epd;
  if (epd.Init() != 0)
  {
    Serial.print("e-Paper init failed");
    return;
  }

  Serial.print("e-Paper Clear\r\n ");
  epd.Clear(EPD_7IN3F_WHITE);

  Serial.print("Show pic\r\n ");
  epd.EPD_7IN3F_Display_part(gImage_7in3f, 250, 150, 300, 180);
  delay(2000);

  Serial.print("draw 7 color block\r\n ");
  epd.EPD_7IN3F_Show7Block();
  delay(2000);

  epd.Sleep();
}

void loop()
{
  // put your main code here, to run repeatedly:
}
