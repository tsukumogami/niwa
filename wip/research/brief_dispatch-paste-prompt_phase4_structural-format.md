# Structural Format Review

**Verdict:** PASS

The brief satisfies every structural check in `brief-format.md` -- valid frontmatter, all five required sections present and in canonical order, an FC03-clean bare `Draft` on the Status first line, no private or `wip/` references, no placeholders, and no writing-style violations.

## Violations Found

None.

Check-by-check record:

1. **Frontmatter validity** -- PASS. `schema: brief/v1`, `status: Draft`, `problem:` and `outcome:` are present as literal block scalars (4 lines each, within the 2-4 guidance). `motivating_context:` is present and is a documented optional field. `upstream:` is correctly omitted -- the context file records entry mode `freeform` with no upstream path, and the format spec says the field is optional precisely for that case. `status: Draft` is one of the three valid FC02 values.

2. **Required sections present and in order** -- PASS. Headings appear as `## Status` (L22), `## Problem Statement` (L33), `## User Outcome` (L63), `## User Journeys` (L82), `## Scope Boundary` (L116). Optional sections follow in matrix order: `## Open Questions` (L153), `## References` (L167). No stray or duplicated headings.

3. **Body Status matches frontmatter (FC03)** -- PASS. The first non-blank line under `## Status` is `Draft` (L24), bare on its own line, followed by a blank line before the transition-context paragraph at L26. The compared value is exactly `Draft`, equal to the frontmatter `status`. This is the check most briefs fail; this one is shaped correctly.

4. **Public-visibility cleanliness** -- PASS. Grep for `private/`, private repo names (`vision`, `coding-tools`, `dot-niwa-overlay`), and private-repo issue-number patterns returns nothing. No `wip/...` path appears anywhere in the committed artifact -- the wip references live only in `wip/brief_dispatch-paste-prompt_context.md`, which is the staging file, not the brief. Both References entries are durable repo-relative paths that resolve in this repo: `docs/briefs/BRIEF-instance-dispatch.md` and `docs/prds/PRD-instance-dispatch.md` both exist on disk. There is no `Downstream Artifacts` section, so the durable-path rule for that section does not apply.

5. **No placeholders** -- PASS. Every required section carries real, specific content. No `TBD`, `TODO`, `<...>`, or template residue.

6. **Frontmatter consistency with body** -- PASS. The `problem:` block names the single-positional-argument constraint and the three bad options (retyping, summarizing away detail, shell quoting); the Problem Statement elaborates exactly those three, then adds the `-d "$(cat)"` workaround as evidence of the gap's size. The `outcome:` block names select-run-paste-one-key, the option to add context first, and the absence of an intermediate file or quoting; the User Outcome section elaborates each of those in the same order and adds the size-ceiling recoverability paragraph, which the frontmatter summary reasonably omits. Elaboration in both directions, no contradiction.

7. **Open Questions is Draft-only** -- PASS. The section is present and the document is `Draft` in both frontmatter and body. Its four items each defer a framing detail downstream (ceiling value and messaging, non-TTY behavior, composition with detach, non-interactive invocation) rather than raising a blocker that should stop the brief. Note for finalization: per the lifecycle table, this section must be emptied or removed before the Draft -> Accepted transition.

8. **Writing style** -- PASS. Grep for the workspace's banned words (`tier/tiered`, `robust`, `leverage`, `comprehensive`, `holistic`, `facilitate`) returns no hits. No preamble constructions ("it's worth noting", "it should be noted"). No emoji (Unicode scan clean). No AI attribution or co-author lines. Em-dash substitutes are written as `--` consistently, matching workspace convention. Sentence length varies; contractions appear naturally ("doesn't", "is not"). Prose leads with the concrete fact in each section rather than throat-clearing.

## Public-Visibility Flags

none

## Suggested Improvements

1. **Journey user specificity**: all four journeys open with "A developer" or "The same developer". The format's quality guidance asks each journey to name a concrete user, and the entry points here are genuinely distinct (bare paste, annotated paste, oversize paste, abandoned capture), so the check passes on substance. Still, giving two of them a slightly sharper role -- the developer working over SSH, the developer mid-review on someone else's branch -- would make the distinctness visible from the first line rather than from the trigger.

2. **List-formatting symmetry in Scope Boundary**: the OUT items lead with a bolded phrase (`**Launching `$EDITOR`.**`, `**A `--clipboard` flag**`), while the IN items are plain sentences. Both are valid; matching the two would let a reader scan the boundary as one table rather than two shapes. Purely cosmetic -- no rule requires it.

3. **Status prose could name the Open Questions dependency**: the Status paragraph already assigns the ceiling, the non-TTY behavior, and the acceptance criteria to the downstream PRD, which maps cleanly onto three of the four open questions. Adding a clause that the Open Questions section must clear before Accepted would make the finalization precondition self-evident to a reader who lands on the brief cold.

4. **`motivating_context` is well used**: not an improvement so much as a note for the record -- the field earns its place here, since the "the material most worth handing off is the material hardest to hand off" framing explains why the brief exists now in a way the `problem` field alone does not.

## Summary

The brief passes all eight structural checks with no violations. Frontmatter carries the three required fields plus two valid optional ones, the five required sections appear in canonical order followed by correctly-ordered optional sections, the Status first line is a bare `Draft` matching the frontmatter, and the document is clean of private references, `wip/` paths, placeholders, and banned style words. The three suggestions above are cosmetic or forward-looking -- none blocks a PASS verdict, and none needs addressing before the Phase 4 jury closes.
