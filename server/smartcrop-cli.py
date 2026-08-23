import json
import sys

import smartcrop
from PIL import Image

# smartcrop.py v0.3.2 still names Image.ANTIALIAS, which Pillow removed in 10.0.
# Without this shim every crop dies with an AttributeError deep inside
# SmartCrop.crop() and the server silently center-crops every single image.
if not hasattr(Image, 'ANTIALIAS'):
    Image.ANTIALIAS = Image.LANCZOS

if len(sys.argv) != 4:
    sys.exit('usage: smartcrop-cli <image> <width> <height>')

filename = sys.argv[1]
cropWidth = int(sys.argv[2])
cropHeight = int(sys.argv[3])

if cropWidth <= 0 or cropHeight <= 0:
    sys.exit('crop dimensions must be positive, got %dx%d' % (cropWidth, cropHeight))

image = Image.open(filename)
if image.mode != 'RGB' and image.mode != 'RGBA':
    new_image = Image.new('RGB', image.size)
    new_image.paste(image)
    image = new_image

# smartcrop uses width/height only as an aspect ratio, but their magnitude also
# decides how far it prescales the image before analysing it (min_scale =
# min(max_scale, max(1 / scale, min_scale))), so do not pass the raw pixel
# counts: asking for 1200x1600 directly analyses a 2370x1777 thumbnail where
# 100x133 analyses a 197x148 one, ~50x slower for the same crop. Normalising
# the short edge to 100 keeps the analysis at least as detailed as the old
# width=100 did, in either orientation.
#
# The values stay floating point. The old int(cropHeight / cropWidth * 100)
# turned 1200x1600 into 100x133, a ratio 0.25% off the panel's, which the
# exact-size resize downstream then stretched; for ratios past 100:1 it
# truncated to 0 and smartcrop died with a ZeroDivisionError. (Its own
# prescaling still floors to 0 somewhere past 111:1 - no panel is that
# lopsided, and the server falls back to a centered crop if one ever is.)
scale = 100.0 / min(cropWidth, cropHeight)

cropper = smartcrop.SmartCrop()
result = cropper.crop(image, width=cropWidth * scale, height=cropHeight * scale)

print(json.dumps(result['top_crop'], indent=2))
