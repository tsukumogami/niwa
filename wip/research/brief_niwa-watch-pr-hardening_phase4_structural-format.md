# Structural Format Review

**Verdict:** PASS

The BRIEF satisfies every frontmatter, section-order, FC03, public-visibility, and writing-style rule in the format reference.

## Violations Found

None.

## Public-Visibility Flags

none

All referenced paths (`internal/watch/state.go`, `internal/watch/select.go`, `internal/cli/watch.go`, `docs/briefs/BRIEF-niwa-watch-once-pr-review.md`, `docs/prds/PRD-niwa-watch-once-pr-review.md`) are public niwa-repo paths. No private repos, private paths, private filenames, internal codenames, or private issue numbers appear.

## Suggested Improvements

1. Frontmatter block length: `outcome` runs 5 lines and `problem` sits at the top of the 2-4 line window. The spec's guidance is "2-4 line summary"; trimming `outcome` to 4 lines would sit more comfortably inside the guidance. Non-blocking — this is guidance, not a validated check.
2. `motivating_context` is optional and well-used here, but at ~8 lines it is the longest frontmatter block. Consider tightening it if a reader should be able to skim the frontmatter quickly. Non-blocking.

## Summary

The document carries valid frontmatter (`schema: brief/v1`, `status: Draft`, `problem`, `outcome`, plus optional `motivating_context`; no `upstream` pointing at a private artifact), all five required sections in canonical order, and a body `## Status` whose first non-blank line is the bare word `Draft` followed by a blank line and prose — FC03-clean. The frontmatter `outcome` block matches the body User Outcome's resume model (continue an existing briefed session, start fresh when none survives, self-discard stale staged reviews, hard total cap), and no banned style words, placeholders, emojis, or AI attribution are present. Open Questions is appropriately Draft-only.
