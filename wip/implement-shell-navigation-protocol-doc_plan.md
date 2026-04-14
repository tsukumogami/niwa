# Documentation Plan: shell-navigation-protocol

Generated from: docs/plans/PLAN-shell-navigation-protocol.md
Issues analyzed: 3
Total entries: 1

---

## doc-1: docs/designs/current/DESIGN-shell-navigation-protocol.md
**Section**: frontmatter `status:` field and `## Status` section
**Prerequisite issues**: #1, #2, #3
**Update type**: modify
**Status**: updated
**Details**: Flip `status: Planned` to `status: Current` in the YAML frontmatter, and update the `## Status` section body from `Planned` to `Current`. This matches the convention used by every other doc under `docs/designs/current/` once implementation lands. All three issues must complete first because the design's three-phase implementation (CLI protocol writer, shell wrapper update, stderr routing) corresponds exactly to issues #1, #2, and #3 — the design isn't fully realized until all three phases ship.

---

## Notes on deliberately skipped items

The following potential doc updates were considered and skipped:

- **README.md**: does not document the shell wrapper internals, the `shell-init` subcommands, or the `NIWA_RESPONSE_FILE` protocol variable. No user-facing command surface changes (no new commands, no changed flags). The `niwa shell-init install` re-run requirement is an upgrade note, not steady-state documentation.
- **CHANGELOG / release notes file**: the repo has no tracked changelog file. Release notes are sourced from annotated git tag messages (see `.github/workflows/release.yml`), written at tag time rather than edited as a doc artifact. The required callout ("users must re-run `niwa shell-init install` to pick up the new wrapper") belongs in the tag annotation for the release that includes issue #2, not in a committed doc.
- **Internal refactor (issue #3)**: routing progress output and subprocess stdout to stderr is an internal fix with no user-visible behavior change (progress remains visible because stderr still reaches the terminal). No doc update warranted.
- **Protocol helper (issue #1) in isolation**: `NIWA_RESPONSE_FILE` is explicitly documented in the design doc as an internal protocol variable not intended for direct use by callers. No separate user-facing reference is needed; the design doc itself is the reference.
