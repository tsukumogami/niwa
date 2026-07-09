# Structural Format Review

**Verdict:** PASS

The brief satisfies all frontmatter, section-order, FC03, public-visibility, and writing-style requirements with no violations found.

## Violations Found
None.

Detailed check results:

1. **Frontmatter validity** -- PASS. Required fields present: `status: Draft`, `problem` (block scalar), `outcome` (block scalar). `schema: brief/v1` present as routing key. `motivating_context` present (valid optional field). `upstream` deliberately omitted -- confirmed correct per spec ("Omit the field entirely when the upstream is a private artifact a public brief cannot name"). Not a violation.
2. **Required sections and order** -- PASS. Status (L27), Problem Statement (L38), User Outcome (L71), User Journeys (L103), Scope Boundary (L149), then optional Open Questions (L237) and References (L262). Canonical order preserved; optional sections follow the required five.
3. **FC03** -- PASS. First non-blank line under `## Status` is the bare word `Draft` (L29) alone, followed by a blank line (L30), then explanatory prose (L31+). Matches frontmatter `status: Draft` exactly.
4. **Public-visibility cleanliness** -- PASS. No private paths, no `private/` references, no `tsukumogami/vision` or other private repos, no private filenames (ROADMAP-event-driven-dispatch, STRATEGY-*, SPIKE-*), no private issue numbers, no private feature codes (ED1/ED2, etc.). References cite only public niwa-repo paths (`internal/cli/dispatch.go`, `internal/cli/dispatch_launcher.go`) and the public `/dispatch` skill. "Downstream PRD/DESIGN" are generic artifact types, not named private documents. Reads self-contained around the niwa `watch --once` feature.
5. **No placeholders** -- PASS. Every section carries real, specific content.
6. **Frontmatter consistency** -- PASS. The `problem:` block (dispatch is a pull verb; naive proactive staging is a remote-execution vector) matches the Problem Statement's two-part framing. The `outcome:` block (one stateless hand-run command surfaces an already-staged, contained, pre-drafted review; post is a separate trusted step) matches the User Outcome section. No contradiction.
7. **Open Questions (Draft-only)** -- PASS. Present, brief is Draft, and each of the five entries genuinely defers a framing detail to the downstream PRD/DESIGN (workspace-repo coverage, handled-set minimum contract, directly-requested qualifier semantics, trusted-post-step shape, per-run staging-bound value) rather than raising a blocker.
8. **Writing style** -- PASS. No occurrences of "tier/tiered", "robust", "leverage", "comprehensive/holistic", or "facilitate". Prose is direct, uses contractions, varies sentence length. No emojis. No AI attribution.

Additional structural confirmations: User Journeys carries four distinct journeys (L104, L119, L129, L141), each with a `###` name heading, a named user, an explicit Trigger, and an explicit Outcome, with differing entry points (first run, re-run dedup, hostile-PR containment, team-scoped exclusion). Scope Boundary carries explicit IN and OUT lists with real, non-filler exclusions.

## Public-Visibility Flags
none

## Suggested Improvements
1. Downstream Artifacts section: none present, which is correct at Draft with no downstream artifact yet; populate with durable repo-relative paths once the PRD or DESIGN lands.
2. Open Questions closure note: optionally name the downstream PRD's Decisions and Trade-offs section as the canonical closure surface for these questions, matching the format reference's guidance. Optional polish, not a gap.

## Summary
The BRIEF passes structural format review cleanly on this re-review. Frontmatter is valid with the upstream field correctly omitted for a public brief whose upstream is a private roadmap, all five required sections appear in canonical order, and FC03 holds with a bare `Draft` first line matching the frontmatter. The document is free of private references and banned writing-style terms; no violations block acceptance and the two suggestions are optional forward-looking polish.
