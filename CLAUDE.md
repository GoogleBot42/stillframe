# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Stillframe is an e-ink picture frame system with two components:
- **Server** (Go) — HTTP server that processes images and converts them for e-ink displays
- **ESPHome** (YAML + C++) — ESPHome-based firmware with HA integration, OTA, and deep sleep

## Working Environment (agents)

- This sandbox has **no serial/USB access and no route to the user's LAN** — it can never see the frame, `homeassistant.local`, or Home Assistant. Validate firmware with `esphome … compile` and the host tests; every physical step (esptool, button holds, HA UI actions) is run by the user, who pastes output back. The proven flash/debug loop, including the recipes to hand the user, is in `.claude/skills/device-debug/`.
- Cutting a release is `.claude/skills/cut-release/` — the agent tags via the Gitea API (it cannot push tags to `origin`), then polls the GitHub mirror's CI and verifies the live Pages manifest (~5 minutes typically; up to ~30 on cold caches).
- User preference: prefer delegating work to **Opus subagents** and verify their work — launch code-review and verification subagents after any substantive change before considering it done.

## Build Commands

```bash
nix build .#server                        # Build Go server
nix flake check                           # Run all checks (server build+tests, eink C++ tests, NixOS service test)
nix develop                               # Enter dev shell (Go, Python3, ESPHome)
```

### ESPHome Commands

Run these from the `esphome/` directory (inside `nix develop`, which ships a current ESPHome). There are no secrets — nothing needs to be filled in before building.

Always pass `-s components_source <path to esphome/components>`. `common.yaml` defaults `components_source` to `github://GoogleBot42/stillframe@main`, which ESPHome clones at build time — so without the override every command below depends on the public GitHub mirror existing and being current, and fails outright until it does. The override also guarantees you are validating the drivers in your working tree.

```bash
# from esphome/
esphome -s components_source components config color7-tinypico.yaml            # Validate the adoptable config
esphome -s components_source components run color7-tinypico.yaml --device <IP> # OTA flash a device you own
esphome -s components_source components logs color7-tinypico.yaml              # View logs (when device is awake)

# from esphome/factory/ — the path is relative to the config being built
esphome -s components_source ../components config color7-tinypico.yaml         # Validate the factory (CI) build
esphome -s components_source ../components compile color7-tinypico.yaml        # Build the public firmware
```

Dropping `-s components_source …` (plain `esphome config color7-tinypico.yaml`) is what an *adopted* config does in a user's ESPHome dashboard; it needs the published mirror. CI builds the factory configs with `components_source=../components` for the same reason as above.

Replace `color7-tinypico` with `grey16-tinypico`, `color7-tinys3`, `grey16-tinys3`, or `spectra13-inkplate` for other variants. `esphome/dev.yaml` bakes the same override into a file if you prefer editing an include over typing the flag.

Host unit tests for the panel-independent driver logic (JSON request bodies, byte counts, transfer state machine, pixel packing) — run from the repo root, needs only a C++ compiler:

```bash
bash esphome/tests/run.sh
```

## Architecture

### Image Processing Pipeline

1. ESP32 firmware POSTs JSON to server describing display capabilities (dimensions, color space, flip settings)
2. Server selects a random image from its configured directory, or from an Immich library when one is configured (see `source.go`/`immich.go`)
3. Server pipeline: face-aware crop (in-process, pigo) → Lanczos3 resize → gamma correction → CIEDE2000 nearest-color mapping → Floyd-Steinberg dithering → nibble packing (2 pixels/byte)
4. Server returns raw binary image data
5. Firmware writes to e-ink display via SPI, then enters deep sleep

### Server (`server/`)

Go (module directive tracks the nixpkgs toolchain) with chi router. Three POST endpoints: `/fetchImage`, `/calibrationImage`, `/clearImage`. All three are unauthenticated by design — the firmware's "Server Auth Header" is sent as `Authorization` and never read, and `TestRoutesAreUnauthenticated` pins that; enforcing auth is a reverse proxy's job.

Key files:
- `main.go` — HTTP server, routing, CLI args and the package-level `imageSource` the handlers draw from. The command line is `parseArgs` (testable, and tested): an optional `-bind` flag *before* the positional `<port> [imgDir]` form that `service.nix` interpolates, which must keep working unchanged. The listener is a constructed `http.Server` with named Read/ReadHeader/Write/Idle timeout constants — `writeTimeout` (180 s) is the one with a derivation attached to it, because it bounds the whole handler including the `limitConcurrency` queue, the Immich download (`immichTimeout`, 30 s) and a worst-case 4 Mpx conversion. Handlers answer through `writeImage`, which sets `Content-Type: application/octet-stream`: unset, Go sniffs a bright grey16 frame as `text/plain; charset=utf-8` and any proxy in front is then free to rewrite it. One `log.Printf` per request — never a line per palette entry
- `source.go` — The `ImageSource` interface (`NextImage(ctx)`), `localDirSource` (the directory walk, shuffle and no-repeat memory), and `fallbackSource`, which serves the local directory whenever the primary source errors
- `immich.go` — Optional [Immich](https://immich.app) source, configured *only* through `IMMICH_URL` / `IMMICH_API_KEY` / `IMMICH_ALBUM` (the positional args are interpolated by `service.nix` and must not change). `POST /api/search/random` picks the asset, `GET /api/assets/{id}/thumbnail?size=preview` downloads the server's transcode so HEIC/RAW never has to be decoded here, and an album name is resolved through `GET /api/albums` and cached for the process (failures are not cached). Never the only source: it is always wrapped in a `fallbackSource`, because a frame that misses a refresh looks exactly like a healthy one. Written against the documented API and verified only against httptest mocks — the API-version assumptions are documented on the `immichSource` type
- `image.go` — Image decoding (PNG/JPEG/GIF/WebP), RGBA conversion, gamma correction. `decodeUpright` is the shared decode + EXIF-orientation path, used by both `ReadImage` (files) and the Immich download
- `einkimage.go` — Color space mapping, CIEDE2000 dithering, nibble packing
- `bestcrop.go` — Crop selection: pure-Go face detection ([pigo](https://github.com/esimov/pigo), cascade embedded from `server/cascade/facefinder` with `//go:embed`) positions the crop over the faces; with no faces found it is a plain center crop, which is also the fallback whenever detection cannot run. No subprocess, no temp files, no Python
- `service.nix` — NixOS systemd service module `services.stillframe-server` (port, `bindAddress` — default `0.0.0.0`, narrow it to loopback or a Tailscale address — optional imgDir, which defaults to a systemd `StateDirectory` at `/var/lib/stillframe-server/images`, user/group, environmentFile). Every value interpolated into `ExecStart` goes through `utils.escapeSystemdExecArgs` (from the NixOS module `utils` argument, not `lib`) — systemd’s quoting, not the shell’s: it splits the line on whitespace (an imgDir with a space used to arrive as two arguments) *and* expands `%` specifiers and `$VAR` inside quotes as well as out, so `escapeShellArg` would not have been enough. The unit is sandboxed (`ProtectSystem=strict`, `ProtectHome=read-only` — an operator's imgDir may live under `/home` — `PrivateTmp`, `PrivateDevices`, empty `CapabilityBoundingSet`, `RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX AF_NETLINK` (netlink because glibc's `getaddrinfo` opens one — leaving it out would break Immich while the network-less VM test stayed green), `MemoryMax=1G` as a `mkDefault`, since on a 1 GB host it never binds and an operator may want it lower, and the rest). `SystemCallFilter` deliberately stops at `@system-service ~@privileged`: adding `~@resources` would SIGSYS the Go runtime, which raises its own `RLIMIT_NOFILE` at start-up
- `service-test.nix` — NixOS VM test, two nodes: the service starts, serves, declares `application/octet-stream`, keeps a space-containing imgDir in one piece, binds only what `bindAddress` names, and carries the sandboxing properties

### ESPHome Firmware (`esphome/`)

"Made for ESPHome"-style public firmware: every user flashes the identical public build and **no secrets exist anywhere in the tree** (no `secrets.yaml`, no `!secret`). All personal configuration lives in NVS at runtime.

**Factory / adoptable split**

- `esphome/<variant>.yaml` — the *adoptable* config: board-specific hardware + `common.yaml` as a package. Pulls the display drivers from the published repo (`external_components: - source: ${components_source}`, default `github://GoogleBot42/stillframe@main`; ESPHome auto-detects the `esphome/components` folder) so an adopted dashboard can build it anywhere.
- `esphome/factory/<variant>.yaml` — the *factory* build CI compiles and publishes. Each of the five is now only a `variant` substitution plus `packages: factory: !include common.yaml`; everything else lives once in `esphome/factory/common.yaml`, which pulls in the adoptable yaml with `packages: device: !include ../${variant}.yaml` (legal because ESPHome's packages pass runs *before* the substitution pass and resolves a package's include path itself, using the including file's root `substitutions:`) and parameterizes the two per-variant URLs on `${variant}`. So a fix to the factory layer is made once, not five times. What a factory build adds on top of the adoptable yaml is only what a pre-built binary needs: `esphome.project` (name `googlebot42.stillframe`, version `${firmware_version}`, defaulting to `dev` and overridden by CI with the release tag), `name_add_mac_suffix`, `dashboard_import` (`github://GoogleBot42/stillframe/esphome/<variant>.yaml@main`, `import_full_config: false`), `ota: platform: http_request`, and `update: platform: http_request` pointing at `https://googlebot42.github.io/stillframe/firmware/<variant>/manifest.json` (combined esp-web-tools + OTA manifest). CI also overrides `components_source=../components` so a release compiles the drivers from the tagged checkout, not from `@main`.

  The update entity sets `update_interval: never`, because `HttpRequestUpdate::setup()` schedules an initial check ~10 s after boot whenever the interval is *not* `never` — which would mean hitting GitHub Pages on every single wake, for an update a frame that is about to sleep could never install. The only automatic check is the `component.update: firmware_update` in the factory `on_boot` (priority -200), gated behind an untimed `wait_until` on the "Prevent Deep Sleep" switch: it fires when the frame is deliberately held awake (including when the switch is flipped on from HA after boot) and otherwise never completes, dying with the chip at deep sleep. Home Assistant's own "check for update" command still works — it calls the entity directly, not the poller.
- `esphome/dev.yaml` — local-dev override that swaps `components_source` to the local `components/` directory. The source is a substitution, not an extra `external_components` entry, because ESPHome clones *every* entry in that list.

**Releases (`.github/workflows/firmware.yml`, `site/README.md`)**

Gitea (`git.neet.dev/zuckerberg/stillframe`) is the source of truth; `github.com/GoogleBot42/stillframe` is a read-only push mirror that builds and hosts — never commit, tag, or edit there. The workflow fires **only** on a mirrored `vX.Y.Z` tag (plus `workflow_dispatch` against an existing tag): it stamps `firmware_version` with the tag minus its leading `v`, builds all five factory configs, attaches the binaries and manifests to the GitHub Release, and deploys the Pages site devices poll. There is no `VERSION` file — the tag *is* the version. A human with push rights cuts a release on Gitea with `git tag -a v1.3.0 -m v1.3.0 && git push origin v1.3.0`; an agent cannot push tags to `origin` and instead tags through the Gitea API — full procedure in `.claude/skills/cut-release/`. A prerelease suffix (`v1.3.0-rc.1`) builds and archives without moving the Pages channel.

Five device variants (`color7-tinypico`, `color7-tinys3`, `grey16-tinypico`, `grey16-tinys3`, `spectra13-inkplate`):
- `color7-*` — 7-color EPD7IN3F on TinyPico (ESP32) / TinyS3 (ESP32-S3)
- `grey16-*` — 16-gray IT8951 on TinyPico / TinyS3
- `spectra13-inkplate.yaml` — 6-color 13.3" Spectra 6 (EL133UF1) on Soldered Inkplate 13 Spectra (ESP32-S3, esp-idf framework)

**Provisioning and runtime config (`common.yaml`)**

- `wifi:` has no credentials and deliberately no fallback AP / captive portal. Provisioning is `improv_serial:` over USB (ESP Web Tools / web.esphome.io); re-provisioning means plugging in again. `reboot_timeout: 0s` so an in-progress Improv session is never interrupted.
- Serial port per board: TinyPico has a USB-UART bridge (UART0 default). TinyS3 is native USB and `improv_serial` rejects `USB_CDC` on the S3, so those configs set `logger: hardware_uart: USB_SERIAL_JTAG`. The Inkplate 13 Spectra has a CH340K bridge, so it overrides the S3 default back to `UART0`.
- `api:` has `encryption:` with no key — both plaintext and noise transports are compiled and Home Assistant pushes a dynamic PSK on adoption, which is then stored in flash. `ota: platform: esphome` has no password so the dashboard can reflash over the factory firmware.
- Reflashing escape hatch (`components/flashing_mode/`): the "Enter Flashing Mode" button, or an 8 s hold of the wake button (`flashing_mode_hold` script), sets `RTC_CNTL_FORCE_DOWNLOAD_BOOT` in `RTC_CNTL_OPTION1_REG` and reboots, so the chip comes up in the ROM serial downloader with no BOOT button, strapping pin or DTR/RTS timing involved — essential on the Inkplate 13 Spectra, which has neither a BOOT button nor a dependable auto-download circuit. The bit is sticky — a power cycle or the EN/RESET button clears it, a software reset or deep-sleep wake does **not** — so the component also zeroes it in `setup()`, which is what keeps a one-shot flash request from arming every future wake. The classic ESP32 (TinyPico) has no such bit (it arrived with the S2's on-chip USB), so there the action just logs "hold BOOT and tap RESET".
- Server config is runtime, via `restore_value` template text entities: **Server URL** (default `http://homeassistant.local:8080`) and **Server Auth Header** (default empty; the `Authorization` header is omitted when empty). Endpoint paths are derived from the base URL (`<base>/fetchImage`).

The `fetch_and_display` script streams the HTTP response to the display in 4KB chunks (no full-image buffering, except on the EL133UF1 whose dual-controller layout requires a PSRAM frame buffer). Display drivers in `components/epd7in3f/`, `components/it8951_spi/`, and `components/el133uf1/`; each driver implements the same interface consumed by the shared script: `get_image_request_body()` (JSON capability body, including the color space), `get_image_byte_count()`, `begin_image()`, `write_image_data()`, `finish_image(ok)`, `wake()`, `sleep()`. The CS pin is managed by ESPHome's SPI framework (`enable()`/`disable()`), not manually — except the EL133UF1's two CS pins (`cs_m_pin`/`cs_s_pin`).

That interface is implemented once, in `components/eink_frame/` — the shared base every driver derives from (`AUTO_LOAD = ["spi", "eink_frame"]`). It owns the request-body JSON and the color space tables, the 4bpp byte-count math, the begin/write/finish transfer accounting (chunk clamping, short-transfer detection, failure logging), the shared config schema (`eink_frame_schema()` / `register_eink_frame()`), and `wait_for_pin()`. Drivers supply only panel behaviour through the `on_begin_image_()` / `on_image_data_()` / `on_image_end_()` / `on_finish_image_(complete)` hooks plus `wake()`/`sleep()`. `eink_frame.h`/`.cpp` (and the drivers' pure helpers `it8951_words.h`, `el133uf1_image.h`) deliberately include no ESPHome runtime headers — logging goes through `eink_log.h` — so they compile and are unit tested on the host.

**Deep sleep**

Wake sources: timer + button on `${wake_pin}` (a per-device substitution: GPIO25 on TinyPico, GPIO0/boot on TinyS3, GPIO18/WAKE on Inkplate; must be RTC-capable). The wake period is **runtime**-configurable through the "Sleep Duration" number entity (minutes, `restore_value`, initial value `${default_sleep_minutes}`) — factory binaries are compiled once for everyone, so it must not be baked in; `maybe_sleep` passes it to `deep_sleep.enter` as a templated `sleep_duration` lambda.

There is no `run_duration`: sleep is only ever entered through the explicit `deep_sleep.enter` in the `maybe_sleep` script, so anything that stops `maybe_sleep` from reaching it keeps the frame awake indefinitely. It stays awake when: "Prevent Deep Sleep" is on (checked here because `deep_sleep.enter` bypasses `deep_sleep.prevent`), there are no WiFi credentials in NVS (`wifi::global_wifi_component->has_sta()`), the provisioning hand-off is armed (see below), or Home Assistant has never connected (the `has_been_adopted` global is set from `api.on_client_connected` and persists across sleep cycles). That guarantees a fresh device stays reachable for provisioning and adoption; once adopted it resumes wake → fetch → sleep even if HA is unreachable.

Provisioning hand-off: Improv connects without rebooting, and `fetch_and_display` only runs from `on_boot` and the "Fetch Image Now" button, so a frame provisioned after that boot's fetch window would sit connected, adoptable and blank. `maybe_sleep` therefore starts — before, and independently of, the sleep decision, so the "Prevent Deep Sleep is on" path gets it too — by arming the `await_provisioning` script whenever the frame is waiting on provisioning: either `has_sta()` is false (unprovisioned, or an Improv attempt just failed and `clear_sta()`d), or credentials appeared *during* this boot (`had_wifi_creds_at_boot` false, snapshotted in `on_boot` before `improv_serial`'s loop can `set_sta()`) with no picture yet (`image_shown_this_boot`, set only by a fetch that completed and was handed to the panel) and no connection yet. That second path is what a plain `has_sta()` test got wrong: `set_sta()` makes it true for the whole 30 s connection attempt. The credentials-at-boot clause keeps a *provisioned* frame whose AP is merely down from parking awake forever instead of sleeping and retrying next wake.

Arming only happens when `await_provisioning` is not already running, and sets `provisioning_handoff_parked`, which `await_provisioning` clears as soon as its `wait_until` completes. That flag — not `script.is_running`, which is also true for the inline `await_provisioning → fetch_and_display → maybe_sleep` chain — is the "do not deep-sleep out from under a parked hand-off" condition. Both arming paths guarantee the `wait_until` is false at entry, so the script always parks and the flag is never a lie.

`maybe_sleep` deliberately does **not** check the wake button: `wakeup_pin_mode: KEEP_AWAKE` already makes `deep_sleep.enter` defer while the pin is held and latch the sleep for when it is released. Holding the wake button through a reset instead turns the "Prevent Deep Sleep" switch **on** (`on_boot`, priority -100), so the OTA window is a single piece of state, visible and revocable from Home Assistant; the switch's `on_turn_off` re-runs `maybe_sleep` so turning it off actually puts the frame back to sleep. Every boot runs `fetch_and_display` regardless — "staying awake" never means "stop refreshing the picture".

`api: reboot_timeout: 0s` (default is 15 min) — otherwise any frame not currently connected to HA would reboot every 15 minutes, costing a full panel refresh and capping every OTA window.

HA entities: "Prevent Deep Sleep" switch, "Sleep Duration" number, "Fetch Image Now" and "Enter Flashing Mode" buttons, "Server URL"/"Server Auth Header" text, and (factory builds) the "Firmware" update entity.

### Nix Integration

- `flake.nix` — Defines the `server` package, dev shell, NixOS module, and checks. Inputs track `nixos-unstable`; `nix develop` therefore ships a current Go, Python 3, and ESPHome
- `overlay.nix` — The package overlay, and the single source of truth for it: `flake.nix` sets `overlays.default = import ./overlay.nix`, so `nix flake check` exercises the same file downstream flakes import. It exports the same package set under three attributes: `pkgs.stillframe` (the current name, used by this repo and `server/service.nix`) plus `pkgs.picture-frame` and `pkgs.dynamic-frame` (older names, kept as aliases for downstream consumers)
- `server/default.nix` — One `buildGoModule` derivation and nothing else. The server has no runtime dependency outside its own binary — the cropper is in-process and its cascade is embedded — so there is no wrapper script and nothing on PATH to arrange. (It used to wrap the binary to put `smartcrop-cli` on PATH; that whole path, Python package included, is gone. Should a wrapper ever be needed again, `--prefix PATH :` is the only safe form: `--set PATH "...:$PATH"` expands `$PATH` at build time and drags the whole stdenv toolchain, ~870 MiB, into the runtime closure of a network-facing daemon)

Checks (`nix flake check`, and `.github/workflows/checks.yml` on the GitHub mirror). **Gitea runs no CI**: there is no `.gitea/workflows/`, and every job in `.github/workflows/` is guarded to run only when `github.server_url` is `https://github.com`, so a push or PR on Gitea is validated by nothing until the mirror syncs and the `Checks` workflow runs there. That is why `nix flake check` is run locally before a PR is merged — a broken commit on Gitea `main` gets no red mark on Gitea, ever.

- `checks.build` — builds the server; buildGoModule's checkPhase runs the Go tests
- `checks.einkTests` — compiles and runs `esphome/tests/run.sh` (host unit tests for the panel-independent display logic) in the sandbox, with `CXX` and `EINK_TESTS_NIX_RETRY=1` set so its `nix shell` fallback never fires
- `checks.service` — NixOS VM test, two nodes (imgDir set + default bind, imgDir unset + `bindAddress = "127.0.0.1"`): starts the systemd service, asserts `/calibrationImage` answers 200 with a non-empty `application/octet-stream` body, and that a `/fetchImage` against an imgDir with no usable images cannot kill the daemon (the main PID must be unchanged); also pins the rendered unit — the escaped `ExecStart` (checked against the process's actual `/proc/<pid>/cmdline`, since the test imgDir has a space in its name, plus a journal assertion that the daemon really walked that directory rather than being locked out of it by the sandbox — a 500 alone cannot tell those apart), a set imgDir getting no `StateDirectory` while an unset one gets the state directory created even under `ProtectSystem=strict`, the listening socket matching `bindAddress`, and the sandboxing properties
