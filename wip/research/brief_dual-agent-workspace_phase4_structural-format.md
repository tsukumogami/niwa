# Verdict: PASS

Reviewed: `/home/dgazineu/dev/niwaw/tsuku/tsuku+codex_dual_agent-4ff0633a/public/niwa/docs/briefs/BRIEF-dual-agent-workspace.md`
Contract: `/home/dgazineu/.claude/plugins/cache/shirabe/shirabe/0.18.1-dev/skills/brief/references/brief-format.md`

## Criterion findings

### 1. Required sections present and in canonical order — MET

Heading scan returns, in file order: `## Status` (line 24), `## Problem
Statement` (33), `## User Outcome` (58), `## User Journeys` (78), `## Scope
Boundary` (116), `## Open Questions` (164). That is all five required sections
in the canonical order the format matrix specifies, followed by one optional
section.

`Open Questions` is the only optional section present, and it is permitted
because the document's status is `Draft`. Both of its entries defer a framing
detail to the downstream PRD (hook delivery scope; whether the interactive TUI
starts clean) rather than posing a blocker that should have stopped the brief —
consistent with the section's Draft-only role. It must be emptied or removed
before the Draft -> Accepted transition, which is a future-transition
obligation, not a defect now.

`Downstream Artifacts` and `References` are absent, which is valid — both are
optional and no downstream artifact exists yet.

Sub-structure inside the required sections satisfies the mechanical
requirements: User Journeys carries four `###` name headings (lines 80, 89, 99,
107), and Scope Boundary carries both an explicit `### IN` (118) and `### OUT`
(138) list.

### 2. Frontmatter fields and status value — MET

Frontmatter spans lines 1-20. `schema: brief/v1` (line 2) matches the pinned
artifact-type contract. All three required fields are present: `status: Draft`
(3), `problem` (4), `outcome` (9). `status` is one of the valid FC02 set
(Draft, Accepted, Done).

`problem` and `outcome` are both YAML literal block scalars written with `|`,
and both run four lines of body — inside the 2-4 line guidance. Content
correspondence holds in both cases: the `problem` field's "agent choice is
exclusive and made at creation time... preparing for Codex replaces the Claude
context at the levels niwa owns and skips the repository and worktree levels"
is exactly what the Problem Statement elaborates at lines 36-46; the `outcome`
field's "runs `claude` exactly as today, and runs `codex` in the same instance
from any directory inside it" is what the User Outcome section elaborates at
lines 60-76.

The one optional field present, `motivating_context` (line 14), is a
well-formed literal block scalar and does the job the format reserves for it —
it carries why the brief is being written now (exploration against a specific
codex-cli version overturned an earlier premise), which is distinct from both
the problem and the outcome.

### 3. Body `## Status` first non-blank line — MET (strict check)

Under `## Status` at line 24, line 25 is blank and line 26 is `Draft` — the
bare status word alone on its own line, nothing appended. Line 27 is blank, and
the explanatory prose about what the downstream PRD and DESIGN own begins at
line 28. This is precisely the FC03-passing shape, and it avoids the format's
single most common failure (prose sharing the first line). Frontmatter `status:
Draft` and that line are an exact match.

### 4. Public-visibility clean — MET

Every grep the review calls for comes back empty against the document:

- Word-boundary `vision` (case-insensitive): no hits. The document contains no
  occurrence at all, not even a `provision`-adjacent false positive to
  adjudicate — "Ephemeral session provisioning" at line 143 does not match the
  word-boundary pattern.
- Org-qualified private repo names `tsukumogami/vision`, `tsukumogami/tools`,
  `tsukumogami/coding-tools`, `tsukumogami/dot-niwa-overlay`: no hits.
- `private/` path prefix: no hits.
- Feature-numbering scheme `\bF[0-9]\b`: no hits, so no upstream plan's feature
  identifiers leak in.
- Related private-context terms (overlay, strategic, market, competitive,
  competitor, roadmap, `STRATEGY-`, `VISION-`): no hits.

No `upstream:` frontmatter field is present. Its absence is correct and
deliberate here: the format explicitly permits omitting `upstream` when the
strategic ancestor is a private artifact a public brief cannot name, and adding
it would be the exact leak this criterion guards against.

The two issue references that do appear — `niwa#228` (line 149) and `niwa#247`
(line 161) — are issue numbers in this same public repository. The format
reference calls that out directly: public GitHub issue numbers from the same
repo are routinely cited and are not in scope of the restriction, which covers
only private-repo issue numbers. Both are fine.

### 5. Writing style — MET

The repo's `.claude/helpers/writing-style.md` does not exist at either the
workspace root or under `public/niwa/`, so the check ran against the banned-word
quick reference in the workspace `CLAUDE.md` plus niwa's own `CLAUDE.md`
conventions.

Banned words — `tier`/`tiered`, `robust`, `leverage`, `comprehensive`,
`holistic`, `facilitate` and their inflections — produce no hits. No emoji
appears anywhere in the file (checked across the emoji, dingbat, and arrow
Unicode ranges). No AI attribution or co-author line appears: no
`Co-Authored-By`, no "Generated with", no mention of Anthropic or Claude Code
as an authoring tool. Note that "Claude Code" appears at line 66 as the name of
the agent the feature serves, which is subject matter, not attribution.

Prose is direct, with no preamble hedges — a scan for the usual openers and AI
tells ("It's worth noting", "It is important to note", "delve", "seamless",
"underscore", "testament", "unlock", "elevate", and others) returns nothing.
Sentence length varies and contractions are used ("doesn't", "can't", "isn't"),
matching the requested register.

## Validator output

Command run from `/home/dgazineu/dev/niwaw/tsuku/tsuku+codex_dual_agent-4ff0633a/public/niwa`:

```
shirabe validate docs/briefs/BRIEF-dual-agent-workspace.md --format json --visibility=Public
```

Exit status 0. The full envelope:

```json
{
  "schema_version": "shirabe-validate/v1",
  "summary": {
    "outcome": "clean",
    "errors": 0,
    "notices": 0
  },
  "findings": [],
  "advisory": {
    "summary": "Draft posture: no draft-tolerable findings to flag.",
    "notes": []
  }
}
```

Outcome is `clean` with zero errors and zero notices, and the findings array is
empty. The advisory block adds nothing: "Draft posture: no draft-tolerable
findings to flag."

## wip/ reference check

A grep for `wip/` across the document returns no matches. Nothing in the brief
points at a non-durable path, so no reference will dangle when workflow cleanup
deletes the staging area. The document also carries no Downstream Artifacts
section, which is where a `wip/...` path would most plausibly have crept in.

## Required changes

None.

## Optional improvements

Non-blocking, offered only as observations; none of these affects the verdict.

- The `motivating_context` field runs six lines. The format sets no length bound
  for that field (the 2-4 line guidance applies to `problem` and `outcome`), so
  this is within contract. If the author wants tighter symmetry with the two
  bounded fields, the codex-cli version detail is the most compressible part.
- When the brief moves Draft -> Accepted, the Open Questions section has to be
  emptied or removed, and the format's canonical closure surface is the
  downstream PRD's Decisions and Trade-offs section. Worth carrying both
  questions forward explicitly rather than dropping them at transition time.
