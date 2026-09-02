# Public firmware distribution

`site/` is the static install page published to GitHub Pages, alongside the
compiled factory firmware. Together they let anyone flash a Stillframe from
a browser and then receive updates over the air, with no toolchain.

**Forge topology:** the source of truth is Gitea
(`git.neet.dev/zuckerberg/stillframe`, Tailscale-only). A **public
downstream mirror** at
[github.com/GoogleBot42/stillframe](https://github.com/GoogleBot42/stillframe)
exists solely for public distribution: it receives every branch and tag via
Gitea's push mirror, its Actions build the firmware, and its Pages site is what
devices poll. **Never create commits, tags, or edits on GitHub directly** — all
git data flows one way, Gitea → GitHub.

**CI runs on the mirror only.** Gitea itself runs no CI at all: there is no
`.gitea/workflows/`, and every job in `.github/workflows/` is guarded with
`github.server_url == 'https://github.com'`, so even with Gitea Actions
enabled every run there is a deliberate no-op. A push, branch, or PR on Gitea
is validated by nothing until the push mirror has synced it to GitHub and the
`Checks` workflow has run there. If the mirror stalls, a broken commit can sit
on Gitea `main` indefinitely with no red mark anywhere — check the mirror's
Actions tab, not Gitea's, before trusting a commit.

## How the pipeline works

1. `.github/workflows/firmware.yml` fires on a pushed `vX.Y.Z` **tag** — and
   nothing else. (`workflow_dispatch` is a manual escape hatch: run it against
   an existing tag to re-publish; dispatching from a branch is rejected.)
2. A `version` job resolves the version from the tag, validates the format and
   the 30-character ESPHome cap, and fails in seconds if the tag is malformed.
3. A matrix job builds each of the five secret-free configs in
   `esphome/factory/` with [`esphome/build-action`](https://github.com/esphome/build-action),
   using `complete-manifest: true`.
4. Every build is stamped with the tag's version — tag `v1.3.0` stamps `1.3.0`
   — passed in as the ESPHome substitution `firmware_version`. The workflow
   also overrides `components_source=../components`, so the custom display
   drivers are compiled from the tagged checkout rather than from
   `github://GoogleBot42/stillframe@main` (the default that makes an adopted
   config buildable anywhere).
5. The `release` job attaches every `*.factory.bin`, `*.ota.bin` and
   `<variant>.manifest.json` to the GitHub Release for the tag
   (`softprops/action-gh-release`, with generated release notes).
6. The `deploy` job lays the same build outputs out as `firmware/<variant>/`
   next to the contents of `site/`, and publishes the whole tree with
   `actions/upload-pages-artifact` + `actions/deploy-pages`.

Publishing a release **is** the OTA push: the Pages URLs below are stable and
always serve the newest stable release, so from the moment the deploy finishes
that is what every frame installs. That is why builds are gated on tags rather
than on pushes to `main` — a commit should not ship firmware to every device in
the house.

Frames do **not** poll on a schedule (`update_interval: never`): a frame is
asleep almost all the time, and a check it cannot act on is just traffic. Each
one checks this site when it is deliberately held awake — the "Prevent Deep
Sleep" switch, which is also what holding the wake button through a reset turns
on — and installing is then a click on the *Firmware* entity in Home Assistant.

Steps 5 and 6 are independent on purpose. Pages is the channel devices actually
poll, so a flaky release-asset upload must never block the OTA; the release
assets are a permanent per-version archive for humans.

**Pre-releases.** Give the tag any semver prerelease suffix — `v1.3.0-rc.1`.
That single fact drives the whole split: the GitHub Release is marked
*prerelease* and the Pages deploy is **skipped**, so the URL devices poll keeps
serving the last stable build. Install a pre-release by hand: download its
`*.factory.bin` from the release page and flash it over USB (esp-web-tools at
<https://web.esphome.io>, or `esptool`). Building from a working tree with
`esphome run` is *not* the same firmware — that produces the adoptable config,
without the `project:` version stamp, `dashboard_import`, or the `update:`
component, so the device would not be on the release channel at all.

A failed run is recovered by re-running it from the Actions tab — the release
step upserts, so it is idempotent. Never fix a release by pushing to GitHub.

The single `manifest.json` per variant serves two consumers:

- **esp-web-tools** in the browser reads `builds[].parts[]` and flashes
  `*.factory.bin` over USB.
- **the device itself** — the ESPHome `update:` component with
  `platform: http_request` — reads the same file for `version` plus
  `builds[].ota.path` / `.md5` to install `*.ota.bin` over Wi-Fi (on demand,
  not on a poll interval; see above).

### The version contract

`build-action` has no version input; the manifest's `version` comes from
`esphome.project.version` in the config. On the device, the update component
compares the manifest version against the compiled `ESPHOME_PROJECT_VERSION`
with a plain **string equality** check — no semver, no ordering. Equal means
"up to date", anything else means "update available".

So the factory configs must expose the version as a substitution the workflow
can override. All five share `esphome/factory/common.yaml`, so it lives there
once:

```yaml
substitutions:
  firmware_version: dev # local builds

esphome:
  project:
    name: googlebot42.stillframe
    version: ${firmware_version}
```

The workflow overrides it with `-s firmware_version <tag without the leading v>`,
and fails the build with an explicit message if the resulting manifest version
doesn't match — otherwise devices would silently keep a hard-coded version and
never update.

There is no `VERSION` file: nothing in the tree carries a version, so the tag is
the single source of truth for the version and it can never drift from what was
tagged. It also cannot be published twice by accident, because a tag is cut
once. (`dev` stays the default so local `esphome compile` of a factory config
still works.)

The tag is the single source of truth for the *code*, too, but only because of
the `components_source=../components` override in step 4. `esphome/common.yaml`
defaults `components_source` to `github://GoogleBot42/stillframe@main`, and
ESPHome clones that at build time — so without the override a release built
from tag `v1.3.0` would compile the drivers from whatever was on `main` at that
moment. Do not drop it.

## Published URLs

| What | URL |
| --- | --- |
| Install page | `https://googlebot42.github.io/stillframe/` |
| Manifest (per variant) | `https://googlebot42.github.io/stillframe/firmware/<variant>/manifest.json` |

with `<variant>` one of `color7-tinypico`, `color7-tinys3`, `grey16-tinypico`,
`grey16-tinys3`, `spectra13-inkplate`. For example:

```
https://googlebot42.github.io/stillframe/firmware/color7-tinypico/manifest.json
```

That exact URL is what the factory configs put in `update:` → `source:`.
Each directory also holds `<name>.factory.bin` (USB install) and
`<name>.ota.bin` (over-the-air update); the manifest references them by bare
filename, resolved relative to itself.

The GitHub Release for each tag carries the same files — every `*.factory.bin`,
`*.ota.bin`, and the manifests renamed `<variant>.manifest.json` (release assets
live in one flat namespace). They are a per-version archive, not the channel:
devices must use the Pages URLs, because release URLs redirect through long
signed links that can overflow the ESPHome HTTP client's buffer.

## Cutting a release

Everything starts on Gitea; GitHub only builds and hosts the artifacts. There is
nothing to bump first — the tag is the version.

```bash
git tag -a v1.3.0 -m "v1.3.0"   # on Gitea's `main`, from a pushed commit
git push origin v1.3.0
```

From there:

1. the Gitea push mirror replicates the tag to GitHub (immediately if the mirror
   syncs on push, otherwise on its interval — or hit *Synchronize now* in the
   Gitea mirror settings);
2. `.github/workflows/firmware.yml` fires on the mirrored tag, resolves the
   version from it, builds all five variants, publishes the GitHub Release with
   the binaries and manifests attached, and deploys Pages;
3. every frame on an older version offers the update the next time it is held
   awake (see "Frames do not poll on a schedule" above).

Watch it at <https://github.com/GoogleBot42/stillframe/actions>. A tag that
lands on GitHub with no run means the mirror pushed with an identity Actions
ignores — check the mirror credential first.

Use `v1.3.0-rc.1` (any prerelease suffix) to build and archive a candidate
without moving the channel devices poll. Never tag on GitHub.

## One-time setup for the repo owner

This repository's canonical home is the private Gitea instance; GitHub is only
a push mirror, and Actions run exclusively on the GitHub side.

1. **Create the public GitHub repo** `GoogleBot42/stillframe` (public, empty
   — no README, no license, no `.gitignore`).
2. **Configure push mirroring in Gitea**: repository → *Settings* →
   *Repository* → *Mirror Settings* → *Push Mirror*. Set the remote to
   `https://github.com/GoogleBot42/stillframe.git`, authenticate with a
   GitHub personal access token that has `repo` scope (username `GoogleBot42`,
   password = the token), and enable the sync interval. Push `main` at least
   once so the branch exists on GitHub. The mirror must push with a
   PAT/deploy-key identity — tags pushed by such an identity trigger Actions;
   tags authored by `github-actions` would not.
3. **Enable Actions on the mirror**: GitHub → *Settings* → *Actions* →
   *General* → *Allow all actions and reusable workflows*, with workflow
   permissions allowing `contents: write` (the built-in `GITHUB_TOKEN` is
   enough) so the release job can create releases and upload assets. Mirrored
   repos can land with Actions disabled, in which case tags produce no builds.
4. **Enable Pages**: GitHub → *Settings* → *Pages* → *Build and deployment* →
   *Source*: **GitHub Actions**. Do **not** pick "Deploy from a branch"; the
   workflow uploads the artifact directly.
5. **Allow tag deployments to Pages**: GitHub → *Settings* → *Environments* →
   *github-pages* → *Deployment branches and tags* → add a **tag** rule for
   `v*`. GitHub auto-creates this environment restricted to the `main` branch,
   but this pipeline deploys from tags — without the rule the deploy job is
   rejected by environment protection before it runs a single step (every build
   green, `Deploy to GitHub Pages` failed with no steps executed). After adding
   the rule, *Re-run failed jobs* on the run recovers it; nothing needs
   rebuilding.
6. **Cut the first release**: tag `v1.0.0` on Gitea and push it (see
   [Cutting a release](#cutting-a-release)). The first run creates the
   `github-pages` environment and the deployment; afterwards
   `https://googlebot42.github.io/stillframe/` is live.

No secrets need to be configured on GitHub — the workflow only uses the
built-in `GITHUB_TOKEN` and the Pages OIDC identity token, and the factory
configs contain no credentials.

Gitea Actions is **not** used by this repo. The workflow carries a
`github.server_url == 'https://github.com'` guard so that enabling Gitea Actions
later cannot make the Gitea side start publishing — Gitea stays the source of
truth, GitHub stays the only publisher.

## Why the install page patches esp-web-tools

Browser flashing of the Inkplate 13 Spectra fails intermittently, and the
failure is not in our firmware — it is in how esptool-js drives the board's
auto-reset circuit. `site/index.html` carries a small, self-disabling patch for
it. Read this before touching that script.

**The mechanism.** The CH340K bridge (VID `0x1a86`, PID `0x7522`) wires DTR and
RTS through the usual transistor pair to IO0 and EN. esptool-js's `ClassicReset`
toggles them one signal at a time — `setDTR(false)`, `setRTS(true)`, wait,
`setDTR(true)`, `setRTS(false)`, wait, `setDTR(false)` — and
`Transport.setRTS()` internally issues a *second* `setSignals()` re-asserting
DTR (a Windows workaround), so one reset costs **seven** `setSignals()`
round-trips. An ESP32-S3 latches its boot straps within about 3 ms of EN rising,
and the split `setDTR(true)` / `setRTS(false)` pair passes through the
intermediate state (DTR=1, RTS=1) — EN released while IO0 is still high — with a
renderer↔browser IPC hop inside that window. The chip boots the application
instead of the ROM downloader, and esp-web-tools reports "Failed to initialize".
Python `esptool` never sees this because `UnixTightReset` sets both modem lines
in a single `TIOCMSET` ioctl.

Upstream: [esptool-js#222](https://github.com/espressif/esptool-js/issues/222)
(open) and [PR #225](https://github.com/espressif/esptool-js/pull/225)
(unmerged). Chromium's `SerialSplitDtrAndRts` feature, on by default from 141 on
non-Windows ([crbug 420689824](https://issues.chromium.org/issues/420689824)),
makes it worse.

**The patch.** esp-web-tools assigns its `ESPLoader` to `window.esploader` as a
debug hook, synchronously after construction and before `main()` runs. The page
installs an accessor on `window.esploader` *before* the module loads; the setter
replaces `loader.resetConstructors.classicReset` with a strategy that performs
each edge as one combined
`port.setSignals({dataTerminalReady, requestToSend})` on the raw `SerialPort`,
bypassing `Transport.setRTS()`'s extra call. Seven round-trips become three.

`constructResetSequence()` builds exactly two strategy objects (resetDelay 50
and 550) and `connect()` cycles them over 7 attempts, so there is no way to add
entries to that array from outside; instead the strategies share a counter and
alternate — two combined-signal attempts, then one stock split-signal attempt,
repeating (5 combined and 2 stock across the 7 tries).

**It narrows the window; it does not close it.** With `SerialSplitDtrAndRts`
enabled Chrome still splits a combined `setSignals()` into two sequential
blocking CH340 control transfers, so an intermediate (DTR=1, RTS=1) state
remains — just far shorter, with no IPC round-trip inside it. Treat this as a
large reliability improvement, not a guarantee. The deterministic paths are the
firmware-side download escape hatch and the command line below.

**Failure safety.** Every step is guarded: if the debug hook is gone, if
`resetConstructors.classicReset` is missing, or if `transport.device` is not a
`SerialPort` with `setSignals`, the patch returns the stock object and
esp-web-tools behaves exactly as shipped. It cannot make an install that would
have worked fail.

**Exact-version pin.** The script tag names `esp-web-tools@10.4.0`, not `@10`.
The patch depends on bundle internals, so it must not follow a minor bump
silently. On upgrade, fetch
`https://unpkg.com/esp-web-tools@<version>/dist/web/install-button.js` and the
`install-dialog-*.js` chunk it imports, and re-verify:

1. `window.esploader = <ESPLoader>` is still assigned synchronously right after
   construction and before `.main()` is awaited;
2. `resetConstructors` is still a plain instance property
   `{classicReset, customReset, hardReset, usbJTAGSerialReset}`, and
   `constructResetSequence()` still reads `resetConstructors.classicReset`
   lazily, calls it as `(transport, resetDelay)`, and uses the result only via
   `await strategy.reset()`;
3. `Transport#device` is still the raw WebSerial `SerialPort`;
4. the failure UX hooks still hold — `ESPLoader#main()` rejects on a failed
   connect, and the install button still reports port-open errors via `alert()`.
   (Note that in 10.4.0 `<esp-web-install-button>` fires **no** events; the
   `state-changed` event in that bundle belongs to the Improv serial client.)

If esptool-js has merged the fix, delete the patch instead of porting it.

**The error UX.** esp-web-tools' failure message tells the user to hold a BOOT
button — which no board on this page has. The page therefore keeps its own
`<details id="trouble">` guidance box, opened and scrolled to automatically when
a connect fails: press RESET and retry, retry a few times, reload the page
between attempts (a failed connect can leave the port open, and the next attempt
dies with "port already open"), confirm the Inkplate's power switch is on and
its blue LED lit, use a data USB-C cable with no hub, close other apps holding
the port, install WCH's `CH34xVPCDriver` on macOS (Apple's bundled CH34x driver
does **not** work with this CH340K — see
[the Soldered thread](https://community.soldered.com/t/how-to-get-mac-to-see-inkplate-13-spectra/130)),
and, for the stubborn cases, relaunch Chrome with
`--disable-features=SerialSplitDtrAndRts`.

### Flashing from the command line

The reliable fallback, and what to reach for when the browser will not
cooperate. Download the variant's `*.factory.bin` from the
[release page](https://github.com/GoogleBot42/stillframe/releases) and, with
Python `esptool` installed:

```bash
esptool --chip esp32s3 --port /dev/ttyUSB0 --baud 921600 \
  --before default-reset --after hard-reset \
  write-flash -z --flash-mode keep --flash-freq keep --flash-size keep \
  0x0 <name>.factory.bin
```

Those are Soldered's own upload parameters for the Inkplate 13 Spectra (from
their `boards.txt` / `platform.txt`). `--chip esp32s3` covers the TinyS3 and
Inkplate builds; use `--chip esp32` for the TinyPico ones. Notes:

- A factory image is written at offset `0x0` — it contains the bootloader and
  partition table. The `*.ota.bin` files are **not** flashable this way.
- Errors partway through a write usually mean the bridge cannot keep up: drop
  `--baud` to `115200`.
- If it cannot connect at all, use `--before no-reset` and press the board's
  RESET button as esptool prints `Connecting....`.
- The command above is esptool v5 syntax. On v4 the binary is `esptool.py` and
  the names use underscores: `write_flash`, `--before default_reset`,
  `--after hard_reset`.
- Python esptool succeeds where browsers fail because on Linux and macOS its
  `UnixTightReset` sets DTR and RTS in one atomic `TIOCMSET` ioctl — the whole
  problem the patch above works around.

## Working on the page locally

`site/index.html` is a single self-contained file. Open it directly to check
layout; the install buttons will report that installing needs `https://`, and
the per-card version lines stay blank because there is no `firmware/`
directory locally. Both come right once deployed.
