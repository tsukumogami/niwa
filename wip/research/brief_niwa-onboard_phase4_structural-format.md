# Structural Format Review

**Verdict:** PASS

The brief carries valid frontmatter, all five required sections in canonical order, a clean FC03-compliant Status opening, four distinct and complete User Journeys, and real (non-filler) Scope Boundary exclusions, with no public-visibility leaks.

## Violations Found

None.

## Public-Visibility Flags

None. `tsukumogami/niwa#194` and `tsukumogami/niwa#199` are cited repeatedly (lines 20-21, 35-37, 170-171, 188-191), but `niwa` is a public repo per the workspace repo table, so these citations are permitted under the "public GitHub issue numbers from the same repo are routinely cited" carve-out. No `private/` paths, private repo issue numbers, `wip/` paths, dispatch-brief paths, or local filesystem paths appear anywhere in the document body. In-repo doc references (`docs/guides/machine-identity-vault-sync.md`, `docs/guides/vault-integration.md`, `docs/guides/init-bootstrap.md`, `docs/briefs/BRIEF-instance-dispatch.md`) are all durable repo-relative paths.

## Suggested Improvements

1. Trim the `problem` and `outcome` frontmatter blocks to the reference's 2-4 line guidance: both currently run 6 lines each (lines 4-10 and 11-17). This isn't a validated check (FC01 only tests field presence) so it isn't a violation, but tightening would align the artifact with the documented convention.
2. Consider whether "Team admin hits a step their plan won't allow" (journey 3) could more sharply signal its distinctness from journey 1 up front — both open on a team admin running the team setup, and the differentiating trigger (plan-gated step) doesn't appear until partway into the paragraph. This is a readability nit, not a structural failure; the journey does carry a genuinely distinct trigger and outcome shape.

## Summary

This re-review of the User Journeys section (and a full pass over the rest of the document) finds no structural violations. All four required-sections mechanics (frontmatter validity, section presence/order, FC03 body-status match, and public-visibility cleanliness) pass. The User Journeys section, the target of the prior edit pass, now contains four journeys that each name a concrete user, a distinct trigger, and a distinct outcome shape, satisfying the finalization-readiness bar for that section. No blocking issues remain from a structural-format standpoint.
