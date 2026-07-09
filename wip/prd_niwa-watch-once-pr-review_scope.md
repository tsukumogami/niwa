# PRD scope: niwa-watch-once-pr-review

Upstream: docs/briefs/BRIEF-niwa-watch-once-pr-review.md (Accepted).

## Problem (from brief)
niwa dispatch is a pull verb; nothing stages a PR review proactively, and
naive proactive staging is unsafe (untrusted PR content into an
authority-bearing session = remote-execution vector).

## Research leads
1. niwa dispatch flow + flags + env inheritance (dispatch.go, dispatch_launcher.go).
2. Instance provisioning + whether .claude/settings.json is written/merged.
3. Workspace repo enumeration (workspace.toml sources/repos) for the poll intersection.
4. Command registration pattern for a new `watch` verb.
5. Existing GitHub/gh usage in the codebase.

## Decisions to resolve (carried from brief Open Questions)
D1 workspace-repo coverage + enumeration
D2 handled-set minimum contract (key shape)
D3 directly-requested qualifier semantics
D4 trusted post step shape (affordance + credential provenance) [DESIGN picks mechanism]
D5 per-run staging bound value
