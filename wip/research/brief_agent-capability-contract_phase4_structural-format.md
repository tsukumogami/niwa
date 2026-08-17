# Structural Format Review

**Verdict:** PASS

The brief is structurally valid: frontmatter is complete and well-formed, all five required sections appear in canonical order, the body Status opens with the bare word `Draft` matching the frontmatter, and the prose is public-visibility clean with no placeholders.

## Violations Found

None.

Checks performed:

1. Frontmatter validity: `schema: brief/v1`, `status: Draft` (valid FC02 value), `problem` and `outcome` present as literal block scalars, optional `motivating_context` present and permitted. No `upstream` field, as expected for this brief. FC01/FC02 pass.
2. Required sections: Status, Problem Statement, User Outcome, User Journeys, Scope Boundary — all present, in exactly that order (FC04/FC15 pass). Optional sections (Open Questions, References) follow the required set in the matrix's order.
3. FC03: the first non-blank line under `## Status` is the bare word `Draft`, with explanatory prose after a blank line. Matches frontmatter `status`. Passes.
4. Open Questions present and document is in Draft status — permitted. Each question defers a requirements- or design-level determination to the downstream PRD rather than blocking the brief.
5. No placeholder text anywhere; every section carries real content.
6. Frontmatter/body consistency: the `problem` block (Claude-shaped path, contract reaching two of ~twenty capabilities, no test that fails on a faked implementation, prior attempt as dead plumbing) is the same problem the Problem Statement elaborates. The `outcome` block (no forced agent choice, generated gap list, structural claims checked by tests, provable no-behavior-change first delivery) is the same outcome the User Outcome section elaborates. No contradiction.
7. User Journeys: four journeys, each with a `###` name heading, a concrete user (mixed-team developer, reviewing maintainer, gap-hitting developer, later contributor), a trigger, and an outcome shape. Distinct entry points.
8. Scope Boundary: explicit In and Out lists; Out items are real exclusions (side-by-side multiplexing, re-measuring the spike, weakening the 15-scenario acceptance bar, third-agent delivery).
9. Writing style: no banned words from rules.yaml (no tier/robust/comprehensive/leverage/facilitate/seamless/etc.), no adverb openers, no preamble, no emojis, no AI attribution. "Journeys" appears only as the format-mandated section heading. No vacuous sentences, dangling demonstratives, or uncited attributions found; claims are anchored to named artifacts (#248, #254, the spike, the design doc).

## Public-Visibility Flags

none

Scanned for: word-boundary "vision" (none; no "provision" false positives either), "dot-niwa-overlay" (none), "coding-tools" (none), "tsukumogami/vision" (none), `wip/` paths (none). All issue references (tsukumogami/niwa#248, tsukumogami/niwa#254) are public niwa issues/PRs, which are explicitly permitted. All document paths (docs/spikes/SPIKE-codex-discovery-mechanics.md, docs/designs/current/DESIGN-claude-key-consolidation.md) are durable repo-relative paths in the public repo.

## Suggested Improvements

1. Em-dash density: the body carries roughly 18 em dashes over about 1,550 scoped prose words, around 11-12 per thousand — above the rules.yaml threshold of 10 per thousand. Converting three or four of them to commas, periods, or parentheses (candidates: the User Outcome and Contributor-journey paragraphs, which stack several) would bring the document under the threshold.
2. Frontmatter block length: the `problem` and `outcome` literal blocks run 5 wrapped lines each against the format's "2-4 line summary" guidance, and `motivating_context` runs 7. The content is appropriately summary-shaped, so this is a wrap-width nit; tightening each by a clause would match the guidance exactly.

## Summary

The brief passes every structural check: valid frontmatter, all five required sections in canonical order, a bare-word Status line matching the frontmatter, Draft-only Open Questions, and full frontmatter/body consistency. The prose is public-visibility clean — all cited issues and paths belong to the public niwa repo, and no private repo, private path, or wip/ reference appears. The only style findings are advisory: em-dash density slightly above the frequency threshold and frontmatter summary blocks a line over the 2-4 line guidance.
