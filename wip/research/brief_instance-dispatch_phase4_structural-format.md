# Structural Format Review

**Verdict:** PASS

The brief satisfies all four FC checks, carries the five required sections in canonical order, opens its body Status with the bare status word, and contains no private-visibility leaks or banned writing-style words.

## Violations Found

none

Detailed check results:

1. Frontmatter validity (FC01/FC02): PASS. Required fields `status`, `problem`, `outcome` all present as literal block scalars. `schema: brief/v1` present as the routing key. `status: Draft` is in the valid set {Draft, Accepted, Done}. No `upstream` field present, so the public-cannot-point-at-private rule is not triggered. Optional `motivating_context` present and well-formed (distinct from problem/outcome, explains why the brief exists now).

2. Required sections present and in order (FC04/FC15): PASS. Status -> Problem Statement -> User Outcome -> User Journeys -> Scope Boundary all present in the canonical order. Optional Open Questions and References follow.

3. Body Status first non-blank line (FC03): PASS. The first non-blank line under `## Status` is the bare word `Draft` alone on its own line (line 27), followed by a blank line and then explanatory prose. This exactly matches the frontmatter `status: Draft`.

4. Public-visibility cleanliness: PASS. The cited issue numbers are all from public repos and explicitly allowed: `tsukumogami/niwa#171`, `tsukumogami/niwa#172`, `anthropics/claude-code#60975`, and `#31940`. No `private/` paths, private repos (vision, tools, coding-tools), private filenames, or private issue numbers. All three referenced docs (DESIGN-ephemeral-session-instances.md, ephemeral-session-instances.md guide, worktree.md guide) exist in-repo at durable paths.

5. No placeholders: PASS. No TODO/TBD/`<...>` placeholder tokens.

6. Frontmatter problem/outcome consistent with body: PASS. The `problem` field (no reliable one-step path to put each background worker in its own fully-configured instance; hook path mis-delivers config, manual path is tedious) matches the Problem Statement's two-path analysis. The `outcome` field (one niwa command yields a background worker in an isolated ephemeral instance loading full config, visible in Agent View, auto-reclaimed) matches the User Outcome section.

7. Open Questions Draft-only: PASS. The section is present, the doc is Draft, and each of the three items genuinely defers a framing detail to the downstream PRD (command verb/flag surface, reclamation aggressiveness, prompt delivery) rather than naming a blocker that should stop the brief.

8. Writing style: PASS. No banned words (tier/tiered, robust, leverage, comprehensive/holistic, facilitate). Prose is direct, no emojis, no AI attribution. Sentence length varies; contractions used naturally.

## Public-Visibility Flags

none

## Suggested Improvements

1. Optional Downstream Artifacts section absence: rationale — none is required while the doc is Draft and no downstream PRD exists yet; this is correct as-is and noted only for completeness, not as a defect.
2. References use repo-relative paths without owner/repo prefixes (e.g. `docs/designs/current/...`): rationale — acceptable since they are same-repo in-repo precedents and the format reserves the `owner/repo:path` convention for cross-repo upstreams; no change needed.

## Summary

The brief passes structural format review on every dimension: valid `brief/v1` frontmatter with all required fields, the five required sections in canonical order, an FC03-clean body Status line whose bare word matches the frontmatter, and frontmatter problem/outcome that track the corresponding body sections. Public-visibility is clean — the only issue references are the explicitly-allowed public `tsukumogami/niwa#NN` and `anthropics/claude-code#NN` numbers, and all three referenced in-repo docs exist at durable paths. No placeholders, no banned writing-style words, and the Draft-only Open Questions section defers framing details appropriately.
