---
name: fetch-work
description: Pull ranked candidate work for stillframe from the Gitea issue tracker, open PRs, and memory. Use when asked "what should we work on", "find something to do", "pick up the next task", or when starting an open-ended session with no specific request.
---

# Fetching work (stillframe)

Produce a ranked view of what's worth doing next, then act according to how
the request was phrased:

1. **Kind-directed** — the user names a kind ("server work", "something
   quick", "firmware robustness"): enumerate the sources, filter to that
   kind, name the top pick and why in a line or two, and start on it.
2. **Agent-choose** — "pick something and do it": choose the top-ranked
   item overall, state what and why in one line, and start.
3. **Suggest** (default when the user is asking, not delegating —
   "what's there to do?"): present the ranked top ~5 and stop; don't start
   work they haven't picked.

## Sources

Enumerated with commands in `sources.md` (kept as a separate file so a
future `unblock` skill can share it). Short version: the Gitea issue
tracker on `zuckerberg/stillframe` is primary; open PRs, the memory
directory, and a broken `nix flake check` on main are the others.

## Ranking

The tracker uses no labels or milestones, so ranking means reading issue
bodies (`tea issues <n> --repo zuckerberg/stillframe`) and judging. Weigh,
roughly in order:

- **Broken beats better.** A failing `nix flake check` on main, or an
  issue describing silent data-loss/hang behavior on the device or server,
  outranks features and chores.
- **Sandbox-verifiability.** This sandbox has no serial/USB and no route
  to the frame or Home Assistant (CLAUDE.md "Working Environment"). Server
  work verifies fully here (Go tests, the NixOS VM test); firmware logic
  verifies via `esphome … compile` and the host unit tests — but anything
  whose acceptance is "watch the panel do X" ends UNVALIDATED and blocked
  on the user's hands. Prefer work that can be *finished* here; take
  hardware-facing work knowingly, and say up front that it will end with a
  hand-off recipe (see `.claude/skills/device-debug/`).
- **User impact per effort.** The frame's failure mode is silent — a
  frame showing a stale photo looks identical to a healthy one — so
  robustness issues (lost refresh cycles, discarded timeouts, no-retry
  paths) punch above their apparent size.
- **Staleness risk.** An issue whose subject area was recently rewritten
  may be already fixed or half-fixed — check `git log` on the files it
  names before ranking it high.

**Dedup before presenting or starting.** Multiple agent sessions share the
`agent` account: check `tea pr list --repo zuckerberg/stillframe` and
recent `origin/main` commits for the same work already in flight or
landed. An open agent PR that just needs verification and merge is itself
top-ranked candidate work.

## Presenting (suggest mode)

One ranked list, each entry a line or two: issue number + title, why now,
effort (S/M/L), and whether it's verifiable in-sandbox or ends blocked on
hardware. Note anything skipped as probably-stale or already in flight.

## After picking

Normal repo workflow — branch from `origin/main`, push to the `fork`
remote, PR via `tea`, merge yourself once verified (owned by the
user-level `git-forges` skill; standing rules there about PR numbers and
`Closes` lines apply). One `Closes #<n>` line per issue in the PR body.
If the work ships firmware behavior, a release is a separate follow-up —
`.claude/skills/cut-release/`.
