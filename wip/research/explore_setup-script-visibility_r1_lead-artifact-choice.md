# Lead: Which durable artifact should record the outcome — an ADR for the exit-code decision, an amendment to the existing design doc, or both? And what does the repo's doc lifecycle mechanically require?

## Findings

### 1. docs/ inventory — there is no decisions/ directory

`find docs -type d` in the niwa repo returns exactly six directories:

```
docs/briefs      docs/designs/archive   docs/designs/current
docs/guides      docs/prds              docs/spikes
```

There is **no `docs/decisions/`**, and zero files matching `ADR-*` or
`DECISION-*` anywhere in the tree. File counts: 17 briefs, 9 archived designs,
46 current designs, 12 guides, 29 PRDs, 6 spikes.

This is the single most decisive finding for the lead. Writing an ADR into niwa
would be creating a new artifact class in a repo that has produced 119 durable
docs without one.

### 2. The four upstream-facing artifact types and their frontmatter schema

The frontmatter contract is not niwa-local — it is owned by the shirabe
validator, `crates/shirabe-validate/src/formats.rs`. Eight formats are
declared, matched by **filename prefix** (`detect_format`, longest-prefix
match). The Design profile, verbatim from `formats.rs`:

```rust
FormatSpec {
    name: "Design".to_string(),
    prefix: "DESIGN-".to_string(),
    schema_version: "design/v1".to_string(),
    required_fields: s(&["status", "problem", "decision", "rationale"]),
    valid_statuses: s(&["Proposed", "Accepted", "Planned", "Current", "Superseded"]),
    required_sections: s(&[
        "Status", "Context and Problem Statement", "Decision Drivers",
        "Considered Options", "Decision Outcome", "Solution Architecture",
        "Implementation Approach", "Security Considerations", "Consequences",
    ]),
    ...
}
```

**The eight formats are Comp, Design, PRD, VISION, Roadmap, Plan, Strategy,
Brief.** There is no `Decision` / `ADR` format. `detect_format("ADR-foo.md")`
returns `None`. An ADR committed to this repo would be an unrecognized file that
no validator inspects — no frontmatter check, no section check, no lifecycle
participation. It is not *forbidden*; it is simply unbacked.

Representative frontmatter, read in full:

`docs/designs/current/DESIGN-post-clone-scripts.md:1-22` — four fields, no
`schema:`, no `upstream:`:

```yaml
---
status: Current
problem: |
  niwa clones repos during create/apply but can't run repo-provided setup scripts
  afterward. ...
decision: |
  Scan a configurable setup directory (default: scripts/setup/) in each repo for
  executable scripts and run them in lexical order. ... Non-zero exit codes
  produce warnings, not fatal errors.
rationale: |
  A directory convention is more extensible than a single file ...
---
```

`docs/designs/current/DESIGN-niwa-mesh-removal.md:1-26` — same four fields plus
`upstream: docs/prds/PRD-niwa-mesh-removal.md` as the last key.

`docs/designs/current/DESIGN-workspace-config-sources.md:1-45` — adds
`upstream:` as the *first* key. Note its `decision:` block was edited in place
to carry the amendment inline: "…kept at `<workspace>/.niwa/instance.json` …
(per 2026-04-23 amendment; original spec relocated to `.niwa-state/`)".

### 3. The `status: Current` / `## Status: Proposed` split is systemic, not unique

Scanned all 46 designs in `docs/designs/current/`. Every one has frontmatter
`status: Current`. The body `## Status` section disagrees in **11 of 46**:

| Body `## Status` value | Count | Examples |
|---|---|---|
| `Current` | 32 | claude-key-consolidation, mesh-removal, keep-alive |
| `Proposed` | 8 | **post-clone-scripts**, config-distribution, explicit-repos, file-distribution, init-command, instance-lifecycle, plugin-installation, settings-env, workspace-root-claude |
| `Accepted` | 3 | pull-managed-repos, workspace-config, workspace-config-sources |

So DESIGN-post-clone-scripts.md is not an outlier. It belongs to a cohort of
older docs promoted into `designs/current/` (which sets frontmatter status)
without the body section being touched. The newer docs — everything from
mesh-removal forward — get both right.

**Why this matters for CI:** see §4. The mismatch is currently invisible to the
validator on this specific file, and that is load-bearing.

### 4. The CI gates — exactly what a doc edit must satisfy

`.github/workflows/` holds eight workflows. Three are doc/PR gates, all of them
thin callers into `tsukumogami/shirabe` reusable workflows pinned at `@main`:

| Workflow | Trigger | Calls |
|---|---|---|
| `lifecycle.yml` | `pull_request` (opened, synchronize, reopened, ready_for_review, converted_to_draft) — **no paths filter** | `tsukumogami/shirabe/.github/workflows/lifecycle.yml@main` |
| `validate-docs.yml` | `pull_request` with `paths: ['docs/**']` | `tsukumogami/shirabe/.github/workflows/validate-docs.yml@main` |
| `validate-pr-body.yml` | `pull_request` (opened, edited, reopened, synchronize, ready_for_review) — **no paths filter, by design** | `tsukumogami/shirabe/.github/workflows/pr-body.yml@main` |

`test.yml` is Go-only and has a `paths:` filter on `**/*.go`, `go.mod`,
`go.sum`, `Makefile`, `test/functional/**`, `.github/workflows/test.yml`,
`.tsuku-recipes/**` — a docs-only change does not run it, but our change is
docs+code so it will.

#### 4a. `validate-docs` — the schema gate short-circuits, and this is the key fact

Per-file, changed-files-only. The very first thing it does
(`crates/shirabe-validate/src/validate.rs:184-186`):

```rust
// 1. Schema gate: if doc.schema != spec.schema_version, return SCHEMA notice.
if let Some(schema_err) = check_schema(doc, spec) {
    return vec![schema_err];
}
```

`check_schema` (`checks.rs:48-58`) returns a `SCHEMA` finding when the doc's
`schema:` field is absent or mismatched, with the message `"schema field
missing, skipping"`. `SCHEMA` is registered in `is_notice`
(`validate.rs:87-96`), so it is a **notice, not an error** — and because the
gate `return`s, *no FC check runs at all* on a doc without `schema:`.

Only **6 of 46** current designs carry `schema: design/v1`:
dispatch-paste-prompt, niwa-plugin-record-lifecycle, ephemeral-session-instances,
niwa-default-worktree, watch-operator-approval, remote-control-by-default.

**DESIGN-post-clone-scripts.md is not one of them.** Editing it today is
CI-invisible to the format validator.

The checks that *would* fire if `schema: design/v1` were added:

- **FC01** — missing required field. Would pass: all four of status/problem/
  decision/rationale are present.
- **FC02** — status in the valid enum. Would pass: `Current` is valid.
- **FC03** — frontmatter status must match the `## Status` body. **Would FAIL.**
  `checks.rs:120-166` finds `## Status`, reads the next non-blank line, stops at
  the next heading, and compares case-insensitively. The doc's first non-blank
  line under `## Status` is `Proposed`; frontmatter says `Current`. FC03 is
  **not** in `is_notice` — it is a hard error.
- **FC04** — all nine required sections present. Would pass; I checked
  (`grep -n '^## '` gives exactly the nine, in canonical order at lines 24, 28,
  44, 53, 166, 178, 255, 273, 289).
- **FC15** (order) — notice-level anyway, and would pass.

So: **adding `schema: design/v1` is safe if and only if the `## Status` body is
simultaneously corrected to `Current`.** The two edits are a package.

#### 4b. `lifecycle` — whole-tree, chain-aware, and post-clone-scripts is exempt

`lifecycle.yml`'s own comment states it runs `shirabe validate --lifecycle .`
against the whole tree, in draft posture for DRAFT PRs and ready posture for
READY PRs. Codes L01–L07 (`lifecycle.rs:14-37`). The two that could touch us:

- **L02 (orphan)** — `check_orphan` (`lifecycle.rs:676-694`) exits early for a
  doc at its terminal state: `TargetState::Status("Current")` for a Design, and
  post-clone-scripts is at `Current`. It has no `upstream:` and no downstream
  child, so it *is* an orphan — but a terminal one, which passes. Amending it
  does not change its status, so it stays exempt.
- **L07** — a `Current` design must live in `docs/designs/current/`. It does.
  Do not move the file.

The practical rule: **do not change the design's frontmatter `status:` away from
`Current`.** Dropping it to `Proposed` or `Accepted` to signal "under revision"
would fire L07 (wrong directory for a non-Current design) *and* L02 (non-terminal
orphan with no Active ROADMAP and no chain linkage). Amend in place at Current.

#### 4c. `validate-pr-body` — four mechanical checks, path-independent

Single-sourced in shirabe's `references/pr-body-conformance.md`; implementation
of record is `crates/shirabe-validate/src/pr_body.rs`. Four gated checks:

- **PB1 — Conventional Commits title.** `<type>[scope][!]: <description>`, type
  from `feat|fix|docs|style|refactor|perf|test|chore|ci|build|revert`, non-empty
  description. **An issue-number scope is rejected** — `docs(issue-8):`,
  `chore(#8):`, `fix(8):` all fail, pinned to `^(issue[-_]?)?#?\d+$`
  case-insensitive. Put `Fixes #N` in Part 2.
- **PB2 — exactly one top-level bare `---` separator, non-empty Part 1.** Zero,
  two, or an empty Part 1 all fail. Use `***` or `___` for a rule inside Part 2.
- **PB3 — no AI-attribution footer.** No `Co-Authored-By:` naming an AI, no
  "Generated with Claude Code".
- **PB4 — no ATX markdown heading in Part 1.** A `## Root cause` / `## Fix`
  above the separator fails. Part 2 may use headings freely. `#123`, `#!/bin/sh`,
  `#include`, and 7+ `#` are not matched.

PB2/PB3/PB4 scan with fenced code blocks stripped. Everything else — which Part 2
sections a change needs, wording, whether Part 1 names an issue — is explicitly
advisory and ungated. A docs-only PR with a one-line Part 1 and a Part 2 that is
just `Fixes #N` passes.

There is **no PR template** in this repo — `.github/` contains only
`workflows/`. The template lives in the shirabe/tsukumogami skill layer, not on
disk here.

#### 4d. The wip/ grep check — it does not exist in this repo's CI

`grep -rn "wip" .github/` returns **nothing**. The workspace CLAUDE.md's claim
that "Public-repo CI also runs a grep-based check on every PR" is **not true for
niwa**. The `shirabe-validate` crate contains no `wip/` string either
(`grep -rn 'wip/' crates/shirabe-validate/src/*.rs` → empty).

Enforcement is entirely skill-side, per shirabe's `references/wip-hygiene.md`:
phase scripts hard-stop, and `skills/design` Phase 0 step 0.4a / Phase 6 step
6.4, `skills/plan` Phase 7 step 7.4b, and `skills/prd` Phase 3 step 3.1 each run
a grep. The reference gives the exact verification commands to run manually
before opening the PR:

```bash
git grep -nE 'wip/' -- 'docs/**/*.md'
git ls-files 'docs/**/*.md' | xargs -I{} head -20 {} | grep -nE '^(upstream|source|references):.*wip/'
```

Both must be empty (rule-statement prose is allowed; path-shaped references are
not). Since nothing in CI catches this, it is on us: the design-doc edit must
carry **no** `wip/...` path, and every `wip/explore_setup-script-visibility_*`
and `wip/research/*` file must be deleted before the PR is marked ready.

### 5. Precedent for revising a Current design: amend in place, always

Nine docs in `docs/` contain amendment language. Seven are current designs. Not
one supersession, not one ADR, not one archived-and-replaced design. The
mechanism varies slightly but the shape is stable — a dated section plus a
pointer from `## Status`:

**Pattern A — dedicated `## Amendments` section right after Status.**
`DESIGN-workspace-config-sources.md:46-93`. Status body reads
`Accepted (2026-04-23 Amendment in effect)`, followed by `## Amendments` with a
`### 2026-04-23 — Instance state stays in .niwa/` entry structured as four bold
labels: **What changed.** / **Why.** / **Affected sections.** / **What didn't
change.** The frontmatter `decision:` block was also edited to carry the new
outcome with a parenthetical pointing at the amendment. Note `## Amendments` is
not a required section, so it can sit between Status and Context without
tripping FC15.

**Pattern B — Status pointer plus a trailing `## Amendment <date>: <title>`.**
`DESIGN-niwa-plugin-record-lifecycle.md:32-38` and `:461`. Status body:

```
## Status

Current

Amended 2026-06-20 — see the "Amendment: global marketplace-registry
reconciliation" section at the end, added after a post-implementation
gap surfaced (R18).
```

with `## Amendment 2026-06-20: global marketplace-registry reconciliation` at
line 461 opening with a bold **Gap.** paragraph. This doc carries
`schema: design/v1` — proof the pattern is validator-clean.

**Pattern C — trailing `## Addendum (<date>): <finding>`, no Status edit.**
`DESIGN-niwa-session-keep-alive.md`, commit `783c3f8` "docs(keep-alive): record
re-investigation of programmatic cron seam", +52 lines, doc-only, dated
2026-08-05 — three days ago, the most recent precedent in the repo. This is the
closest structural analogue to our situation: a *re-examined decision that was
re-affirmed*. Its outcome paragraph is the template:

> **Outcome.** Decision B stands: arming remains necessarily agent-mediated, and
> B2 (task-prompt augmentation) remains the shipped channel. The
> re-investigation did surface one concrete, scoped follow-up, filed separately
> rather than folded into this design…

**Pattern D — inline bold amendment inside Status.**
`DESIGN-niwa-watch-once-pr-review.md:59` — `**Amendment (containment model,
sandbox capability, and provisioning).**` as a paragraph in the Status section,
with an italic note at the affected decision section (`:450-456`) explaining
that the earlier framing "was superseded".

In every schema-bearing case (B, D, and dispatch-paste-prompt) the **first
non-blank line under `## Status` is the bare status word**, with amendment prose
after a blank line. FC03 reads only that first line, so this is a mechanical
requirement, not a stylistic one.

`DESIGN-niwa-destroy.md:650` also shows amendments being *planned* as work:
"### Phase A: doc-only PRD/design amendments (parallel, low-risk)", listing
which PRD requirement IDs and design decisions get touched.

### 6. shirabe's own conventions do not supply an amendment procedure

`/home/dgazineu/.claude/plugins/cache/shirabe/shirabe/0.15.1-dev/skills/design/`
contains `SKILL.md`, `references/`, `evals/`, `team.yaml`.
`grep -rn -i "amend"` over the whole design skill returns **nothing**. The
`/design` skill has no concept of amending a Current design — it authors new
designs. The amendment convention is a niwa-repo practice, not a shirabe one,
which is why it varies across the four patterns above.

Shirabe *does* keep decision records for itself, at
`docs/decisions/DECISION-<slug>-<YYYY-MM-DD>.md` (five of them, all dated
2026-06-06, e.g. `DECISION-orphan-doc-passing-state-rule-2026-06-06.md`,
referenced from `lifecycle.rs:25` and `:39`). Note the prefix is `DECISION-`,
not `ADR-`, and the date is in the filename — a third convention.

The workspace-level `tsukumogami:decision-record` skill
(`~/.claude/plugins/cache/tsukumogami/tsukumogami/0.1.0/skills/decision-record/SKILL.md`)
describes a *fourth*: `docs/decisions/ADR-<name>.md`, frontmatter
`status`/`decision`/`rationale` (+`superseded_by` when Superseded), lifecycle
`Proposed → Accepted → Deprecated|Superseded`, five required sections (Status,
Context, Decision, Options Considered, Consequences), 1-2 pages. Its Content
Boundaries section is directly on point:

> If you have multiple related architectural decisions, write a design doc. If
> you have one decision between known options, write an ADR.

and

> **A design doc**: … If you have one clear choice to make, write an ADR.

That skill's ADR shape is validated by nothing — no shirabe format matches
`ADR-`, and its "Validation Rules" section describes checks performed *during
`/shirabe:explore` drafting*, i.e. by an agent, not by CI.

### 7. What is actually at stake in DESIGN-post-clone-scripts.md

Decision 2 (`:121-149`) is 29 lines. The two lines the implementation
contradicts are `:132` and `:134-135`:

```
- Stdout/stderr: printed to niwa's output, prefixed with the repo name
- Exit code 0: success
- Exit code non-zero: warning printed, **remaining scripts for that repo are
  skipped**, pipeline continues with next repo
```

followed by a sample-output fence (`:137-144`) showing the per-repo and
per-script progress lines, and the **Why stop-on-error per repo** rationale
(`:146-149`) that the exploration is explicitly *not* reversing. The document is
309 lines with all nine required sections.

## Implications

1. **Amend the design doc in place. Do not write an ADR.** The evidence is
   one-directional: no `docs/decisions/` exists, no ADR has ever been written
   here, the validator has no `ADR-`/`Decision` format so the file would be
   inert, and there are four competing ADR-naming conventions in the surrounding
   tooling (`ADR-<name>.md` from the tsukumogami skill, `DECISION-<slug>-<date>.md`
   in shirabe's own repo). Picking one would be inventing a convention as a side
   effect of a bugfix PR. Meanwhile the repo has amended a Current design seven
   times, most recently three days ago, for exactly this situation.

2. **The exit-code decision is not a separate decision — it is a consequence of
   Decision 2 that Decision 2 failed to state.** The tsukumogami skill's own
   boundary rule ("multiple related architectural decisions → design doc") points
   the same way: routing, progress lines, and discoverability are three coupled
   choices in one subsystem, all under Decision 2's existing scope.

3. **Use Pattern B, the schema-bearing variant.** `## Status` gets `Current` on
   its own first non-blank line, then an `Amended <date> — see …` paragraph; a
   `## Amendment <date>: setup-script output and failure visibility` section goes
   at the end of the doc. `DESIGN-niwa-plugin-record-lifecycle.md` is the working
   model and it validates clean. Borrow Pattern A's four bold labels (**What
   changed** / **Why** / **Affected sections** / **What didn't change**) for the
   body — "What didn't change" is where "Decision 2's warn-on-failure choice for
   cross-repo resilience stands" belongs, which is precisely the thing the
   exploration must not appear to be reversing.

4. **Amend Decision 2's bullets in place too, not only in a trailing section.**
   The failure mode being corrected is a reader trusting `:132` and `:134-135`.
   A trailing amendment alone leaves those lines standing. Follow
   `DESIGN-niwa-watch-once-pr-review.md:450-456`: an italic note at the affected
   subsection saying the earlier framing was superseded and pointing at the
   amendment. Also update the Security Considerations claim that niwa "prints
   each script name before execution" — the exploration scope flags it as false.

5. **Decide deliberately on `schema: design/v1`.** Adding it is a small quality
   win (turns on FC01–FC15 for this file forever) and forces the
   Proposed→Current body fix, which the doc needs anyway. But it is scope beyond
   the issue, and if the amendment ships without it the file simply keeps
   emitting the pre-existing SCHEMA notice. **Recommendation: add it, and fix
   `## Status` to `Current` in the same edit.** The two together are four lines,
   the FC04 section requirement is already satisfied, and leaving a doc we are
   actively editing in the unvalidated cohort is how the next drift goes
   unnoticed. If reviewers want it scoped out, drop the `schema:` line and fix
   `## Status` anyway — it is a correctness fix independent of validation.

6. **Frontmatter `decision:`/`rationale:` blocks must be updated.** The current
   `decision:` says "Non-zero exit codes produce warnings, not fatal errors" —
   true and staying true, but silent on output routing and discoverability.
   `DESIGN-workspace-config-sources.md` set the precedent of editing the
   `decision:` block with a parenthetical pointing at the amendment. Frontmatter
   is what an agent regex-greps; leaving it stale reproduces the original defect
   at a different layer.

7. **PR mechanics, so it passes first run.** Title: Conventional Commits, no
   issue-number scope — `fix(apply): surface setup-script output and failed-script
   status` works; `fix(issue-239): …` fails PB1. Body: exactly one bare `---` at
   top level, prose Part 1 with **no `##` headings**, `Fixes #239` in Part 2.
   No `Co-Authored-By` / "Generated with Claude Code". Before marking ready:
   delete every `wip/` file and run the two greps from §4d, because **nothing in
   CI will catch a leftover `wip/` reference in this repo**.

8. **Do not touch the design's frontmatter `status:` or its path.** Keep
   `Current` in `docs/designs/current/` or L07 and L02 fire.

## Surprises

- **The workspace CLAUDE.md is wrong about wip/ CI enforcement for this repo.**
  It states public-repo CI runs a grep-based `wip/` check on every PR. niwa's
  `.github/` contains no such check, and the shirabe validator crate has no
  `wip/` string. The rule is real and skill-enforced; the CI backstop is not.

- **The schema gate means most of this repo's design docs are unvalidated.** 40
  of 46 current designs carry no `schema:` field, so `validate-docs` returns a
  single "skipping" notice and never checks their frontmatter, statuses, or
  sections. The 11 Proposed/Accepted-vs-Current body mismatches survive for
  exactly this reason. This is by design (a migration seam), but it means "CI is
  green" says much less about a design doc here than one would assume.

- **FC03 is a trap on the exact file we are editing.** Adding one apparently
  cosmetic frontmatter line (`schema: design/v1`) to DESIGN-post-clone-scripts.md
  turns its long-standing Proposed/Current mismatch from invisible into a hard CI
  error. Worth knowing before someone "tidies up" the frontmatter mid-review.

- **shirabe's `/design` skill has no amendment concept at all** — zero matches
  for "amend". Every amendment in this repo was hand-rolled, which explains the
  four distinct patterns.

- **Four different decision-record conventions exist in the surrounding
  tooling** (`ADR-<name>.md` per the tsukumogami skill, `DECISION-<slug>-<date>.md`
  in shirabe's repo, plus shirabe's `skills/scope/references/decision-record-*`
  rejection/re-evaluation templates), and niwa has adopted none of them. Any ADR
  written here would be picking a winner by accident.

## Open Questions

- Should the amendment carry a date in its heading? Precedent is split:
  plugin-record-lifecycle and workspace-config-sources date it, keep-alive dates
  the Addendum, dispatch-paste-prompt and watch-once-pr-review do not. Dating it
  is more useful and matches the two most structured examples — suggest
  `## Amendment 2026-08-08: …` unless the PR lands on a different day.

- Is `docs/guides/` in scope? There is no post-clone-scripts guide today. If the
  fix introduces an operator-visible knob (a `--strict`-style flag or a log-file
  location), the CLAUDE.md "Contributor Guides" list is the place users look, and
  a guide entry may be warranted. Out of this lead's scope but adjacent.

- Does `DESIGN-clone-output-ux.md` overlap? It is a Current design about clone
  output that I did not read in full. If it owns the apply pipeline's output
  contract, the amendment may need a cross-reference or the change may belong
  partly there. Worth a check before drafting.

- Whether to add `schema: design/v1` is ultimately the author's call; §Implications
  item 5 recommends yes but the amendment is CI-valid either way.

## Summary

The repo has no `docs/decisions/` directory, has never written an ADR, and the
shirabe validator has no `ADR-`/`Decision` format — so an ADR here would be an
inert file establishing a new convention as a side effect of a bugfix, while the
repo has amended a Current design in place seven times, most recently three days
ago for a re-affirmed-after-re-investigation decision that mirrors this one
exactly. Amend `DESIGN-post-clone-scripts.md` in place using the
`DESIGN-niwa-plugin-record-lifecycle.md` pattern: keep frontmatter `status:
Current` and the file in `docs/designs/current/` (L07/L02), put the bare word
`Current` as the first non-blank line under `## Status` followed by an
`Amended <date> — see …` pointer, add a trailing `## Amendment <date>: …`
section, correct Decision 2's stdout/stderr and Security Considerations claims
in place, and update the frontmatter `decision:` block. Mechanically: the doc
carries no `schema:` field so `validate-docs` currently skips every format check
on it — adding `schema: design/v1` is recommended but only together with fixing
the `## Status` body from `Proposed` to `Current`, or FC03 fails hard; the PR
needs a Conventional Commits title with no issue-number scope, exactly one bare
`---` with a heading-free prose Part 1, and a manual `git grep -nE 'wip/' --
'docs/**/*.md'` before merge because this repo's CI has no wip/ check at all.
