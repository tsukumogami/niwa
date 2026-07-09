# Structural Format Review

**Verdict:** PASS

The BRIEF satisfies every structural, frontmatter, section-order, FC03, public-visibility, and writing-style rule with no violations.

## Violations Found

None.

Checks confirmed:
1. Frontmatter valid. `schema: brief/v1`, and all three required fields (`status`, `problem`, `outcome`) present (FC01 pass). `status: Draft` is a valid status (FC02 pass). `motivating_context` is a permitted optional field. `upstream` is correctly OMITTED -- the note confirms the upstream is a private roadmap a public brief cannot name, so omission is the correct behavior, not a violation.
2. Required sections all present and in canonical order (FC04/FC15 pass): Status, Problem Statement, User Outcome, User Journeys, Scope Boundary. Optional Open Questions and References follow.
3. FC03 pass. First non-blank line under `## Status` (line 29) is the bare word `Draft` alone; explanatory prose begins after a blank line (line 31). Matches frontmatter `status: Draft`.
4. Frontmatter/body consistency pass. `problem:` (pull-verb toil + unsafe-if-naive) is the same problem the Problem Statement elaborates. `outcome:` (one hand-run command surfaces an already-staged, contained review) is the same outcome the User Outcome elaborates.
5. No placeholders. Every required section carries real, specific content.
6. Open Questions valid. Status is Draft, so the section is permitted; its three items genuinely defer framing details to the downstream PRD rather than raising blockers.
7. User Journeys well-formed. Four distinct journeys, each with a `###` name heading, a named user, an explicit Trigger, and an explicit Outcome. Entry points differ (first run, re-run dedup, hostile-PR containment, team-scoped exclusion).
8. Scope Boundary has explicit IN and OUT lists with real, non-filler exclusions (scheduling, durable dedup, attention/cost controls, multi-repo scale-out, relevance model, ambient sources, sandbox residual caveats).

## Public-Visibility Flags

None. No private paths, no private repos (no `tsukumogami/vision`, `private/`, etc.), no private filenames (no `ROADMAP-*`, `STRATEGY-*`, `SPIKE-*`), and no private issue numbers. The References section cites only public niwa-repo paths (`internal/cli/dispatch.go`, `internal/cli/dispatch_launcher.go`). The "downstream PRD" and "downstream DESIGN" mentions are generic artifact types, not named private documents.

## Suggested Improvements

1. Optional Downstream Artifacts section: none present, which is correct at Draft with no downstream artifact yet; no action needed but worth populating once the PRD lands.
2. Writing-style scan is clean -- no banned words (`tier/tiered`, `robust`, `leverage`, `comprehensive/holistic`, `facilitate`), no preamble, no emojis, no AI attribution. No changes required.

## Summary

The BRIEF is structurally compliant on all counts: valid frontmatter with the three required fields, correct section presence and order, a clean FC03 bare-status-word line, and consistent frontmatter-to-body problem/outcome pairing. The deliberate `upstream` omission is correct for a public brief whose upstream is private, and the document is free of private references and banned writing-style terms. Verdict is PASS with no violations.
