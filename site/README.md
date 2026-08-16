# Public firmware distribution

`site/` is the static install page published to GitHub Pages, alongside the
compiled factory firmware. Together they let anyone flash a Stillframe from
a browser and then receive updates over the air, with no toolchain.

**Forge topology:** the source of truth is Gitea
(`git.neet.dev/zuckerberg/picture-frame`, Tailscale-only). A **public
downstream mirror** at
[github.com/GoogleBot42/picture-frame](https://github.com/GoogleBot42/picture-frame)
exists solely for public distribution: it receives every branch and tag via
Gitea's push mirror, its Actions build the firmware, and its Pages site is what
devices poll. **Never create commits, tags, or edits on GitHub directly** — all
git data flows one way, Gitea → GitHub.

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
   `github://GoogleBot42/picture-frame@main` (the default that makes an adopted
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

So each `esphome/factory/<variant>.yaml` must expose the version as a
substitution the workflow can override:

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
defaults `components_source` to `github://GoogleBot42/picture-frame@main`, and
ESPHome clones that at build time — so without the override a release built
from tag `v1.3.0` would compile the drivers from whatever was on `main` at that
moment. Do not drop it.

## Published URLs

| What | URL |
| --- | --- |
| Install page | `https://googlebot42.github.io/picture-frame/` |
| Manifest (per variant) | `https://googlebot42.github.io/picture-frame/firmware/<variant>/manifest.json` |

with `<variant>` one of `color7-tinypico`, `color7-tinys3`, `grey16-tinypico`,
`grey16-tinys3`, `spectra13-inkplate`. For example:

```
https://googlebot42.github.io/picture-frame/firmware/color7-tinypico/manifest.json
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

Watch it at <https://github.com/GoogleBot42/picture-frame/actions>. A tag that
lands on GitHub with no run means the mirror pushed with an identity Actions
ignores — check the mirror credential first.

Use `v1.3.0-rc.1` (any prerelease suffix) to build and archive a candidate
without moving the channel devices poll. Never tag on GitHub.

## One-time setup for the repo owner

This repository's canonical home is the private Gitea instance; GitHub is only
a push mirror, and Actions run exclusively on the GitHub side.

1. **Create the public GitHub repo** `GoogleBot42/picture-frame` (public, empty
   — no README, no license, no `.gitignore`).
2. **Configure push mirroring in Gitea**: repository → *Settings* →
   *Repository* → *Mirror Settings* → *Push Mirror*. Set the remote to
   `https://github.com/GoogleBot42/picture-frame.git`, authenticate with a
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
5. **Cut the first release**: tag `v1.0.0` on Gitea and push it (see
   [Cutting a release](#cutting-a-release)). The first run creates the
   `github-pages` environment and the deployment; afterwards
   `https://googlebot42.github.io/picture-frame/` is live.

No secrets need to be configured on GitHub — the workflow only uses the
built-in `GITHUB_TOKEN` and the Pages OIDC identity token, and the factory
configs contain no credentials.

Gitea Actions is **not** used by this repo. The workflow carries a
`github.server_url == 'https://github.com'` guard so that enabling Gitea Actions
later cannot make the Gitea side start publishing — Gitea stays the source of
truth, GitHub stays the only publisher.

## Working on the page locally

`site/index.html` is a single self-contained file. Open it directly to check
layout; the install buttons will report that installing needs `https://`, and
the per-card version lines stay blank because there is no `firmware/`
directory locally. Both come right once deployed.
