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

Run these from the `esphome/` directory. There are no secrets — nothing needs to be filled in before building.

```bash
esphome config color7-tinypico.yaml             # Validate the adoptable config
esphome config factory/color7-tinypico.yaml     # Validate the factory (CI) build
esphome compile factory/color7-tinypico.yaml    # Build the public firmware
esphome run color7-tinypico.yaml --device <IP>  # OTA flash a device you own
esphome logs color7-tinypico.yaml               # View logs (when device is awake)
```

Replace `color7-tinypico` with `grey16-tinypico`, `color7-tinys3`, `grey16-tinys3`, or `spectra13-inkplate` for other variants.

Local driver development (build against `esphome/components/` instead of the published repo):

```bash
esphome run dev.yaml                                          # edit the include in dev.yaml to pick a variant
esphome -s components_source components run color7-tinys3.yaml  # same override, any variant, no file edit
```

The `esphome` in `nix develop` is pinned to an old nixpkgs (2023.4.4) and cannot parse these configs; use a current one, e.g. `nix shell nixpkgs#esphome --command esphome config color7-tinypico.yaml`.

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

"Made for ESPHome"-style public firmware: every user flashes the identical public build and **no secrets exist anywhere in the tree** (no `secrets.yaml`, no `!secret`). All personal configuration lives in NVS at runtime.

**Factory / adoptable split**

- `esphome/<variant>.yaml` — the *adoptable* config: board-specific hardware + `common.yaml` as a package. Pulls the display drivers from the published repo (`external_components: - source: ${components_source}`, default `github://GoogleBot42/picture-frame@main`; ESPHome auto-detects the `esphome/components` folder) so an adopted dashboard can build it anywhere.
- `esphome/factory/<variant>.yaml` — the *factory* build CI compiles and publishes. Includes the adoptable yaml as a package and adds only what a pre-built binary needs: `esphome.project` (name `googlebot42.picture-frame`, version `${firmware_version}`, defaulting to `dev` and overridden by CI), `name_add_mac_suffix`, `dashboard_import` (`github://GoogleBot42/picture-frame/esphome/<variant>.yaml@main`, `import_full_config: false`), `ota: platform: http_request`, and `update: platform: http_request` pointing at `https://googlebot42.github.io/picture-frame/firmware/<variant>/manifest.json` (combined esp-web-tools + OTA manifest). The update entity is only polled/refreshed while the device is deliberately staying awake, since a sleeping frame cannot install anything.
- `esphome/dev.yaml` — local-dev override that swaps `components_source` to the local `components/` directory. The source is a substitution, not an extra `external_components` entry, because ESPHome clones *every* entry in that list.

Five device variants (`color7-tinypico`, `color7-tinys3`, `grey16-tinypico`, `grey16-tinys3`, `spectra13-inkplate`):
- `color7-*` — 7-color EPD7IN3F on TinyPico (ESP32) / TinyS3 (ESP32-S3)
- `grey16-*` — 16-gray IT8951 on TinyPico / TinyS3
- `spectra13-inkplate.yaml` — 6-color 13.3" Spectra 6 (EL133UF1) on Soldered Inkplate 13 Spectra (ESP32-S3, esp-idf framework)

**Provisioning and runtime config (`common.yaml`)**

- `wifi:` has no credentials and deliberately no fallback AP / captive portal. Provisioning is `improv_serial:` over USB (ESP Web Tools / web.esphome.io); re-provisioning means plugging in again. `reboot_timeout: 0s` so an in-progress Improv session is never interrupted.
- Serial port per board: TinyPico has a USB-UART bridge (UART0 default). TinyS3 is native USB and `improv_serial` rejects `USB_CDC` on the S3, so those configs set `logger: hardware_uart: USB_SERIAL_JTAG`. The Inkplate 13 Spectra has a CH340C bridge, so it overrides the S3 default back to `UART0`.
- `api:` has `encryption:` with no key — both plaintext and noise transports are compiled and Home Assistant pushes a dynamic PSK on adoption, which is then stored in flash. `ota: platform: esphome` has no password so the dashboard can reflash over the factory firmware.
- Server config is runtime, via `restore_value` template text entities: **Server URL** (default `http://homeassistant.local:8080`) and **Server Auth Header** (default empty; the `Authorization` header is omitted when empty). Endpoint paths are derived from the base URL (`<base>/fetchImage`).

The `fetch_and_display` script streams the HTTP response to the display in 4KB chunks (no full-image buffering, except on the EL133UF1 whose dual-controller layout requires a PSRAM frame buffer). Display drivers in `components/epd7in3f/`, `components/it8951_spi/`, and `components/el133uf1/`; each driver implements the same interface consumed by the shared script: `get_image_request_body()` (JSON capability body, including the color space), `get_image_byte_count()`, `begin_image()`, `write_image_data()`, `finish_image(ok)`, `wake()`, `sleep()`. The CS pin is managed by ESPHome's SPI framework (`enable()`/`disable()`), not manually — except the EL133UF1's two CS pins (`cs_m_pin`/`cs_s_pin`).

**Deep sleep**

Wake sources: timer (`sleep_duration`) + button on `${wake_pin}` (a per-device substitution: GPIO25 on TinyPico, GPIO0/boot on TinyS3, GPIO18/WAKE on Inkplate; must be RTC-capable). Sleep is only ever entered through the `maybe_sleep` script, which keeps the device awake when: "Prevent Deep Sleep" is on, the wake button is held, there are no WiFi credentials in NVS (`wifi::global_wifi_component->has_sta()`), or Home Assistant has never connected (the `has_been_adopted` global is set from `api.on_client_connected` and persists across sleep cycles). That guarantees a fresh device stays reachable for provisioning and adoption; once adopted it resumes wake → fetch → sleep even if HA is unreachable. HA entities: "Prevent Deep Sleep" switch, "Fetch Image Now" button, "Server URL"/"Server Auth Header" text, and (factory builds) the "Firmware" update entity.

### Nix Integration

- `flake.nix` — Defines packages (server, smartcrop, firmware), dev shell, NixOS module, and checks
- `overlay.nix` — Package overlay for use by other flakes
- `server/default.nix` — Builds smartcrop Python package, Go server, and wraps the binary so smartcrop-cli is in PATH
