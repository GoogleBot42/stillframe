# Public firmware distribution

`site/` is the static install page published to GitHub Pages, alongside the
compiled factory firmware. Together they let anyone flash a DynamicFrame from
a browser and then receive updates over the air, with no toolchain.

## How the pipeline works

1. `.github/workflows/firmware.yml` runs on every push to `main` that touches
   `esphome/**` or `site/**` (and on manual dispatch).
2. A matrix job builds each of the five secret-free configs in
   `esphome/factory/` with [`esphome/build-action`](https://github.com/esphome/build-action),
   using `complete-manifest: true`.
3. Every build is stamped with the same version, `1.<run number>`, passed in as
   the ESPHome substitution `firmware_version`.
4. The deploy job lays the build outputs out as `firmware/<variant>/` next to
   the contents of `site/`, and publishes the whole tree with
   `actions/upload-pages-artifact` + `actions/deploy-pages`.

The single `manifest.json` per variant serves two consumers:

- **esp-web-tools** in the browser reads `builds[].parts[]` and flashes
  `*.factory.bin` over USB.
- **the device itself** — the ESPHome `update:` component with
  `platform: http_request` — polls the same file and reads `version` plus
  `builds[].ota.path` / `.md5` to install `*.ota.bin` over Wi-Fi.

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
    name: googlebot42.picture-frame
    version: ${firmware_version}
```

The workflow overrides it with `-s firmware_version 1.<run number>`, and fails
the build with an explicit message if the resulting manifest version doesn't
match — otherwise devices would silently keep a hard-coded version and never
update.

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

Use the Pages URLs rather than GitHub release asset URLs — release URLs redirect
through long signed links that can overflow the ESPHome HTTP client's buffer.

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
   once so the branch exists on GitHub.
3. **Enable Actions on the mirror**: GitHub → *Settings* → *Actions* →
   *General* → *Allow all actions and reusable workflows*. Mirrored repos can
   land with Actions disabled, in which case pushes produce no builds.
4. **Enable Pages**: GitHub → *Settings* → *Pages* → *Build and deployment* →
   *Source*: **GitHub Actions**. Do **not** pick "Deploy from a branch"; the
   workflow uploads the artifact directly.
5. **Run it once**: *Actions* → *Firmware* → *Run workflow*, or push a commit
   touching `esphome/**`. The first run creates the `github-pages` environment
   and the deployment; afterwards
   `https://googlebot42.github.io/picture-frame/` is live.

No secrets need to be configured on GitHub — the workflow only uses the
built-in `GITHUB_TOKEN` and the Pages OIDC identity token, and the factory
configs contain no credentials.

## Working on the page locally

`site/index.html` is a single self-contained file. Open it directly to check
layout; the install buttons will report that installing needs `https://`, and
the per-card version lines stay blank because there is no `firmware/`
directory locally. Both come right once deployed.
