# Structural Format Review

**Verdict:** PASS

The brief has valid frontmatter, all five required sections in canonical order, a bare `Draft` status line that matches the frontmatter, no private references, and prose that stays within the writing-style rules apart from a few small judgment-level nits.

## Violations Found

None.

What I checked:

1. Frontmatter validity: `status: Draft`, `problem`, and `outcome` are all present, and `schema: brief/v1` is set. `Draft` is a valid status. There's no `upstream` field, so the public-visibility direction rule doesn't come into play. `motivating_context` is an allowed optional field.
2. Required sections and order: Status, Problem Statement, User Outcome, User Journeys, Scope Boundary appear in that order. Open Questions and References follow them, and both are allowed optional sections.
3. Body Status line: the first non-blank line under `## Status` is `Draft` on its own, with a blank line before the prose. That matches the frontmatter, so the status-match check passes.
4. Public visibility: the only issue numbers cited are #292, #297, #265, and #279, all from the public niwa repo. The document names no private repos, `private/` paths, or private filenames. The three References paths (`docs/briefs/BRIEF-ephemeral-session-instances.md`, `docs/briefs/BRIEF-instance-dispatch.md`, `docs/guides/worktree.md`) exist in this repo and are durable, and none of them points at `wip/`.
5. Placeholders: none found.
6. Frontmatter consistency: `problem:` says teardown can't find the record for the id the developer holds, so the merged-branch check never runs, and that parallel provisioning can fail or drop mappings. That's the same problem the Problem Statement describes. `outcome:` covers teardown by the id the developer holds (with the merged-branch check or a plain "nothing to check" message) and concurrent dispatches that are all recorded and reachable. That matches the User Outcome. Neither field contradicts the body.
7. Open Questions: the section is present and the status is Draft, which is allowed. All three questions defer framing details to the PRD, and none of them should block the brief.
8. Writing style: no banned words from the organizing, verbs, descriptors, or adverb-opener lists. The document has no em dashes, emojis, or AI attribution, and it doesn't open sections with preamble. "journey" is on the abstract-nouns list, but here it names the required User Journeys section ("the journeys", "the first journey"), so it's the format's own term and not the abstract usage the rule targets.

## Public-Visibility Flags

- motivating_context and the Problem Statement describe a "fixed sequence" developers use to reclaim workers (destroy worktrees, stop and remove the Claude session, then reap). If that sequence is a workspace-internal cleanup routine and not something niwa documents, an outside reader won't recognize it as "the sequence they already follow". This is likely a false positive, since every step is a public command (`niwa worktree destroy`, `claude agents`, `niwa reap`), but the brief could say it's the order those public commands are run in, or point to where it's documented.
- "the ephemeral-session hook" (Scope Boundary, In) refers to the niwa SessionStart hook. That's a public niwa feature covered by BRIEF-ephemeral-session-instances, so it's probably fine.

## Suggested Improvements

1. Frontmatter length: the format reference describes `problem` and `outcome` as 2-4 line block scalars. `problem` runs 6 lines and `outcome` 5. No validator check enforces this, but trimming both brings them in line with the field spec. For example, `problem` could drop "at the two moments that matter most" and fold the last two clauses together.
2. Vague attribution in Scope Boundary, Out: "That's the longer-term refactor tracked separately" names no tracker. Cite the public issue number if there is one, or drop "tracked separately" so the sentence doesn't point at a record the reader can't find.
3. Vague catch-all in Scope Boundary, Out: "and the other open issues around them" doesn't give a downstream author a usable boundary. Name the specific issues or cut the phrase. #265 and #279 already make the point.
4. Weak closing line in User Outcome: "The cleanup sequence they already follow does what it claims to." mostly repeats the paragraph before it. Either cut it or make it specific, for example "the reap that follows no longer runs past a skipped branch check."
5. Redundant closing line in Open Questions: "None of these block the brief; each is a detail the downstream PRD settles." repeats what each question already says (every bullet already names the PRD as the owner). It can go.
6. Opening fragment in Status: "Framing for making niwa's session records dependable..." is a sentence fragment. A full sentence such as "This brief frames..." would read more directly. This is optional.

## Summary

The brief meets every structural requirement: valid frontmatter, the five required sections in order, a status line that matches, a Draft-only Open Questions section, and durable public references. The public-visibility check is clean except for one likely false positive about a described cleanup sequence. The suggestions are small wording fixes: shorter frontmatter summaries, a citation for "tracked separately", and removing two restating closing lines.
