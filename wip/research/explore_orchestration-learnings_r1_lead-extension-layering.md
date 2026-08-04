# R1: How the shirabe extension layering actually works, and whether it transfers to `_common.md`

Research lead: extension-layering. Read-only investigation. Every claim below is
tagged **[verified]** (observed on disk, in source, or by direct experiment) or
**[inferred]** (reasoned from evidence, not directly observed).

---

## 1. The `@path` directive

### Which layer resolves it

**Claude Code resolves it, not shirabe.** [verified — by elimination and by design-doc statement]

There is no shirabe code that reads, parses, or expands `@` lines. The shirabe Rust
crates were searched and the only occurrences of the string `shirabe-extensions`
anywhere in the repo are in markdown (SKILL.md files, design docs, a `.gitignore`, and
one `evals.json` string). No crate resolves the directive.

The design doc states the mechanism explicitly:

> `.claude/shirabe-extensions/<name>.md` are loaded via `@` includes in the base
> SKILL.md. The `@` resolution is handled client-side by Claude Code before the LLM
> processes the skill — confirmed by testing: 0 tool calls, deterministic, works
> with plugin registry install, `--plugin-dir`, and local paths.

`public/shirabe/docs/designs/current/DESIGN-skill-extensibility.md:100-104`

So it is the same client-side import machinery that expands `@` lines in the CLAUDE.md
chain, applied to a SKILL.md loaded as an attachment. Corroborating evidence from this
very session: `.claude/rules/workspace-imports.md` contains two absolute `@` lines, and
the contents of `workspace-context.md` and `CLAUDE.overlay.md` arrived pre-expanded in
the system prompt. [verified]

### What the path is relative to

The directive is written as a **relative** path, `@.claude/shirabe-extensions/<name>.md`.

The design doc claims it resolves from "the workspace root":

> The `@.claude/shirabe-extensions/` path resolves from the workspace root regardless of
> whether shirabe is installed via plugin registry, git submodule, or local path.

`DESIGN-skill-extensibility.md:186`, restated at `:258` ("Installation-agnostic: path
resolves from workspace root regardless of how shirabe is installed").

Note "workspace root" here is Claude Code's sense of the term (the session's project
root / cwd), **not** the niwa sense (the directory holding instances). [inferred — the
doc predates and is independent of niwa, and the resolution cannot be relative to the
plugin directory, since no plugin cache dir contains a `shirabe-extensions/` folder]

This reading is confirmed by where niwa actually places the files: **into each managed
repo's `.claude/`**, i.e. the directory a session's cwd sits in when working in that
repo. See section 4.

### Missing-file behaviour

**Silent skip, not an error** — with a documented caveat that the raw text may remain
visible. [verified by experiment + design doc]

Design doc: "Missing files produce silent skips (raw `@path` text visible to LLM but
ignored in practice)" (`DESIGN-skill-extensibility.md:104`), and again at `:253-254`
("Both are resolved client-side; missing files are silently skipped").

The doc lists this as a known negative:

> Raw `@path` text remains visible in skill context when extension file is absent;
> not a confirmed failure mode, but a behavioral dependency on the LLM ignoring it

`DESIGN-skill-extensibility.md:479-481`

This matters for the live workspace, because **every one of the ten `@<skill>.md` slots
is empty in every niwa-managed repo** — only the `.local.md` slots are filled. See
section 4. So the "missing file" path is not an edge case here; it is the steady state
for half the declared slots.

### Live population state

`find` across the workspace tree confirms `.claude/shirabe-extensions/` exists in every
managed repo of every instance. In the current instance: [verified]

```
public/tsuku, public/koto, public/niwa, public/dot-niwa, public/.github,
private/tools, private/vision, private/dot-niwa-overlay, private/coding-tools
  -> design.local.md, explore.local.md, plan.local.md, prd.local.md, work-on.local.md

public/shirabe
  -> the same five .local.md files, PLUS work-on.md and README.md
     (those two are committed in the shirabe repo itself, not distributed by niwa)
```

The instance root `.../tsuku+orchestration_learnings-6b7b745a/.claude/` contains only
`bin/`, `hooks/`, `rules/`, `settings.json`, `settings.local.json` — **no
`shirabe-extensions/`**. The niwa workspace root `/home/dgazineu/dev/niwaw/tsuku/`
likewise has none. The directory exists only at repo level. [verified]

---

## 2. Which skills carry extension hooks

Enumerated by grepping for a line beginning `@.claude/shirabe-extensions/` in every
`public/shirabe/skills/*/SKILL.md`: [verified]

| Skill | Hook | Slot line numbers |
|-------|------|-------------------|
| brief | yes | `skills/brief/SKILL.md:22-23` |
| decision | yes | `skills/decision/SKILL.md:15-16` |
| design | yes | `skills/design/SKILL.md:13-14` |
| explore | yes | `skills/explore/SKILL.md:14-15` |
| plan | yes | `skills/plan/SKILL.md:13-14` |
| prd | yes | `skills/prd/SKILL.md:14-15` |
| roadmap | yes | `skills/roadmap/SKILL.md:17-18` |
| strategy | yes | `skills/strategy/SKILL.md:21-22` |
| vision | yes | `skills/vision/SKILL.md:15-16` |
| work-on | yes | `skills/work-on/SKILL.md:6-7` |
| charter | no | — |
| comp | no | — |
| execute | no | — |
| inflight | no | — |
| private-content | no | — |
| public-content | no | — |
| release | no | — |
| review-plan | no | — |
| scope | no | — |
| writing-style | no | — |

Ten of twenty skills carry hooks. The installed plugin cache at
`/home/dgazineu/.claude/plugins/cache/shirabe/shirabe/0.15.1-dev/skills/` has the same
ten, two lines each. [verified]

### Naming convention

Strict and documented: the extension basename equals the skill's `name:` frontmatter
field.

> ```markdown
> @.claude/shirabe-extensions/<name>.md
> @.claude/shirabe-extensions/<name>.local.md
> ```
> where `<name>` matches the `name:` field in the skill's SKILL.md frontmatter.
> These two lines are the stable public API of the extension mechanism.

`DESIGN-skill-extensibility.md:316-322`

Placement is also part of the contract: immediately after the closing `---` of
frontmatter, before any prose (`DESIGN-skill-extensibility.md:143-153`, Decision 3;
implementation instruction at `:395`). Verified on disk — in every hooked skill the two
`@` lines sit exactly one blank line after the frontmatter terminator and before the
first `#` heading.

### Hooks nobody fills

**Five of the ten hooked skills have no extension file in this workspace at all**:
`brief`, `decision`, `roadmap`, `strategy`, `vision`. niwa distributes only five names
(`design`, `explore`, `plan`, `prd`, `work-on` — `workspace.toml:56-60`), and the
strategic-chain skills were added to shirabe later without a corresponding config
update. [verified]

So those five skills render four dead `@` lines each into context on every invocation,
plus one dead line each for the five that are filled — 15 unresolved `@` lines across
the ten hooked skills in a niwa-managed repo. Design doc acknowledges the token cost
(`:485-486`, "~100 tokens per skill") but that estimate assumed the slots would be
filled.

---

## 3. The two-file split

### Intended difference

Per the design, the split is **committed team config vs. gitignored personal override**:

> A `.local.md` variant (`.claude/shirabe-extensions/<name>.local.md`, gitignored)
> enables personal machine-level overrides that aren't committed to the repo.

`DESIGN-skill-extensibility.md:105-106`

> Layered: repo-level extension committed to the project; `.local.md` gitignored for
> personal overrides

`DESIGN-skill-extensibility.md:259`

Load order is base skill, then `<name>.md`, then `<name>.local.md`
(`DESIGN-skill-extensibility.md:361-365`), with the later file carrying more LLM weight.

Both layers are **additive-only**. There is no override or suppression syntax, and this
was a deliberate rejection, not an oversight:

> Extension files append context that the LLM considers alongside the base skill.
> There is no "delete this instruction" operation in LLM-read markdown — any attempt
> to suppress base behavior via text ("ignore step X") is unreliable and model-dependent.
> Extension files should express intent ("also do Y when doing Z") not negation.

`DESIGN-skill-extensibility.md:166-171` (Decision 4). A structured `## Override:` format
was investigated and found to have "weak reliability... No test confirmed reliable
suppression" (`:173-178`).

### What is actually in the files

**The versioned source** `public/dot-niwa/.niwa/extensions/explore.md` (26 lines) is
project convention data — a label vocabulary table (`needs-triage`, `needs-design`,
`needs-prd`, `needs-spike`, `needs-decision`), a two-bullet content-governance rule
splitting private vs public repos, and a wip/ artifact naming convention. It is pure
declarative project context; no procedure, no tool invocation. The other four
(`design.md` 944B, `plan.md` 917B, `prd.md` 673B, `work-on.md` 1502B) are the same shape.

**shirabe's own committed `work-on.md`** is the one genuinely load-bearing extension in
the workspace. It declares a verification map consumed by the definition-of-done gate.
Its sibling README (which is deliberately *not* imported) explains:

> Project-specific `/work-on` configuration for the shirabe repo. The work-on skill
> imports `work-on.md` here via `@.claude/shirabe-extensions/work-on.md`, so that file
> is pulled into context on every `/work-on` run — keep it minimal. This README holds
> the rationale a reader wants but the skill does not need loaded (it is **not** imported).

`public/shirabe/.claude/shirabe-extensions/README.md:1-6`

That README is a useful pattern in its own right: split the *contract* (imported, kept
minimal) from the *rationale* (adjacent, not imported).

**The overlay's `.local.md` files** at
`private/dot-niwa-overlay/.claude/shirabe-extensions/{design,explore,plan,prd,work-on}.local.md`
are **not** hand-authored private content, contrary to what the task framing assumed.
They are niwa's own distribution output landing in the overlay repo, because
`dot-niwa-overlay` is itself a managed repo in the workspace. `explore.local.md` there
is byte-identical to `public/dot-niwa/.niwa/extensions/explore.md`. The overlay's
`.gitignore` is a single line, `*.local*`, so they are ignored, not committed.
[verified — diffed the two files, read the .gitignore]

The overlay's actual private contribution is `workspace-overlay.toml` (private group
definition, scope overrides, vault provider, secret bindings, private marketplace) plus
`.niwa/CLAUDE.overlay.md` and `.niwa/claude/private.md`. **It adds no extension files.**

### Gitignore status

Yes, `.local.md` is ignored — but by a broader mechanism than shirabe's design assumed.
niwa relies on target repos carrying a `*.local*` pattern:

> Target repos gitignore `*.local*` to keep niwa-generated files out of version control.
> Files distributed by niwa must follow this pattern or they'll show up as untracked.

`public/niwa/docs/designs/current/DESIGN-file-distribution.md:41-44`

In `public/niwa/.gitignore:27` that pattern is present. In practice a *different* rule
fires first — `git check-ignore -v .claude/shirabe-extensions/explore.local.md` in
`public/niwa` reports `.gitignore:41:.claude/`, i.e. the whole `.claude/` directory is
ignored, so the `.local` infix is redundant there. [verified]

shirabe's own repo takes the opposite approach, ignoring `.claude/*` then re-including
exactly the one file it needs to ship:

```
.claude/*
!.claude/settings.json
!.claude/shirabe-extensions/
!.claude/shirabe-extensions/work-on.md
*.local*
```

`public/shirabe/.gitignore:15-20` and `:30`

---

## 4. How the files get placed

### The distribution table

`public/dot-niwa/.niwa/workspace.toml:55-60`:

```toml
[files]
"extensions/design.md" = ".claude/shirabe-extensions/"
"extensions/explore.md" = ".claude/shirabe-extensions/"
"extensions/plan.md" = ".claude/shirabe-extensions/"
"extensions/prd.md" = ".claude/shirabe-extensions/"
"extensions/work-on.md" = ".claude/shirabe-extensions/"
```

The live workspace config at `/home/dgazineu/dev/niwaw/tsuku/.niwa/workspace.toml:56-60`
is identical. The overlay's `workspace-overlay.toml` has **no** `[files]` table at all.
[verified]

### Which level it targets: repos, and only repos

niwa has three distribution tables with different semantics:

| Table | Lands at | Name |
|-------|----------|------|
| `[files]` | each managed repo | rewritten with a `.local` infix |
| `[instance.files]` | each instance root | verbatim (exact name) |
| `[root.files]` | the workspace root | verbatim (exact name) |

`public/niwa/docs/guides/file-distribution.md:7-11`

`[files]` is the per-repo table. So the answer is **each managed repo — not the
workspace root, not the instance root**. Confirmed against the live filesystem in
section 1: nine repos have the directory, the instance root and workspace root do not.

### The `.local` rename, and the mismatch it produces

**This is the most consequential finding of the investigation.**

Because the destination `.claude/shirabe-extensions/` ends in `/` (a directory, not an
explicit filename), niwa auto-inserts a `.local` infix:

```go
// localRename inserts ".local" before the file extension. Files without an
// extension get ".local" appended. This ensures distributed files match the
// *.local* gitignore pattern in target repos.
//
//	"design.md"    -> "design.local.md"
```

`public/niwa/internal/workspace/materialize.go:1459-1467`; the idempotence guard that
leaves an already-`.local` name alone is `injectLocalInfix` at `materialize.go:45-57`.
The design documents this as intentional, with an escape hatch:

```toml
# Auto-renamed: design.md -> design.local.md
"extensions/design.md" = ".claude/shirabe-extensions/"

# Explicit name: no renaming
"extensions/design.md" = ".claude/shirabe-extensions/custom-name.local.md"
```

`DESIGN-file-distribution.md:134-142`

**Consequence:** shirabe's committed-team-config slot (`<skill>.md`) is *never* filled
by niwa. Everything niwa distributes lands in the gitignored personal-override slot
(`<skill>.local.md`). The two-layer split that shirabe designed — team layer, then
personal layer on top — is collapsed to a single layer in this workspace, and it is the
*wrong* one semantically: workspace-wide team conventions are riding in the slot
reserved for individual machine-local overrides.

Nothing is broken by this — the content loads, because shirabe declares both slots and
the `.local.md` one is genuinely imported. But:

- there is **no remaining slot for a personal override**, since niwa occupies it and
  overwrites it on every `niwa apply`;
- the extension content is invisible to `git` in the target repos, so a reviewer reading
  a repo sees no trace of the conventions governing its skills;
- if anyone ever hand-writes a `<skill>.md` in a managed repo, it will load *before*
  niwa's distributed file and be outranked by it.

Does live state match what the config implies? Reading `[files]` naively, you would
expect `explore.md` on disk; you get `explore.local.md`. Config and filesystem are
consistent **once you know the rename rule**, but the config as written does not
telegraph it, and it silently targets the wrong half of shirabe's contract. That
mismatch is a finding, not a bug report — it is working as each system independently
specified.

---

## 5. Documentation

### shirabe

**The consumer-facing guide does not exist.** `DESIGN-skill-extensibility.md:385` lists
as a Phase 1 deliverable:

> `docs/extending.md` — consumer-facing extension guide: stable API surface (the two
> `@` slot lines, CLAUDE.md header names), what to express by behavior description vs.
> what to avoid, `.local.md` use-case guidance

`ls public/shirabe/docs/extending.md` -> no such file. `ls public/shirabe/CHANGELOG.md`
-> no such file, though the design designates it as the breaking-change signal
(`:300`, `:344-347`, `:502-504`). `docs/guides/` contains eight files, none about
extensions. `grep` for `shirabe-extensions` across README.md, AGENTS.md, CLAUDE.md and
`docs/guides/` returns nothing. [verified]

The contract is documented **only** in the design doc and in two in-repo artifacts: the
`README.md` beside shirabe's own extension file, and
`skills/work-on/references/verification-map.md:4`. The extension mechanism has no
user-facing documentation. [verified]

### niwa

`docs/guides/file-distribution.md` does **not** mention shirabe or extensions anywhere —
grep returns zero hits. Its worked example is `.mcp.json` distribution
(`file-distribution.md:34-59`). The guide covers the mechanism generically. [verified]

The *design* doc does name shirabe extensions as the motivating case:

> But some workflows need to copy arbitrary files into repos. Plugin extension files
> go to `.claude/shirabe-extensions/`, command fragments to `.claude/commands/`,
> configuration templates to repo subdirectories. Today, each new file type would
> require writing a new materializer with its own config types, tests, and pipeline
> integration. This doesn't scale.

`DESIGN-file-distribution.md:34-39`

and uses `"extensions/design.md" = ".claude/shirabe-extensions/"` as its canonical
schema example (`:69-72`). Note that design is still marked **Status: Proposed**
(`:25-27`) despite being shipped and in live use.

---

## 6. Applicability to `_common.md`

### What `_common.md` actually is

`/home/dgazineu/dev/niwaw/tsuku/.niwa/dispatch-briefs/_common.md` — 167 lines, 8.5 KB,
eleven `##` sections (Autonomy mandate, The issue is primary and it may be wrong, Check
reachability not just mechanism, Acceptance criteria that always apply, Testing
constraints in this repo, How to test not just what, The golden-plan CI workflow, Repo
conventions, Scope discipline, Reporting). Its own opening states the contract: [verified]

> Every dispatch brief in this directory references this file. It carries the parts
> that do not change between tasks, so they cannot drift between workers. Your brief
> carries the parts that do [...] Read your brief first. This file is the standing
> agreement underneath it. Where the two disagree, your brief wins — it was written
> for your task.

`_common.md:3-9`

It contains **zero `@` lines** (grepped across `_common.md` and all 56 briefs in the
directory). [verified]

### How it loads today

Purely by prose instruction plus the worker's Read tool. Every brief opens with a
"Read first" list naming the absolute path, e.g.:

```
## Read first

1. **`/home/dgazineu/dev/niwaw/tsuku/.niwa/dispatch-briefs/_common.md`** — the standing
   working agreement. It applies, and it is also *itself a subject* of this task.
```

`.niwa/dispatch-briefs/orchestration-learnings.md:3-6`; the same pattern appears at line
5 of `design-docs-gc.md`, `ci-push-codecov.md`, `shirabe-ci-gate.md`,
`doctor-path-precedence.md`, `release-v0-13-0.md`, `library-staging.md`,
`update-all-stale-cleanup.md`, `storage-plan-fields.md`. [verified]

The chain is: `niwa dispatch` puts *"Read \<abs-path-to-brief\> for your complete task
brief"* in the worker's opening prompt (`internal/workspace/rootskills/dispatch/SKILL.md:88-96`);
the worker Reads the brief; the brief's prose tells it to Read `_common.md`; the worker
Reads that too. Two LLM-driven tool calls, both dependent on the model complying.

Notably, **niwa's `/dispatch` skill has no knowledge of `_common.md`** — grep for
`_common` in `rootskills/dispatch/SKILL.md` returns nothing. The convention is entirely
hand-maintained in the brief corpus. [verified]

### Does `@import` fire in that context? No.

I tested this directly rather than reasoning about it. I created
`probe/target.md` containing:

```
# Probe doc
@.claude/shirabe-extensions/probe.md
@.claude/shirabe-extensions/probe.missing.md

End of probe.
```

with a real `probe/.claude/shirabe-extensions/probe.md` holding a sentinel string, then
Read `target.md` with the Read tool. The result was the file verbatim — line 2 came back
as the literal text `@.claude/shirabe-extensions/probe.md`, the sentinel never appeared,
and the missing-file line was equally inert. [verified by experiment]

**The Read tool does not resolve `@` imports.** Import expansion is a property of *how a
file enters context* — the CLAUDE.md chain and skill attachments get expanded by the
client at load time; file contents returned through a tool call do not. Putting an `@`
line in `_common.md` would place raw, inert text in front of the worker.

### Assessment: the pattern does not transfer

The shirabe mechanism has three properties, and `_common.md` can inherit none of them
as-is:

1. **Deterministic, zero tool calls.** shirabe's extensions load because Claude Code
   expands a skill attachment before the model sees it. `_common.md` is not an
   attachment; it is a Read result. There is no attachment path to hook.
2. **Declared slot in a shipped file.** shirabe's contract lives in files shirabe
   *ships* — the plugin author writes the `@` lines and the consumer fills the file.
   The inverse of `_common.md`'s situation, where the file at the workspace root *is*
   the thing to be overridden.
3. **Placement by `[files]`.** `_common.md` lives at `<workspaceRoot>/.niwa/dispatch-briefs/`,
   which is *inside niwa's config directory*. That is not a valid destination for any
   distribution table — the guide is explicit: "Destinations stay at the project root
   of their level. Distributed files are meant for the workspace-root or instance-root
   project directory, **not for niwa-internal directories**"
   (`file-distribution.md:92-94`). Further, `dispatch-briefs/` is treated as niwa-*local
   runtime state* that must be actively rescued from the config-dir swap on every
   fetch — see `preserveDispatchBriefs` at `internal/workspace/snapshotwriter.go:489-524`
   and its comment: "dispatch-briefs/ is niwa-written local state under the config dir,
   not upstream source content." A file niwa deliberately excludes from the source/
   distribution model cannot be distributed by the distribution model.

There is one salvageable piece, and it is the cheap one: **layering by convention rather
than by mechanism.** `_common.md` already establishes precedence in prose — "Where the
two disagree, your brief wins" (`_common.md:8-9`) — and briefs already exercise it
explicitly (`release-v0-13-0.md:18`: "This overrides `_common.md`'s autonomy mandate";
`shirabe-ci-gate.md:18` enumerates which sections carry over to a different repo). That
is a working override layer today. It costs a Read call and depends on model compliance,
but so does everything else about the brief system, and it demonstrably works — the same
brief corpus records that `_common.md` "demonstrably changed worker behaviour *because
it is read*" (`orchestration-learnings.md:40`).

---

## Implications

**Answering question 6 directly: no, the `@import` pattern does not transfer to
`_common.md`, and the reason is mechanical rather than stylistic.** Import expansion
happens when Claude Code loads a file *as context* — the CLAUDE.md chain, a skill
attachment. `_common.md` reaches the worker as the return value of a Read tool call,
and I verified by experiment that Read hands back `@` lines as literal text. An `@`
line added to `_common.md` would be inert.

Three routes remain, in descending order of how much I would recommend them.

**Keep the prose-convention layering and make it explicit.** This is already 80% built
and it already works. `_common.md` states brief-wins precedence; briefs already override
named sections. Formalizing it costs nothing: add a short, standard "Local overrides"
section to `_common.md` naming a sibling path (say `_common.local.md`) and instructing
the worker to Read it if present and treat it as outranking the base — plus a matching
line in the `/dispatch` skill so generated briefs carry the instruction. This mirrors
shirabe's two-file split semantically while being honest that the load is a Read, not an
import. Downside: an extra tool call, and "if present" means the worker must attempt a
Read that may fail.

**Move the standing agreement into the CLAUDE.md chain, where imports genuinely fire.**
If the goal is specifically the shirabe mechanism, this is the only route that delivers
it. Content distributed via `[instance.files]` (verbatim, no `.local` rename —
`file-distribution.md:10`) into each instance root as an imported fragment would get
real client-side expansion and real zero-tool-call determinism. But this changes what
`_common.md` *is*: it stops being a document a worker deliberately reads at the start of
a task and becomes ambient context in every session at that root, including sessions
that are not dispatched workers. That is a meaningful semantic change and probably not
what the author wants.

**Distribute `_common.md` through `[files]`.** I would not. It is blocked twice over:
the destination is inside niwa's own config directory, which the guide rules out, and
`dispatch-briefs/` is explicitly classified as niwa-local runtime state that
`preserveDispatchBriefs` rescues from the config swap. Fighting both would be building
against the grain of two deliberate design decisions.

**The most important caveat, independent of which route is chosen:** the shirabe
mechanism as *deployed in this workspace* is not the mechanism as *designed*. niwa's
`[files]` table auto-appends a `.local` infix, so everything niwa distributes lands in
`<skill>.local.md` — the slot shirabe reserved for gitignored personal overrides — while
the committed team-config slot `<skill>.md` sits empty in all nine managed repos. The
two-layer design is collapsed to one layer, occupying the wrong half, with the content
invisible to git in every target repo and no slot left for an actual personal override.
Anyone imitating "the way the shirabe skills work" should imitate the design, not the
current deployment. Two smaller items worth surfacing while this area is open: five
hooked skills (`brief`, `decision`, `roadmap`, `strategy`, `vision`) have no extension
file anywhere, so fifteen `@` lines resolve to nothing on every invocation; and the
consumer-facing `docs/extending.md` and `CHANGELOG.md` that
`DESIGN-skill-extensibility.md:385` and `:300` designate as the contract's public
documentation and breaking-change signal were never written.
