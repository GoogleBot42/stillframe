---
name: cut-release
description: Cut a stillframe firmware release — tag via the Gitea API, wait for the GitHub mirror's CI to build all five factory variants, verify the deployed Pages manifest. Use when asked to release, tag a version, or ship firmware.
---

# Cutting a stillframe firmware release

Recap of the release model (owned in full by CLAUDE.md's "Releases" section —
read that first if anything here is ambiguous): Gitea
(`git.neet.dev/zuckerberg/stillframe`) is the source of truth,
`github.com/GoogleBot42/stillframe` is a read-only push mirror whose Actions
build and whose Pages host, and the pushed `vX.Y.Z` tag *is* the version —
there is no `VERSION` file. This skill owns everything after "the fix is
merged to `main`": the agent-side tagging mechanics, polling CI to
completion, and verifying the deploy actually reached devices.

This procedure has been run seven times for real (v1.0.0 through v1.0.6). The
steps and numbers below are what actually happened, not a guess.

## 1. Precondition: the change is on Gitea `main`

Getting a fix from a branch/PR onto `main` is PR mechanics — fork push,
`tea pr create`, API merge — and is owned by the user-level `git-forges`
skill. Use it first if the change isn't merged yet.

Once merged:

```bash
git checkout main
git pull origin main
```

`origin` is the Gitea remote (`gitea@git.neet.dev:zuckerberg/stillframe.git`);
there is also a `fork` remote (`gitea@git.neet.dev:agent/stillframe.git`) used
for PR branches, but releases are tagged on `origin`'s `main`.

## 2. Pick the version

Semver `vX.Y.Z` — next patch for a fix, next minor for a feature. Use
`vX.Y.Z-rc.N` for a prerelease: it builds and attaches to a GitHub Release
like any other tag, but deliberately does **not** move the Pages channel, so
no device ever installs a release candidate automatically (see step 5's
gotcha).

## 3. Tag via the Gitea REST API — not `git tag` + `git push`

Every one of the seven real releases was cut this way, and it's the only way
that works from this environment: an agent has no push access to `origin`'s
refs (the bot's Gitea account can push branches to its own `fork` remote and
open PRs, but cannot push a tag directly to `zuckerberg/stillframe`). The
Gitea API, authenticated with the bot's own token, creates the tag directly
on the target repo without needing a git push at all.

The token lives in `~/.config/tea/config.yml` under a `token:` key (already
used by the `tea` CLI from the `git-forges` skill). Pull it out and call the
tags endpoint:

```bash
TOKEN=$(grep -oP 'token:\s*\K\S+' ~/.config/tea/config.yml)
curl -s -X POST -H "Authorization: token $TOKEN" -H "Content-Type: application/json" \
  https://git.neet.dev/api/v1/repos/zuckerberg/stillframe/tags \
  -d '{"tag_name":"v1.0.6","target":"main","message":"v1.0.6 — fix watchdog clobber on update-check overlap"}'
```

`v1.0.6` and the message above are from an actual past release — substitute
the real version and a one-line summary of the change being shipped. `target`
is the branch or commit to tag; `main` after step 1's pull is correct.

A successful call returns the new tag's JSON (id, commit sha, etc.). Do this
once per release — the Gitea push mirror picks up the new tag on its own
sync interval (see step 4's gotcha if it seems slow).

## 4. Poll the GitHub mirror's CI to completion

The tag mirrors to `github.com/GoogleBot42/stillframe`, whose
`.github/workflows/firmware.yml` triggers on `v*` tags (and
`workflow_dispatch` against an existing tag as a manual re-run/recovery
path). It builds all five factory variants (`color7-tinypico`,
`color7-tinys3`, `grey16-tinypico`, `grey16-tinys3`, `spectra13-inkplate`),
publishes a GitHub Release, and deploys Pages.

Poll the **public, unauthenticated** GitHub REST API — no token needed for
reads on a public repo:

```bash
curl -s "https://api.github.com/repos/GoogleBot42/stillframe/actions/runs?per_page=1&branch=v1.0.6"
```

The `branch=` query parameter also matches the tag name for a tag-triggered
run — confirmed against real release runs, not just docs. Extract `status`
and `conclusion`:

```bash
R=$(curl -s "https://api.github.com/repos/GoogleBot42/stillframe/actions/runs?per_page=1&branch=v1.0.6")
echo "$R" | grep -oP '"status":\s*"\K[^"]+' | head -1
echo "$R" | grep -oP '"conclusion":\s*"?\K[^,"]*' | head -1
```

`status` goes `queued` → `in_progress` → `completed`; only check `conclusion`
(`success` / `failure` / `cancelled`) once `status` is `completed`.

**Timing — the actual numbers, not an estimate.** Across all seven real
releases:

- v1.0.1 through v1.0.6 (warm GitHub Actions / ESPHome build caches) each
  took **about 5 minutes** end-to-end, tag-push to Pages-deployed.
- v1.0.0, the very first release ever run against this mirror, took **about
  25 minutes** — cold caches everywhere (ESP-IDF toolchain download for
  `spectra13-inkplate` in particular, plus every other variant's first
  ESPHome build cache).

So the realistic expectation is "back in about 5 minutes," but budget up to
roughly 30 minutes if this is the first release after a long gap, a
dependency bump, or anything else that could evict the build caches. Don't
report failure just because 5 minutes have passed with no `completed` status.

This harness blocks chaining `sleep` + `curl` directly in the foreground, so
run the poll loop with `run_in_background: true` (Bash tool). A short
interval with a generous ceiling covers both the common 5-minute case and the
rare cold-cache case without either spamming the API or timing out early:

```bash
for i in $(seq 1 60); do
  R=$(curl -s "https://api.github.com/repos/GoogleBot42/stillframe/actions/runs?per_page=1&branch=v1.0.6")
  STATUS=$(echo "$R" | grep -oP '"status":\s*"\K[^"]+' | head -1)
  CONCL=$(echo "$R" | grep -oP '"conclusion":\s*"?\K[^,"]*' | head -1)
  echo "$(date -u +%H:%M:%S) status=$STATUS conclusion=$CONCL"
  [ "$STATUS" = completed ] && { echo "RUN: $CONCL"; break; }
  sleep 30
done
```

60 iterations of 30 seconds is a 30-minute ceiling. Adapt the version string
in the URL — `v1.0.6` above is an example, not a literal to reuse.

**Mirror lag.** The tag appears on GitHub shortly (usually seconds) after the
Gitea API call in step 3, via Gitea's push mirror sync. If the very first
poll returns `"total_count": 0` (no matching run), the mirror likely hasn't
synced yet — keep polling rather than assuming the tag or the workflow is
broken.

## 5. Green CI is not "released" — verify the deployed manifest

The `deploy` job publishing successfully doesn't by itself prove a device
would see the update; confirm the live Pages manifest actually advertises the
new version:

```bash
curl -s https://googlebot42.github.io/stillframe/firmware/spectra13-inkplate/manifest.json \
  | grep -oP '"version":\s*"\K[^"]+'
```

This must print the new version **without** the leading `v` (e.g. `1.0.6`,
not `v1.0.6`) — the workflow strips it before stamping
`esphome.project.version`, and the device's `http_request` update component
compares the manifest's `version` against its compiled version with plain
string equality (no semver parsing), so a mismatched format would silently
mean "never offers an update." Check whichever variant(s) are relevant to the
change; all five get their own `firmware/<variant>/manifest.json` and all
five should agree once a stable release deploys.

**If this step is skipped for a prerelease tag (`-rc.N`):** don't bother —
the `deploy` job intentionally does not run for prereleases (see the gotcha
below), so the Pages manifest will still show the previous stable version by
design. Verify a prerelease instead by checking the GitHub Release page for
the attached `*.factory.bin` / `*.ota.bin` / manifest files.

## 6. Optional pre-tag sanity check

Before tagging, it's much cheaper to catch a broken config locally than to
wait out a full CI run and then re-tag:

```bash
cd esphome/factory
esphome -s components_source ../components compile <variant>.yaml
```

Run this for whichever variant(s) the change actually touches — it's the
same build CI runs (factory config, `components_source` pointed at the local
tree), just without the version stamping or the four other variants.

## Failure modes and gotchas

- **Pages deploy rejected despite green build jobs.** The `deploy` job can
  fail purely on GitHub's environment protection rules even when every
  `build` job (and `release`) succeeded — the `github-pages` environment
  GitHub auto-creates is restricted to the `main` branch by default, and this
  workflow deploys from a tag. The one-time fix (adding a `v*` tag rule under
  the environment's deployment branches/tags) is owned by `site/README.md`
  ("One-time setup for the repo owner" section) — don't re-derive it here,
  just go apply that fix if this is what's happening. After fixing it,
  "Re-run failed jobs" on the existing run recovers it; nothing needs
  rebuilding.
- **Never commit, tag, or edit anything on the GitHub mirror.** It is a
  read-only push mirror; all git data flows one way, Gitea → GitHub. If a
  release needs fixing, fix it on Gitea and cut a new tag (or use
  `workflow_dispatch` against the existing tag to re-run/re-publish, per
  step 4).
- **Prerelease tags never move the Pages channel.** A `-rc.N` suffix makes
  the `version` job set `prerelease=true`, and the `deploy` job is gated on
  `prerelease == 'false'` — it does not run at all for a release candidate.
  The build and GitHub Release still happen normally; only Pages (the URL
  every device actually polls) is skipped. This is deliberate, not a bug: it
  keeps a release candidate from being auto-installed by every frame in the
  field.
- **A tag with no matching run on GitHub** almost always means mirror lag
  (see step 4), not a broken workflow — keep polling for a minute or two
  before concluding anything is wrong. If it genuinely never appears, check
  the Gitea repo's push-mirror settings/credentials rather than the GitHub
  side.
- **All five variants must succeed for `release` and `deploy` to run** — both
  jobs `need: [version, build]` with the full build matrix, and each stages
  files expecting to find exactly 5 artifacts (`if [ "$found" -ne 5 ]`). One
  broken variant blocks the whole publish, not just that variant.
