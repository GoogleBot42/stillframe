import json
import sys

import smartcrop
from PIL import Image

filename = sys.argv[1]
cropWidth = int(sys.argv[2])
cropHeight = int(sys.argv[3])

image = Image.open(filename)
if image.mode != 'RGB' and image.mode != 'RGBA':
    new_image = Image.new('RGB', image.size)
    new_image.paste(image)
    image = new_image

cropper = smartcrop.SmartCrop()
result = cropper.crop(image, width=100, height=int(cropHeight / cropWidth * 100))

print(json.dumps(result['top_crop'], indent=2))