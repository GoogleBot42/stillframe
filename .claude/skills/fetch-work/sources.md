# Where stillframe work comes from

Shared source list for `fetch-work` (and a future `unblock` skill, which
would re-read the same sources asking "which HUMAN actions unblock the
most?"). Keep this file the single owner of the enumeration commands.

## 1. Gitea issue tracker (primary)

```bash
tea issues list --repo zuckerberg/stillframe --output simple   # open issues
tea issues <n> --repo zuckerberg/stillframe                    # one body
```

No labels or milestones are in use — titles are written to be
self-ranking-ish, but read the body before committing to a rank. Issues
were largely filed by past agent audit sessions, so bodies usually contain
the exact file/line evidence; verify it against the current tree (the code
may have moved since the issue was filed).

## 2. Open PRs — in-flight and unfinished work

```bash
tea pr list --repo zuckerberg/stillframe --output simple
```

Serves two purposes: dedup (don't restart work another session already
has open) and candidate work in its own right (an agent PR awaiting
verification/merge is often the highest-value next action).

## 3. Memory directory

`/home/googlebot/.claude/projects/-home-googlebot-workspace-picture-frame/memory/`
— read `MEMORY.md` first. Holds pending confirmations and researched-but-
unimplemented plans. Memories are point-in-time: a "pending" there may
have since been filed as an issue (preferred — the tracker outranks
memory as the work queue) or completed; cross-check before treating it as
open work.

## 4. Health of main

```bash
nix flake check
```

A red check on a fresh `origin/main` checkout is drop-everything work that
outranks every tracker item. Green is the normal case and takes a few
minutes to confirm; run it when the recent commit history suggests risk,
not reflexively.
