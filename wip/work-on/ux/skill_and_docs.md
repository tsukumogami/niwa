# Audit: niwa-mesh skill text and `docs/guides/sessions.md`

Scope: assess tone, structure, completeness, and consistency of the
generated `niwa-mesh` SKILL.md and the sessions guide against peer
shirabe skills and peer niwa guides. All citations are file-relative
to the repo root unless absolute.

---

## 1. niwa-mesh skill assessment

**Source of truth:** `internal/workspace/channels.go:682-833`
(`buildSkillContent`).

**Rendered output (canonical):**
`/home/dgazineu/dev/niwaw/tsuku/tsuku-3/.claude/skills/niwa-mesh/SKILL.md`
(byte-identical to the per-repo copies; see
`wip/research/explore_niwa-mesh-reliability_r1_lead-mesh-skill-contract.md:7-26`).

### Verdict

The skill text is **complete on coverage** but **inconsistent in structure
and density** with peer agent-facing skills. It also carries
contract drift the design doc separately commits to fixing in Phase 6.
Beyond the design's existing Phase 6 deliverables, three structural
fixes deserve their own AC lines.

### Issue 1.1 — Frontmatter description is far too long

The `description` field at SKILL.md:3-15 is ~870 characters of prose:

> "Delegate tasks across niwa workspace roles. Use when the user asks to
> dispatch work to another agent... Invoke niwa MCP tools rather than
> writing directly to the filesystem; the tool surface enforces
> authorization and keeps the task state machine consistent across
> restarts."

Peer skills (which Claude Code reads from the same `description` slot to
decide loading) are dramatically shorter and trigger-list shaped. Compare:

- `public/shirabe/skills/release/SKILL.md:3-7` — 3 sentences, no
  meta-explanation.
- `public/shirabe/skills/decision/SKILL.md:3-11` — 8 lines, with explicit
  trigger phrases ("decide between X and Y", "which approach should we
  use for Z").
- `public/shirabe/skills/plan/SKILL.md:3-9` — explicit imperative trigger
  phrases ("break this into issues", "plan the implementation").

The niwa-mesh description spends its second half describing what the
skill body covers ("how to delegate synchronously vs asynchronously, how
to report progress during long-running work..."), which is redundant —
once the skill is loaded, those sections are right there.

The generator comment at `channels.go:687-689` even acknowledges the
length is at the cap: "intentionally under ~800 chars so that name +
description + allowed-tools fit comfortably within Claude Code's
skillFrontmatterCharLimit". Hitting the cap is a smell, not a target.

**Proposed alternative:** trim to 4-5 lines following peer pattern:

> "Delegate work to other niwa workspace roles, track and answer
> in-flight tasks, and exchange messages with peer agents. Use when the
> user asks to dispatch work, check task status, report progress,
> finish a task, or ask another role a question. Invoke niwa MCP tools
> rather than writing directly to the filesystem."

### Issue 1.2 — Body opening is one sentence, no orientation

SKILL.md:33-35:

```
# niwa-mesh

Behavioral defaults for agents participating in the niwa mesh.
```

Peer skills give the agent a mental model first. `decision/SKILL.md:18-23`
opens with a 2-3 line statement of what the skill produces and how it
fits the wider toolchain ("produces decision reports that map directly
to design doc Considered Options sections..."). `design/SKILL.md:16-20`
does the same ("Design documents capture HOW to build something --
they complement PRDs..."). The niwa-mesh skill drops the agent into
"Delegation (sync vs async)" with no anchor.

**Proposed alternative:** 3-4 lines covering: "the niwa mesh is a task
queue across repos in this workspace; tasks are first-class with a
queued/running/terminal lifecycle; you talk to it through `niwa_*` MCP
tools; do not touch `.niwa/` on disk".

### Issue 1.3 — Section headings inconsistent with peer skill voice

niwa-mesh uses noun-phrase headings:

- `## Delegation (sync vs async)` (SKILL.md:37)
- `## Reporting Progress` (SKILL.md:57)
- `## Completion Contract` (SKILL.md:67)
- `## Message Vocabulary` (SKILL.md:79)
- `## Peer Interaction` (SKILL.md:91)
- `## Common Patterns` (SKILL.md:110)
- `## Session Management` (SKILL.md:142)

Peer agent-facing skills use **imperative or task-named** headings:
`design/SKILL.md` has `## Structure`, `### Required Sections`;
`plan/SKILL.md:25` has `## PLAN Doc Structure` then
`## Decomposition Strategies`; `release/SKILL.md:27` has `## Phases`
with `### Phase 1: Version Analysis`. The pattern across shirabe is
"either a noun describing an artifact, or a numbered phase".

niwa-mesh's noun-phrase headings work but mix two registers
("Delegation" is a domain noun; "Reporting Progress" is a verb-form;
"Completion Contract" is jargon). Standardize on either *what the agent
does* (`## Delegating work`, `## Reporting progress`,
`## Finishing a task`) or *what concept this section covers*
(`## The delegation tool`, `## The progress contract`).

The Title Case heading style (`## Reporting Progress`,
`## Completion Contract`, `## Message Vocabulary`) also drifts from
the workspace writing-style guidance at
`public/shirabe/skills/writing-style/SKILL.md:42` (formatting tells
table) which lists "Title Case Headings" as a tell to fix to sentence
case. Peer guides in niwa already use sentence case
(`docs/guides/sessions.md:8` "What a session is",
`:27` "Lifecycle", `:55` "Filesystem layout").

### Issue 1.4 — Wall-of-text paragraphs vs scannable lists

The "Delegation" section (SKILL.md:39-55) is a single 17-line paragraph
that buries five distinct facts:

1. Sync mode blocks until terminal.
2. Async returns `{task_id}`.
3. Async preferred for fan-out.
4. Body must be self-contained (worker uses `niwa_check_messages`).
5. session_id required, exception for `read_only: true`.
6. Error codes: `SESSION_REQUIRED`, `SESSION_INACTIVE`, `SESSION_NOT_FOUND`.

Peer skills convert lists like this into bullets or sub-headings. See
`design/SKILL.md:47-58` which decomposes "optional fields" into bullet
items, or `plan/SKILL.md:30-38` which numbers required sections. An
agent skimming for "what error code do I get if I forget session_id?"
has to read the whole paragraph in the niwa-mesh version.

The "Common Patterns" section (SKILL.md:110-140) is the worst case:
five distinct patterns (fan-out, worker-asks-coordinator,
cancel/update, long-running timeouts, question-handling loop) packed
into one paragraph with no sub-headings. Each should be its own
`### Pattern: ...` block, matching how `release/SKILL.md:27-83` breaks
phases into `### Phase N: ...` blocks.

### Issue 1.5 — Stale contract: `question.ask`, `question.answer`, `status.update`

SKILL.md:84-85:

> "Agent-level peer exchanges use `question.ask`, `question.answer`,
> and `status.update`."

Per `wip/research/explore_niwa-mesh-reliability_r1_lead-mesh-skill-contract.md`
and Phase 6 of `DESIGN-niwa-mesh-reliability.md:1334-1344`, these are
dead types — the runtime emits `task.ask`, `task.delegate`,
`task.progress`, `task.completed`, `task.abandoned`, `task.cancelled`.
This is one of the audited drift items the design already plans to fix.
Worth a verbatim quote in the AC list.

### Issue 1.6 — `Coordinator question-handling loop` has a code block in the middle of a paragraph

SKILL.md:131-140:

```
Coordinator question-handling loop: while blocking on
`niwa_await_task`, a `status:"question_pending"` result means a
worker has a question. Answer with `niwa_finish_task(task_id=
ask_task_id, outcome="completed", result=...)`, then re-call
`niwa_await_task(task_id=<original_task_id>)`. Repeat until the
result is terminal or a timeout:
  result = niwa_await_task(task_id=...)
  while result.status == "question_pending":
    niwa_finish_task(task_id=result.ask_task_id, outcome="completed", result=...)
    result = niwa_await_task(task_id=<same task_id>)
```

The two-space indent is a hack that may render as an indented paragraph
or as code depending on the renderer. Wrap it in a fenced block (the
generator at `channels.go:810-813` writes the indented form with no
fence). Also: this exact pseudocode is duplicated in
`docs/guides/cross-session-communication.md:507-515` (see §3 below).

### Issue 1.7 — Allowed-tools list does not include `niwa_query_task`

`channels.go:708-710` iterates `niwaMCPToolNames`. The rendered
`allowed-tools` block at SKILL.md:16-30 includes `niwa_query_task`
(line 18), which is fine. **But** the body refers to `niwa_query_task`
only once (SKILL.md:43-44, in the Delegation paragraph) and never
explains *when to use it instead of `niwa_await_task`*. A peer skill
would have a `### Polling vs blocking` discriminator note. Worth
adding to Issue 8 ACs.

### Issue 1.8 — No "When NOT to use this skill" section

Peer skills declare boundaries:
- `explore/SKILL.md:9-10` — "Does NOT apply when the user already
  knows their artifact type -- use /prd, /design, or /plan directly".
- `design/SKILL.md:6-9` — "Do NOT use for quick opinions without a
  formal document...".
- `decision/SKILL.md:9-11` — "Do NOT use..." pattern.

niwa-mesh has no anti-pattern section. An agent could plausibly
invoke `niwa_delegate` for work that fits in-context, when it should
just do the work itself. A short "When NOT to use the mesh" block
would help: small one-shot edits, work where the user is in-loop and
synchronous, anything that requires interactive permission prompts
(workers run `--permission-mode=acceptEdits`).

---

## 2. `docs/guides/sessions.md` assessment

### Verdict

The guide is **structurally well-organized** and matches peer niwa
guide conventions (sentence-case headings, "What you get" + Quick
start + reference layout). It's already the strongest guide in the
folder for tone consistency. Three issues stand out: a minor style
inconsistency, a content gap that Phase 6 plans to fill, and a
duplicate section that overlaps with `cross-session-communication.md`.

### Issue 2.1 — Opening uses an em dash; later sections use plain prose

`sessions.md:1-7`:

> "A session gives a worker agent persistent Claude conversation
> context and an isolated git worktree. Instead of each delegated
> task starting a fresh Claude process in the shared repo clone,
> tasks delegated to a session all run in the same worktree and pick
> up the same conversation history."

Compare `vault-integration.md:1-6` and `workspace-config-sources.md:1-7`,
which both follow a "## What you get" bulleted opener with concrete
benefits. `sessions.md` has prose where peers have bulleted hooks.
Not wrong, but inconsistent. The "Em dash overuse" tell from
`writing-style/SKILL.md:43` applies — `sessions.md:24` ("for example,
to hand off a multi-step implementation task — context continuity..."
appears at line 142) and several other "—" instances exist.

Quick scan for em dashes in `sessions.md`: lines 7, 24, 102, 142, 159,
236, 277. Some carry meaning, several could be commas or colons.
This is a small content-style cleanup, not a structural issue.

### Issue 2.2 — `niwa session list` warning is buried and contradictory

`sessions.md:167-179`:

> "Lists lifecycle sessions with optional filters. Without flags,
> this command falls back to `niwa mesh list` (a deprecated alias)
> and prints a warning; use `niwa mesh list` directly for that view."

This contradicts the surrounding context which positions
`niwa session list` as the canonical session-listing tool. Reading
top-down, the user has just learned that sessions are lifecycle
objects with state files; then they're told the bare `list` command
silently does something different and is deprecated. Either:

- Clarify that the deprecation is *only* for the bare-command form
  (no `--repo` or `--status` flag) — and clean up that legacy in code
  too.
- Or: lift the deprecation note into a `> **Deprecation**` callout at
  the top of the section so it's not mid-paragraph noise.

### Issue 2.3 — No coverage of the new `daemon` sub-object (Phase 6 gap)

Per `DESIGN-niwa-mesh-reliability.md:1217`:

> "`docs/guides/sessions.md` updated with both surfaces."

and the more specific deliverable at `:953-956` and `:1339-1341`:

> "Add a section on the `daemon` sub-object returned by
> `niwa_list_sessions` (parallel to existing `daemon_warning` docs at
> ~222-225)."

Verified: `sessions.md:240-251` documents `niwa_list_sessions`
without the `{alive, pid, started_at}` daemon enrichment, because
that's the new behavior the design adds. The current gap exists today
and Phase 6 plans to fill it. Good — no extra AC needed beyond what
Phase 6 already names.

### Issue 2.4 — No coverage of `taskstore_lost` recovery (Phase 6 gap)

Same as 2.3 — the design at `:957-959` adds a `taskstore_lost` recovery
section. Currently the guide's "When the session daemon crashes"
section (`sessions.md:258-281`) handles daemon crashes but not
the related task-store-lost case. Phase 6 covers this; no extra AC
needed.

### Issue 2.5 — No coverage of worker config inheritance (Phase 6 gap)

Per design `:962-964`:

> "Add a section that documents the worker config inheritance
> contract (what workers inherit, what is scoped via
> `--strict-mcp-config`, where to look for divergence)."

This is a substantial new section. Worth making the AC explicit:
the section should land in `sessions.md` adjacent to the existing
"When the session daemon crashes" subsection (since both are
operational), and it should include a worked example matching the
named-skill availability checklist from
`DESIGN-niwa-mesh-reliability.md:1246-1268`.

### Issue 2.6 — "Contributor notes" section mixes two audiences

`sessions.md:303-324` covers contributor-only details: schema
versioning, ID validation regex, the
`SessionLifecycleState` vs `SessionEntry` distinction. This is
useful but it's mixed in with operator-facing prose. Peer guides
either separate this (`workspace-config-sources.md` has no such
section) or move it to a separate doc. Suggestion: move
`## Contributor notes` to a sibling
`docs/guides/sessions-internals.md` or to a comment block at the top
of `internal/mcp/session_lifecycle.go`. Standalone fix; doesn't need
to be in Phase 6.

---

## 3. Cross-doc duplication map

| Topic | niwa-mesh SKILL.md | docs/guides/sessions.md | docs/guides/cross-session-communication.md | Drift risk |
|-------|--------------------|--------------------------|---------------------------------------------|------------|
| `niwa_delegate` `mode=sync\|async` semantics | :39-55 | (mentions at :253-256) | :63-77 | **High** — three rewrites, same fact. SKILL says "blocks until... finishes, is abandoned, or is cancelled"; CSC says "blocks until the task reaches a terminal state". Same idea, different phrasing. |
| `session_id` required + exception for `read_only` | :49-55 | :253-256 | (not mentioned) | Medium — if the rule changes, both update points need touching. |
| `niwa_finish_task` outcome contract | :67-77 | (not covered) | :119-134 | Medium — SKILL gives the agent rule; CSC gives the wire-format rule. Drift risk on error codes (`TASK_ALREADY_TERMINAL` listed in both). |
| `task.*` message vocabulary | :79-89 | (not covered) | (implicit at :443-457) | Low — but Issue 1.5 above shows the SKILL is currently wrong while CSC is right. |
| Coordinator question-handling loop pseudocode | :131-140 | (not covered) | :507-515 | **Very high** — verbatim duplicate of the same five-line pseudocode in two places. Either trim the SKILL block to 1 line + link to CSC, or fold CSC's longer worked example into a `references/` file the SKILL points at. |
| `niwa_await_task` 600s default + re-await loop | :124-130 | (not covered) | :544-565 | High — CSC has 22 lines of nuance ("Two patterns keep long tasks coordinated..."), SKILL has 7 lines. The CSC version is the better source; the SKILL should reference it. |
| Session creation/destruction lifecycle | :142-158 | :29-53, :88-95 | (not covered) | Low — different audiences (SKILL = agent, sessions.md = operator). Healthy duplication. |
| `niwa_destroy_session(force=true)` branch deletion semantics | :155-158 | :96-130 | (not covered) | Low — sessions.md is canonical; SKILL is a shorter pointer. Fine. |

**Drift hotspots requiring policy:** the rows marked **High** /
**Very high** above — the Common Patterns block in the SKILL
duplicates content that lives in CSC. Currently nothing pins them
in sync. When CSC (cross-session-communication.md) gets a fix in
Phase 1-5, the SKILL won't auto-update because `buildSkillContent`
hardcodes the prose.

**Recommendation:** the SKILL should give the agent the *minimum
viable rule* for each pattern, then point at `docs/guides/` for the
worked example. E.g.:

> ## Long-running tasks
>
> `niwa_await_task` defaults to `timeout_seconds=600`. For tasks you
> expect to run longer, pass an explicit timeout. On `status:"timeout"`,
> re-call `niwa_await_task(task_id=...)` — the worker is still running.
>
> See `docs/guides/cross-session-communication.md` "Long-running tasks
> and `niwa_await_task` timeouts" for the full pattern.

This compresses the SKILL by ~30% and centralizes the source of truth.

---

## 4. Style consistency vs peer skills (3-5 concrete differences)

### 4a. Title Case vs sentence case headings

niwa-mesh: `## Reporting Progress`, `## Completion Contract`,
`## Message Vocabulary`, `## Peer Interaction`, `## Common Patterns`,
`## Session Management` (SKILL.md:57, 67, 79, 91, 110, 142).

shirabe peers: `## Decomposition Strategies` (`plan/SKILL.md:44`),
`## Required Sections` (`design/SKILL.md:60`), `## Phases`
(`release/SKILL.md:27`), but also `### Phase 1: Version Analysis`
(`release/SKILL.md:29`) — peers are mixed but trend toward sentence
case for noun phrases and Title Case only for proper-noun phases.

niwa peer guides go further: `sessions.md:8` "What a session is",
`:27` "Lifecycle", `:55` "Filesystem layout", `:97` "The session
branch in git" — all sentence case. niwa-mesh's Title Case is the
outlier.

`writing-style/SKILL.md:42` flags Title Case as a tell to fix.

### 4b. Loaded-tool callout: present in some peers, absent in niwa-mesh

`plan/SKILL.md:23`, `design/SKILL.md:22`, `decision/SKILL.md:24` all
include a one-line:

> "**Writing style:** Read `skills/writing-style/SKILL.md` for guidance."

niwa-mesh has no equivalent pointer. Less critical (niwa-mesh is
agent-facing operational, not prose-producing), but the inconsistency
shows the skill author didn't reach for the convention.

### 4c. Use of `argument-hint` frontmatter

Peer shirabe skills declare `argument-hint` (e.g.,
`work-on/SKILL.md:4`, `plan/SKILL.md:10`, `design/SKILL.md:10`):

```yaml
argument-hint: '<doc-path-or-topic> [--walking-skeleton|--no-skeleton] [--strategic|--tactical]'
```

niwa-mesh declares none (it's not slash-invoked, so it doesn't apply
in the same way) — but the absence underscores that niwa-mesh is a
*passive* skill (loaded automatically), not an *active* slash command.
Worth saying so explicitly in the body opening (Issue 1.2) so an
agent reading top-down knows it doesn't get arguments.

### 4d. Fenced code blocks for tool-call examples

niwa-mesh uses inline code spans for tool calls:

> "Use `niwa_delegate` to hand work to another role. Pass `mode=\"sync\"`..."
> (SKILL.md:39)

CSC and sessions.md use fenced blocks:

```
niwa_delegate(
  to="web",              // target role
  body={...},            // task payload
  mode="async",
)
```

(`cross-session-communication.md:67-74`).

For agent-facing prose, the inline form is denser and probably
correct for niwa-mesh. But the *one* fenced block in niwa-mesh
(SKILL.md:137-140) isn't fenced — it's just an indented paragraph
(see Issue 1.6). Internal inconsistency. Either use fenced blocks
everywhere or use inline calls everywhere.

### 4e. Boundary statement / "do not use this skill for..."

Peer pattern at `decision/SKILL.md:9-11`, `design/SKILL.md:6-9`,
`explore/SKILL.md:9-10`. niwa-mesh has no such block (Issue 1.8).

---

## 5. Concrete proposed issues

Numbered. Most fold into the design's Phase 6 (skill text + sessions
guide rewrite). A few are standalone.

### Issue P6.1 (fold into Phase 6) — Tighten frontmatter description

**Goal:** reduce the SKILL.md frontmatter `description` from ~870
chars to ~250-350 chars in the trigger-list shape used by
`shirabe/skills/decision/SKILL.md` and peers.

**ACs:**
- Description fits on 4-5 wrapped lines.
- Description names *what* the skill does and *which user phrases*
  trigger it; does not enumerate body sections.
- The `channels.go:687-689` comment is updated or removed (since
  the cap is no longer the binding constraint).
- Functional test: a peer skill comparison snapshot diff stays
  within 50% length of `decision/SKILL.md`'s description.

### Issue P6.2 (fold into Phase 6) — Add body opening orientation block

**Goal:** add 3-5 lines after `# niwa-mesh` that orient the agent
on what the mesh is, what role this skill plays, and the boundary
of "use the MCP tools, not the filesystem".

**ACs:**
- The opening states the task lifecycle in one line ("queued ->
  running -> {completed, abandoned, cancelled}").
- The opening calls out that the skill is passively loaded (no
  slash command, no arguments).
- The opening links to `docs/guides/cross-session-communication.md`
  and `docs/guides/sessions.md` as canonical references.

### Issue P6.3 (fold into Phase 6) — Convert wall-of-text sections to scannable structure

**Goal:** restructure "Delegation", "Peer Interaction", and
"Common Patterns" so each distinct fact has a sub-heading or
bullet, matching peer skill density.

**ACs:**
- "Common Patterns" splits into named `### Pattern: ...` blocks
  (fan-out, worker-asks-coordinator, cancel/update, long-running
  tasks, question-handling loop) — 5 sub-sections.
- "Delegation" splits sync vs async into two sub-paragraphs or
  bullets.
- Error-code mentions (SESSION_REQUIRED, SESSION_INACTIVE,
  SESSION_NOT_FOUND, BAD_TYPE, UNKNOWN_ROLE, TASK_ALREADY_TERMINAL)
  become a single `### Error codes` reference table at the bottom of
  the relevant section, not buried inline.

### Issue P6.4 (fold into Phase 6) — Standardize headings to sentence case

**Goal:** rename all `## Heading Like This` to `## Heading like
this`, matching `docs/guides/sessions.md` and the writing-style
skill's formatting tells table.

**ACs:**
- All `##` headings in the rendered SKILL.md use sentence case.
- `buildSkillContent` test verifies sentence case via regex.
- The change is mirrored in `docs/guides/sessions.md` for any
  Title Case that slipped in (none found in the audit, but worth
  re-checking on patch).

### Issue P6.5 (fold into Phase 6) — Fix the indented-pseudocode block

**Goal:** wrap the coordinator question-handling loop pseudocode at
`channels.go:810-813` in a fenced code block.

**ACs:**
- Rendered SKILL.md has a `\`\`\`` fence around the re-await loop
  pseudocode.
- The same pseudocode in
  `docs/guides/cross-session-communication.md:507-515` is the
  canonical source; the SKILL trims to 3 lines and links out.

### Issue P6.6 (fold into Phase 6) — Add "When NOT to use the mesh" boundary block

**Goal:** add the peer-skill anti-pattern block.

**ACs:**
- A `## When not to use this` section lists at minimum:
  in-context one-shot edits, work requiring interactive permission
  prompts, anything where the user is in the loop and synchronous.
- Section sits before "Common Patterns" so an agent scanning
  top-down reads it before deciding to delegate.

### Issue P6.7 (fold into Phase 6) — Replace duplicated prose with guide pointers

**Goal:** for each High / Very high drift row in §3, trim the SKILL
to a 1-2 line rule and a `See docs/guides/...` link.

**ACs:**
- Long-running tasks block (SKILL.md:124-130) shrinks to 3 lines
  and links to CSC `:544-565`.
- Coordinator question-handling block (SKILL.md:131-140) shrinks
  to 4 lines and links to CSC `:436-515`.
- `buildSkillContent` produces a SKILL.md under 6 KB (currently
  ~5.6 KB; the goal is to keep it under that even after adding
  Issue 1.2's opening and Issue 1.8's boundary).

### Issue P6.8 (fold into Phase 6) — Document `niwa_query_task` vs `niwa_await_task` discriminator

**Goal:** add explicit guidance for when to poll (`query`) vs block
(`await`).

**ACs:**
- A `### Polling vs blocking` sub-section in the Delegation block
  states: use `niwa_query_task` for non-blocking checks and inside
  loops where you do other work; use `niwa_await_task` when the
  next step depends on the result.
- The fan-out pattern in Common Patterns is updated to show
  `niwa_await_task` use, not `niwa_query_task`.

### Issue P6.9 (fold into Phase 6) — Sessions guide: clarify `niwa session list` deprecation note

**Goal:** the `sessions.md:167-179` paragraph mixing canonical use
and deprecation should be split.

**ACs:**
- A `> **Deprecation note:**` callout at the top of the
  `### niwa session list` section explains the bare-form fallback.
- The body of the section describes only the canonical
  flag-driven usage.
- A separate AC tracks fixing the bare-form fallback in the CLI
  itself (so the deprecation can eventually be removed).

### Issue P6.10 (fold into Phase 6, but call out for Phase 6 ACs) — Sessions guide: explicit Phase 6 deliverables

**Goal:** make the design's Phase 6 deliverables (`:1334-1344` and
`:953-964`) more granular in the AC list.

**ACs (additional, beyond what `:953-964` already says):**
- The new `daemon` sub-object section uses the same field-table
  format as `:75-86`'s state-file table, not prose.
- The `taskstore_lost` recovery section includes a worked CLI
  example showing the recovery flow (e.g., `niwa task list` ->
  `niwa_redelegate` from a fresh coordinator).
- The worker config inheritance section includes the named-skill
  availability checklist verbatim from
  `DESIGN-niwa-mesh-reliability.md:1246-1268` so the contract is
  user-readable without bouncing to the design doc.

### Issue 11 (standalone, NOT in Phase 6) — Move "Contributor notes" out of `sessions.md`

**Goal:** separate operator audience from contributor audience.

**ACs:**
- `sessions.md:303-324` content moves to either
  `docs/guides/sessions-internals.md` (preferred) or to a doc
  comment block on `SessionLifecycleState` in
  `internal/mcp/session_lifecycle.go`.
- `sessions.md` ends after "Parallel sessions for the same repo".
- The design doc's source-of-truth claim ("`SessionLifecycleState`
  struct in `internal/mcp/session_lifecycle.go` is the
  authoritative definition") is preserved verbatim wherever the
  prose lands.

### Issue 12 (standalone, NOT in Phase 6) — Em dash audit on niwa guides

**Goal:** apply the writing-style "em dash overuse" guidance
(`writing-style/SKILL.md:43`) to the four niwa guides.

**ACs:**
- Em dashes in `sessions.md`, `cross-session-communication.md`,
  `vault-integration.md`, `workspace-config-sources.md`,
  `one-time-notices.md` are reviewed and reduced to those carrying
  meaning.
- A `make lint-style` target is added (or the existing markdown
  lint extended) to flag em-dash density above a threshold.
- This issue is **opt-in** — if the maintainer feels current usage
  is fine, this is closeable as `wontfix`.

### Issue 13 (standalone, NOT in Phase 6) — Drift-prevention tooling between SKILL and CSC guide

**Goal:** institutionalize the "guide is the canonical source"
contract so the SKILL doesn't drift again after Phase 6.

**ACs:**
- A test (`internal/workspace/channels_drift_test.go`) reads
  specific phrases from the rendered SKILL and asserts they appear
  verbatim in `docs/guides/cross-session-communication.md` (or
  `sessions.md`).
- The test fails if the SKILL adds new long-form prose that isn't
  in a guide.
- Alternatively, make `buildSkillContent` read snippets from the
  guide files at build time. (More invasive; consider as v2.)

---

## Summary of issue counts

| Folds into Phase 6 | Standalone |
|--------------------|------------|
| P6.1 description tightening | 11 contributor-notes split |
| P6.2 opening orientation | 12 em-dash audit |
| P6.3 scannable structure | 13 drift-prevention test |
| P6.4 sentence-case headings | |
| P6.5 fenced pseudocode | |
| P6.6 boundary block | |
| P6.7 trim duplicated prose | |
| P6.8 query-vs-await discriminator | |
| P6.9 sessions list deprecation note | |
| P6.10 explicit Phase 6 sub-ACs | |

**Phase 6 additions:** 10 ACs.
**Standalone issues:** 3 ACs.
**Total proposed:** 13 issues.

The most impactful three are: (1) **Issue P6.1** (frontmatter
tightening — directly affects whether Claude Code loads the skill
at all under cap pressure), (2) **Issue P6.7** (trim duplicated prose
— the highest-drift hotspot), (3) **Issue P6.6** (boundary block —
the most-cited peer-skill convention that niwa-mesh lacks).
