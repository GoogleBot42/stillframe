# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

DynamicFrame is an e-ink picture frame system with two components:
- **Server** (Go) — HTTP server that processes images and converts them for e-ink displays
- **Firmware** (C++/Arduino) — Legacy ESP32 firmware (PlatformIO)
- **ESPHome** (YAML + C++) — ESPHome-based firmware with HA integration, OTA, and deep sleep

## Build Commands

```bash
nix build .#server                        # Build Go server
nix build --relaxed-sandbox .#firmware    # Build firmware (needs internet, can't sandbox)
nix flake check                           # Run all checks (build + NixOS service test)
nix develop                               # Enter dev shell (Go, Python3, PlatformIO, ESPHome)
```

The firmware cannot build in the Nix sandbox because PlatformIO requires internet access.

### ESPHome Commands

```bash
esphome compile esphome/color7-tinypico.yaml    # Compile COLOR7 TinyPico firmware
esphome run esphome/color7-tinypico.yaml        # Compile + flash via USB
esphome run esphome/color7-tinypico.yaml --device <IP>  # OTA flash
esphome logs esphome/color7-tinypico.yaml       # View logs (when device is awake)
```

Replace `color7-tinypico` with `grey16-tinypico`, `color7-tinys3`, `grey16-tinys3`, or `spectra13-inkplate` for other variants. Copy `esphome/secrets.yaml.example` to `esphome/secrets.yaml` and fill in your values before building.

## Architecture

### Image Processing Pipeline

1. ESP32 firmware POSTs JSON to server describing display capabilities (dimensions, color space, flip settings)
2. Server selects a random image from its configured directory
3. Server pipeline: smart crop (Python subprocess) → Lanczos3 resize → gamma correction → CIEDE2000 nearest-color mapping → Floyd-Steinberg dithering → nibble packing (2 pixels/byte)
4. Server returns raw binary image data
5. Firmware writes to e-ink display via SPI, then enters deep sleep

### Server (`server/`)

Go 1.19 with chi router. Three POST endpoints: `/fetchImage`, `/calibrationImage`, `/clearImage`.

Key files:
- `main.go` — HTTP server, routing, CLI args (port, image directory)
- `image.go` — Image decoding (PNG/JPEG/GIF), RGBA conversion, gamma correction
- `einkimage.go` — Color space mapping, CIEDE2000 dithering, nibble packing
- `bestcrop.go` — Smart crop via Python subprocess (`smartcrop-cli`)
- `service.nix` — NixOS systemd service module (configurable port, imgDir, user/group)
- `service-test.nix` — NixOS VM test that verifies the service starts

### Firmware (`firmware/`)

PlatformIO project targeting TinyPico (ESP32). Two build environments selected via preprocessor flags:
- `tinypico-color` (`COLOR7`) — 7-color Waveshare EPD7IN3F, 800×480
- `tinypico-16gray` (`GREY16`) — 16-grayscale IT8951, 1872×1404

Display drivers live in `firmware/lib/`. AutoConnect handles WiFi provisioning and OTA updates.

### ESPHome Firmware (`esphome/`)

Declarative ESPHome configs with custom external components for e-ink displays. Five device variants:
- `color7-tinypico.yaml` — 7-color EPD7IN3F on TinyPico
- `color7-tinys3.yaml` — 7-color EPD7IN3F on TinyS3
- `grey16-tinypico.yaml` — 16-gray IT8951 on TinyPico
- `grey16-tinys3.yaml` — 16-gray IT8951 on TinyS3
- `spectra13-inkplate.yaml` — 6-color 13.3" Spectra 6 (EL133UF1) on Soldered Inkplate 13 Spectra (ESP32-S3, esp-idf framework)

Shared config in `common.yaml`: WiFi, deep sleep, OTA, HA API, and the `fetch_and_display` script, which streams the HTTP response to the display in 4KB chunks (no full-image buffering, except on the EL133UF1 whose dual-controller layout requires a PSRAM frame buffer). Display drivers in `components/epd7in3f/`, `components/it8951_spi/`, and `components/el133uf1/`; each driver implements the same interface consumed by the shared script: `get_image_request_body()` (JSON capability body, including the color space), `get_image_byte_count()`, `begin_image()`, `write_image_data()`, `finish_image(ok)`, `wake()`, `sleep()`. The CS pin is managed by ESPHome's SPI framework (`enable()`/`disable()`), not manually — except the EL133UF1's two CS pins (`cs_m_pin`/`cs_s_pin`).

Deep sleep wake sources: timer (`sleep_duration`) + button on `${wake_pin}` (a per-device substitution: GPIO25 on TinyPico, GPIO0/boot on TinyS3, GPIO18/WAKE on Inkplate; must be RTC-capable). "Prevent Deep Sleep" HA switch for OTA windows, plus a "Fetch Image Now" HA button.

Validate with `esphome config <file>.yaml` (needs a filled-in `secrets.yaml`; the api_encryption_key must be valid base64).

### Nix Integration

- `flake.nix` — Defines packages (server, smartcrop, firmware), dev shell, NixOS module, and checks
- `overlay.nix` — Package overlay for use by other flakes
- `server/default.nix` — Builds smartcrop Python package, Go server, and wraps the binary so smartcrop-cli is in PATH
