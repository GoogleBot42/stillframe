---
name: device-debug
description: Flash and debug a physical stillframe device (esp. Inkplate 13 Spectra) — division of labor with the user, esptool recipes, WAKE-hold flashing mode, log capture, and the gotchas that repeatedly cost sessions. Use when firmware must reach or be observed on real hardware.
---

# Device debug

Reconstructed from real past debugging sessions (v1.0.0–v1.0.6) and confirmed
by the user. Items explicitly marked UNCONFIRMED were proposed in a session
but never actually exercised — treat them as a starting guess, not a proven
recipe.

## Division of labor (read this first — it governs everything below)

The agent sandbox (hostname `test-incus`) has no serial/USB access and no
route to the user's LAN: it cannot see the frame's IP, `homeassistant.local`,
Home Assistant, or the Inkplate's CH340K bridge. (A stray FTDI adapter may be
passed through sometimes — do not mistake it for the device itself.) The
user's physical machine, where the frame and its USB cable actually live, is
`fry`.

- **Agent does:** edit code, compile-validate configs, cut releases, poll CI
  and GitHub Pages, read pasted logs, diagnose, and prepare *exact* commands
  for the user to run.
- **User does, on `fry`, physically:** every `esptool`/`miniterm` command,
  plugging/unplugging the board, WAKE-button holds, RESET presses, the
  browser install page, all Home Assistant UI actions ("Prevent Deep Sleep",
  "Fetch Image Now", Firmware → Install, Server URL), and judging any physical
  fact the agent cannot see (did the panel actually refresh, LED state, which
  power source is connected).

Write every hardware-facing command *for* the user to run verbatim, and ask
them to paste the output back verbatim — don't ask them to summarize a log.

## The proven debug cycle (repeated across v1.0.0–v1.0.6)

1. Draft the fix (per the user's preference, often via a subagent) on a
   branch.
2. Compile-validate locally before shipping. From `esphome/factory/` (inside
   `nix develop`; full details and the other variant/target combinations are
   owned by CLAUDE.md's "ESPHome Commands" section):
   ```bash
   esphome -s components_source ../components compile spectra13-inkplate.yaml
   ```
   Compiles take minutes — run this in the background rather than blocking on
   it. It's also useful for verifying *generated* output, e.g. grepping the
   produced `sdkconfig` under `esphome/.esphome/build/...` to prove a config
   value actually landed before tagging a release.
3. Ship via PR + the `.claude/skills/cut-release/` skill (timing expectations
   are owned there — typically about 5 minutes of CI + Pages deploy).
4. The device picks up the new firmware one of two ways:
   - **HA's Firmware update entity** — requires "Prevent Deep Sleep" ON so the
     frame stays reachable long enough to check and install. Force an
     immediate check via HA Developer Tools → Actions →
     `homeassistant.update_entity` targeted at the Firmware entity, or reset
     the device while the switch is on (the on-boot check fires ~15 s after
     Wi-Fi connects).
   - **User reflashes over USB** — see the recipes below.
5. User pastes back the serial/console log; agent diagnoses; repeat from
   step 1.

## USB flashing recipes (user runs these on `fry`)

`esptool` is available in the dev shell (`nix develop` — confirmed present
there, esptool v5.3.1, pulled in as a transitive dependency rather than
listed by name in `flake.nix`) or standalone via `nix shell nixpkgs#esptool`.

**First flash of a factory-fresh Inkplate 13 Spectra cannot be done from the
browser** — no BOOT button, a CH340K bridge, and WebSerial reset timing that
doesn't cooperate (hardware-confirmed, not theoretical). The CLI is
mandatory:

```bash
curl -LO https://googlebot42.github.io/stillframe/firmware/spectra13-inkplate/stillframe-spectra13-esp32s3.factory.bin
esptool --chip esp32s3 --port /dev/ttyUSB0 --baud 921600 \
  --before default-reset --after hard-reset \
  write-flash -z --flash-mode keep --flash-freq keep --flash-size keep \
  0x0 stillframe-spectra13-esp32s3.factory.bin
```

(This matches Soldered's own upload parameters for the board, documented
alongside the browser-flashing fallback in `site/README.md`'s "Flashing from
the command line" section.)

- **Reflashing a board already parked in the ROM downloader by a WAKE-hold:**
  use `--before no-reset` instead of `--before default-reset` — auto-reset
  would kick the chip back *out* of download mode before esptool can talk to
  it.
- **Full factory reset** (also wipes Wi-Fi credentials and HA pairing): run
  `esptool --chip esp32s3 --port /dev/ttyUSB0 erase-flash` first, then the
  write-flash above.
- **Key gotcha:** `write-flash` *preserves* NVS — Wi-Fi credentials, the HA
  encryption key, and the has-been-adopted flag all survive a plain reflash.
  "Reflash" is not "factory reset"; if you need one, you need the
  `erase-flash` step above.
- A surgical NVS-only erase (`erase-region <addr> <size>` with
  `--before no-reset`) exists in principle, but the NVS partition's address
  and size must be read off the device's own boot-log partition table for
  that specific build. UNCONFIRMED / derive-per-build — do not hardcode an
  address from a past session, it may not match the current partition table.

## WAKE-hold flashing mode (user procedure)

Deterministic once firmware ≥ v1.0.1 is already on the board. Internals
(`RTC_CNTL_FORCE_DOWNLOAD_BOOT`, the sticky-bit semantics, why it's cleared in
`setup()`) are owned by CLAUDE.md's "Reflashing escape hatch" bullet and the
comments in `esphome/components/flashing_mode/` — this is just the
user-facing sequence:

1. Hold WAKE through a reset (or press-to-wake and keep holding) for about
   8 seconds. With a log console attached you'll see "keep holding for
   flashing mode" around the 3 s mark and "Rebooting into flashing mode" at
   about 8 s, after which the log stream dies — the chip has parked itself in
   the ROM downloader. **Without a console there is no feedback at all**:
   silence after the 8 s mark means success, not failure.
2. Do **not** press RESET afterward — RESET exits download mode.
3. From there, either use the browser Install page, or `esptool` with
   `--before no-reset` (see above).
4. This works even mid-boot-loop, since the 8 s hold fires before a typical
   ~10–13 s crash-and-reboot cycle.

## Log capture

- **Early/boot logs** (before Wi-Fi exists): either the ESP Web Tools install
  page's "Logs & Console" panel (three-dot menu), or plain pyserial on `fry`:
  ```bash
  python3 -m serial.tools.miniterm --raw /dev/ttyUSB0 115200
  ```
  This has proven rock-solid in sessions where Chrome WebSerial was flaky.
- `avahi-browse -rt _esphomelib._tcp` on `fry` — proven once, inspects the
  device's mDNS TXT record; useful for exonerating the device itself when
  debugging an HA pairing issue.
- UNCONFIRMED (proposed in-session, never actually exercised):
  `nix shell nixpkgs#python3Packages.aioesphomeapi --command aioesphomeapi-logs --address <device-ip>`
  for live logs without going through HA.
- `esphome ... logs` / `esphome run --device <IP>` OTA from the dashboard is
  documented in CLAUDE.md's ESPHome Commands section but has never actually
  been exercised in a real debugging session — UNCONFIRMED in practice. The
  proven update path remains the HA Firmware entity described above.

## Gotcha list

Each of these has cost real debugging time in past sessions:

- Panel refresh on the 13.3" Spectra takes roughly 20–25 seconds — "is it
  stuck" during that window is usually just this.
- The esp-idf task watchdog defaults to 5 s, while a legitimate image fetch
  can block far longer (unresolvable `.local` DNS; 15–25 s of server-side
  dithering). The repo raised `esp32: watchdog_timeout` to 60 s in
  `esphome/common.yaml` and serialized the update-check against the fetch to
  avoid a watchdog-restore race between the two. If a new boot-loop shows up,
  check the watchdog interaction first before assuming a new bug.
- A brownout looks identical to a firmware crash in a boot-loop signature.
  The tell is the `rst:` reason in the serial boot banner (`BROWNOUT` vs.
  `SW`+backtrace) and the power source in use (bare USB vs. a battery or 2 A
  supply) — only the user can check either of these.
- The default Server URL, `http://homeassistant.local:8080`, silently
  fails/stalls wherever `.local` mDNS resolution doesn't work — this has been
  a leading boot-loop cause before the server was even reached.
- "Prevent Deep Sleep" does not auto-clear. It's easy to leave a frame
  permanently awake (and draining its battery) after a debugging session —
  remind the user to turn it back off when done.
- Chrome WebSerial can be broken in a machine-specific way
  (`NetworkError: device has been lost`); ModemManager grabbing the serial
  port is a known contributor. `pyserial`/`miniterm` on the same machine has
  kept working in cases where WebSerial did not.

## Open item

As of the last mined session (v1.0.6 tagged and built), on-hardware
confirmation of a successful panel refresh was never actually captured in a
transcript. Start the first hardware session of any new debugging effort by
asking the user whether the frame is currently displaying photos at all —
don't assume the last known-good state still holds.
