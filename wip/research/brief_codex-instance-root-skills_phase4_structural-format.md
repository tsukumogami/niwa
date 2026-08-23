# Structural Format Review

**Verdict:** PASS

The brief satisfies every validator-level check (FC01-FC04, section order, FC03 bare-word status), is clean for public visibility, and carries only one minor deviation from the format reference plus a few improvement-grade observations.

## Violations Found
1. Frontmatter `problem` and `outcome` fields: each block scalar runs 5 lines → the format reference specifies "a 2-4 line YAML literal block scalar" for both fields (FC01 checks only presence, so no validator failure results) → tighten each summary by one line, e.g. drop "The root's own orientation document tells the worker to invoke a skill it doesn't have." from `problem` (the Problem Statement already elaborates it) and fold the last two sentences of `outcome` into one.

No other violations. For the record, the checks that pass:

- FC01: `status`, `problem`, `outcome` all present; `schema: brief/v1`; optional `motivating_context` is a legal field. `upstream` is correctly absent (freeform entry, no upstream recorded).
- FC02: `status: Draft` is in the valid set.
- FC03: the first non-blank line under `## Status` is the bare word `Draft`, exactly matching the frontmatter, with no trailing prose.
- FC04/order: Status, Problem Statement, User Outcome, User Journeys, Scope Boundary all present and in canonical order; Open Questions and References follow, both legal.
- Open Questions: present while status is Draft, which is the only state that permits it; each entry defers a requirements- or design-level determination rather than a blocker.
- No placeholder text anywhere; every required section carries substantive content.
- Frontmatter/body consistency: `problem` and the Problem Statement describe the same gap (Codex workers at the instance root resolve zero workspace skills while the identical session inside a repo resolves all of them); `outcome` and User Outcome describe the same result (root skill resolution, regenerated gap list, truthful re-gated warning, both capabilities bound to tested deliveries). No contradiction.
- User Journeys: four journeys, each with a `###` name heading, a concrete user (developer, developer again from a different entry point, maintainer, PR reviewer), a trigger, and an outcome shape; entry points are genuinely distinct.
- Scope Boundary: explicit IN and OUT lists; the OUT items are real exclusions a downstream author could plausibly assume in (trust entries, payload-layout widening, a where-from schema axis, trust-bypass flags, the working per-repo delivery).

## Public-Visibility Flags
none

- No `private/` paths, no references to the vision, tools, coding-tools, or dot-niwa-overlay repos, no private issue numbers (no `tsukumogami/vision#NN`-style references at all).
- No `wip/` paths; every References entry is a durable `docs/...` path in the same public repo.
- Row numbers 18 and 19 refer to a public PRD (`docs/prds/PRD-agent-capability-contract.md`) in the same repo, which is in bounds.

## Suggested Improvements
1. Trim the `problem` and `outcome` frontmatter blocks to 4 lines each: brings them inside the format reference's stated length and forces the summaries to stay summaries.
2. Consider softening the fourth journey's mechanics ("renders a session's resolved skills with `codex debug prompt-input` under an isolated `CODEX_HOME`"): the command-level detail edges toward PRD/acceptance-criteria altitude. The journey's shape (reviewer wants evidence at zero model cost, gets a runnable check with a negative control) stands without naming the exact subcommand; the Scope Boundary's "functional acceptance scenario with a negative control" already carries the commitment. Not a violation -- the format guidance rewards concreteness -- but the downstream PRD is the natural home for the invocation.
3. Watch the double-hyphen dash density if the document grows. The em-dash frequency rule matches only the true em dash character, which this document never uses, so no finding results; but the spaced `--` appears at a rate that would trip the threshold if the characters were em dashes. The prose reads well as is; this is a note for future edits, not a change request.

Writing style otherwise clean: no banned words from any category ("journey" appears only as the format-mandated section term), no adverb openers, no vacuous sentences or empty conclusions, demonstratives all have clear antecedents, and the one attribution-shaped claim ("Measurement against the real Codex binary confirmed...") is backed by the cited SPIKE in References. No emojis, no AI attribution, prose opens directly without preamble.

## Summary
The brief is structurally sound: all four validator checks pass, the five required sections appear in canonical order, the Status section holds the FC03 bare-word shape, and Open Questions is legal for its Draft state. It is clean for a public repository -- no private repos, paths, filenames, issue numbers, or wip/ references appear anywhere. The only deviation is the frontmatter summaries running one line over the format reference's 2-4 line guidance, which no validator check enforces; the prose itself is concrete, consistent with the frontmatter, and free of the banned-word and judgment-only style patterns.
