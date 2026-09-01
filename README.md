# Stillframe

A battery-friendly e-ink picture frame. An ESP32 wakes up, asks a small Go
server for a freshly processed photo, streams it straight to the panel, and
goes back to deep sleep.

Two halves:

- **`server/`** — a Go HTTP server that picks a random image from a directory
  and converts it for a specific e-ink panel: face-aware crop → Lanczos3 resize →
  gamma correction → CIEDE2000 nearest-color mapping → Floyd–Steinberg
  dithering → 4-bit packing. The device describes its own capabilities
  (dimensions, color space, flips) in the request, so one server drives every
  panel type.
- **`esphome/`** — ESPHome firmware for the frames, built as
  ["Made for ESPHome"-style](https://esphome.io/guides/made_for_esphome) public
  firmware: everyone flashes the identical build, **no secrets are compiled
  in**, and all personal configuration lives on the device.

## Supported hardware

| Variant | Panel | Board |
| --- | --- | --- |
| `color7-tinypico` | 7-color Waveshare EPD7IN3F, 800×480 | TinyPico (ESP32) |
| `color7-tinys3` | 7-color Waveshare EPD7IN3F, 800×480 | TinyS3 (ESP32-S3) |
| `grey16-tinypico` | 16-gray IT8951, 1872×1404 | TinyPico (ESP32) |
| `grey16-tinys3` | 16-gray IT8951, 1872×1404 | TinyS3 (ESP32-S3) |
| `spectra13-inkplate` | 13.3" Spectra 6 (EL133UF1), 1600×1200 | Soldered Inkplate 13 Spectra (ESP32-S3) |

The display drivers are custom ESPHome components (`esphome/components/`)
sharing a common `eink_frame` base; adding a panel means implementing a handful
of panel-specific hooks.

## Setting up a frame

1. **Flash from the browser** — open the
   [install page](https://googlebot42.github.io/stillframe/) in Chrome or
   Edge, plug the board in over USB, and click Install for your variant.
2. **Wi-Fi in the same dialog** — the installer prompts for your network via
   Improv Serial. There is deliberately no fallback hotspot; to re-provision
   later, plug into USB again.
3. **Adopt in Home Assistant** — the frame is auto-discovered; HA generates and
   pushes the API encryption key itself. (An ESPHome dashboard on the network
   will also offer to adopt it, which switches the device to locally compiled
   builds.)
4. **Point it at your server** — set the *Server URL* entity in HA.

The device then settles into its cycle: wake on a timer (or the wake button),
fetch and display a new photo, sleep. Everything is adjustable at runtime
through Home Assistant:

| Entity | Purpose |
| --- | --- |
| Server URL / Server Auth Header | Where to fetch images (the auth header only matters behind an authenticating reverse proxy) |
| Sleep Duration | Minutes between refreshes |
| Prevent Deep Sleep | Hold the frame awake — the OTA window; firmware updates are checked and installable while on |
| Fetch Image Now | Force a refresh |
| Firmware | Update entity (factory builds); installs pull directly from the release channel |

A frame that has never been provisioned or adopted stays awake so it cannot
strand itself; once adopted it sleeps on schedule even if HA is down.

## Running the server

```bash
nix build .#server
./result/bin/server 8080 /path/to/images   # args optional: port, image dir
```

Or as a NixOS service via the flake's `nixosModules.default` (see
`server/service.nix` for options: port, optional image directory, user/group,
environment file).

### Immich

The server can draw from an [Immich](https://immich.app) library instead of the
image directory. It is configured entirely through the environment, so the
positional arguments above are unchanged:

| Variable | Meaning |
| --- | --- |
| `IMMICH_URL` | Instance root, e.g. `https://photos.example.com`. Unset (the default) disables Immich entirely |
| `IMMICH_API_KEY` | API key from Immich's Account Settings; required when `IMMICH_URL` is set |
| `IMMICH_ALBUM` | Optional album, by name (case-insensitive) or UUID. Unset draws from the whole timeline |

On NixOS, point the service at an environment file. Keep the API key out of the
Nix store — it is world-readable — by having sops-nix or agenix render the file:

```nix
services.stillframe-server = {
  enable = true;
  # imgDir = "/var/lib/stillframe/images";   # optional; still the fallback, see below
  environmentFile = "/run/secrets/stillframe-server.env";
};
```

```
IMMICH_URL=https://photos.example.com
IMMICH_API_KEY=...
IMMICH_ALBUM=Living Room
```

The image directory stays the fallback: if Immich is down, slow, misconfigured
or answers something unusable, the request is logged and served from disk
instead, because a frame that misses a refresh just keeps showing yesterday's
photo and nobody notices.

`imgDir` is optional, and left unset the service falls back to an empty
systemd state directory (`/var/lib/stillframe-server/images`) — so an
Immich-only deployment needs no local photos at all. Dropping a handful in
there anyway is worth it: it is the difference between an Immich outage
refreshing the frame with something and refreshing it with nothing.

Only the server's own transcoded previews are downloaded
(`/api/assets/{id}/thumbnail?size=preview`), so HEIC and RAW originals need no
decoding here.

**Not yet validated against a live instance** — the integration is written from
the documented API and tested against mocks. Expect to check the server log the
first time you point it at a real Immich.

## Development

```bash
nix develop        # Go, Python, ESPHome
nix flake check    # server build + tests, C++ display-driver tests, NixOS VM test
```

Firmware work (from `esphome/`, using the in-tree drivers):

```bash
esphome -s components_source components config color7-tinypico.yaml
esphome -s components_source components compile color7-tinypico.yaml
bash tests/run.sh            # host-native C++ tests for the drivers
```

Server work (from `server/`): `go test ./...`.

See `CLAUDE.md` for the full architecture notes and command reference.

## Releases

The canonical repo lives on Gitea; a public mirror at
[GoogleBot42/stillframe](https://github.com/GoogleBot42/stillframe)
builds and hosts releases. Firmware is built **only** for version tags:

```bash
git tag -a v1.3.0 -m "v1.3.0" && git push origin v1.3.0
```

GitHub Actions builds all five variants, publishes a GitHub Release, and
redeploys the install page + update manifests that the frames poll. Pushes to
`main` run checks but never ship firmware. The full pipeline, the version
contract, and the one-time mirror setup are documented in
[`site/README.md`](site/README.md).
