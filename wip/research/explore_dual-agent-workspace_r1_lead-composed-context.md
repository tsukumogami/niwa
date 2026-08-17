# Lead: What must the composed $CODEX_HOME/AGENTS.md contain?

## Findings

### 1. The context-materialization path, read end to end

Materialization happens at four write sites, each hardcoded to a level, none of
them today writing to `$CODEX_HOME`:

- **Workspace root** (`internal/workspace/root_materializer.go:373-380`,
  `writeRootClaudeMD`/`generateRootClaudeContent`). Writes
  `{workspaceRoot}/{ag.RootContextFileName()}` from a **fixed Go template**
  (not a config-sourced file) describing the workspace/instance model and the
  `/dispatch` skill. Called from `niwa init` (`internal/cli/init.go:785`) and
  `niwa apply` (`internal/cli/apply.go:245`) via `MaterializeWorkspaceRoot`.
  Measured on the live workspace root
  (`/home/dgazineu/dev/niwaw/tsuku/CLAUDE.md`): **1692 bytes**, byte-identical
  to the template in `root_materializer.go:396-429`.
- **Instance root, primary content** (`internal/workspace/content.go:28-50`,
  `InstallWorkspaceContent`). Writes
  `{instanceRoot}/{ag.RootContextFileName()}` from
  `cfg.Claude.Content.Workspace.Source` — an operator-authored file resolved
  under `content_dir`, template-expanded (`{workspace}`, `{workspace_name}`).
  This is the "# Tsuku Project..." file at
  `tsuku+codex_dual_agent-4ff0633a/CLAUDE.md`: **7798 bytes**.
- **Instance root, companion layers** (`internal/workspace/root_materializer.go:182-235,390-417`).
  Three more files written to the instance root and stitched into the primary
  file **only via `@import`**, a Claude-only mechanism:
  - `workspace-context.md` — auto-generated from the classified repo list
    (`generateWorkspaceContext`, `root_materializer.go:563-600`): a
    `# Workspace: <name>` header, a `## Repos` section grouped by group
    directory, and boilerplate navigation tips. Measured: **692 bytes**.
  - `CLAUDE.overlay.md` — copied verbatim from the private overlay clone
    (`InstallOverlayClaudeContent`), present only when an overlay repo ships
    one. Measured: **5757 bytes**.
  - `CLAUDE.global.md` — copied from the operator's global config dir
    (`InstallGlobalClaudeContent`), absent in this instance (the function
    returns `nil, nil` when the source file doesn't exist —
    `root_materializer.go:393-396`).
  These are not `@import`-ed directly into the primary `CLAUDE.md`. Instead
  each is appended as an `@`-line into
  `.claude/rules/workspace-imports.md` (`workspace_context.go:126-155`,
  `appendToWorkspaceRulesFile`), a file Claude Code auto-loads by directory
  convention as it walks up from cwd. Measured content of that file in the
  live instance:
  ```
  @/home/dgazineu/dev/niwaw/tsuku/tsuku+codex_dual_agent-4ff0633a/workspace-context.md
  @/home/dgazineu/dev/niwaw/tsuku/tsuku+codex_dual_agent-4ff0633a/CLAUDE.overlay.md
  ```
  167 bytes. This two-level indirection (`.claude/rules/*.md` auto-loaded →
  `@import` pulls in the companions) exists specifically so opening Claude
  from a sub-repo doesn't trigger the "Allow external CLAUDE.md imports?"
  dialog (comment at `workspace_context.go:178-182`). None of it means
  anything to Codex, which reads neither `.claude/rules/` nor `@import` lines
  (confirmed live, see Finding 4).
- **Group level** (`internal/workspace/content.go:56-87`, `InstallGroupContent`).
  Writes `{instanceRoot}/{group}/{ag.RootContextFileName()}` from
  `cfg.Claude.Content.Groups[group].Source` (or an overlay-added group's own
  dir). Measured: `public/CLAUDE.md` **879 bytes**, `private/CLAUDE.md`
  **859 bytes**.
- **Repository/worktree level** (`internal/workspace/content.go:108-217`,
  `InstallRepoContentTo`). Writes `{repoDir}/CLAUDE.local.md` from an explicit
  `cfg.Claude.Content.Repos[repo].Source`, auto-discovered
  `{content_dir}/repos/{repo}.md`, and an optional overlay append; also
  installs subdir `CLAUDE.local.md` files. **This entire function is a no-op
  under Codex today** — line 130-132: `if !ag.WritesRepoLevelContext() {
  return result, nil }`. `ApplyToWorktree` calls the same function
  (`InstallRepoContentTo`, not a fork), so worktrees inherit the same skip.

None of these four sites write to `$CODEX_HOME`. A prior, already-landed
increment (`docs/designs/current/DESIGN-interactive-codex-session.md`,
confirmed `status: Current`, implemented on
`feat/interactive-codex-session`) made the workspace-root and instance-root
*and group* writers agent-aware — under `default_agent = "codex"` they emit
`AGENTS.md` instead of `CLAUDE.md` at those same three directories, and the
repo/worktree writer is skipped outright. It explicitly **did not** inline the
companion layers (its own words, `DESIGN...md:395-401`): "This slice
deliberately does not inline the companion layers into `AGENTS.md`... belongs
with the full agent-neutral content work." That gap is exactly this lead's
question.

Critically, that landed design writes `AGENTS.md` as three **separate,
per-directory** files (workspace root, each group, instance root) — the same
shape as the `CLAUDE.md` tree, just renamed. Nothing today writes a single
`$CODEX_HOME/AGENTS.md`. Per the parent explore-scope doc
(`wip/explore_dual-agent-workspace_scope.md:26-27`), this is a known defect:
"That makes the existing workspace-root and group-level `AGENTS.md` invisible
to any Codex session not sitting in that exact directory." I confirmed this
independently (Finding 4): Codex does not walk up, so a session sitting in
`public/niwa` sees none of the workspace-root, group, or instance-root
`AGENTS.md` files — only a literal `AGENTS.md` in `public/niwa` itself (which
niwa does not write) and `$CODEX_HOME/AGENTS.md` (which niwa does not write
either, today). **The composed `$CODEX_HOME/AGENTS.md` this lead is scoping is
net-new work, not a rename of what exists.**

There is no existing "flatten this tree into one document" mechanism anywhere
in niwa. `generateWorkspaceContext` synthesizes one file from structured data
(the classified repo list), but nothing concatenates the *content* of several
already-rendered files into one.

### 2. Real measured sizes for the live instance

Full inventory of the context tree for
`tsuku+codex_dual_agent-4ff0633a` (measured with `wc -c`, real files, not
estimates):

| File | Bytes |
|---|---|
| workspace root `CLAUDE.md` | 1,692 |
| instance root `CLAUDE.md` (primary) | 7,798 |
| instance root `workspace-context.md` | 692 |
| instance root `CLAUDE.overlay.md` | 5,757 |
| `.claude/rules/workspace-imports.md` | 167 |
| `public/CLAUDE.md` (group) | 879 |
| `private/CLAUDE.md` (group) | 859 |
| 7 repo `CLAUDE.md` files (`private/tools` 9716, `private/vision` 1136, `private/coding-tools` 4259, `public/tsuku` 7138, `public/koto` 4192, `public/niwa` 2616, `public/shirabe` 12429) | 41,486 |
| 2 repo `CLAUDE.local.md` files (`public/tsuku` 4755, `public/koto` 2616) | 7,371 |
| **Total, everything flattened** | **80,067 bytes ≈ 20,000 tokens** (≈4 bytes/token) |

Two ways to read that number:

- **Per-repo content is the majority.** 41,486 + 7,371 = 48,857 of 80,067
  bytes — **61%** — is content that only matters when the developer is
  literally standing inside that one repo. Working in `public/niwa`, the
  other six repos' `CLAUDE.md` files (tools, vision, coding-tools, tsuku,
  koto, shirabe — 39,370 bytes, ~10K tokens) are dead weight in every turn.
- **The non-repo layer is cheap.** Workspace root + instance root primary +
  three companions + both groups = 1,692 + 7,798 + 692 + 5,757 + 879 + 859 =
  **17,677 bytes ≈ 4,400 tokens**. That is small enough to load unconditionally
  on every Codex invocation with no meaningful budget concern.

A **naive full flatten is size-viable** for this workspace — 20K tokens is a
small fraction of any current context window — but it is not *signal*-viable:
three out of five repos' full guidance (tools, vision, coding-tools — all
private-visibility, tactically and topically unrelated to a niwa session) would
ride along on every single turn regardless of where the developer is working.
The bigger risk is not blowing a token budget, it's diluting the model's
attention with irrelevant repo detail and setting a precedent that scales
badly: this instance has 7 repos; a larger workspace instance could have many
more, and full-flatten cost grows linearly with total repo-content size, not
with anything Codex-session-relevant.

### 3. Per-repo context: the collision risk is real, not hypothetical

I checked whether any repo in the live instance ships its own committed
`AGENTS.md` that a naive "write `AGENTS.md` into every clone" approach would
clobber:

```
$ for d in public/* private/*; do [ -f "$d/AGENTS.md" ] && echo "FOUND: $d/AGENTS.md"; done
FOUND: public/shirabe/AGENTS.md
```

`public/shirabe/AGENTS.md` is tracked, 138 lines, last touched by commit
`8e07f07` ("feat(preflight): declare and verify each skill's host
prerequisites at load (#292)"), and carries real repo-specific instructions
(eval requirements for skill authoring, `scripts/run-evals.sh` usage). niwa's
file-write helpers used elsewhere for this kind of copy
(`writeManagedFile`/`os.WriteFile` in `content.go:1846-1869`,
`materializeVerbatimFile`) are unconditional overwrites with no
collision check. If niwa wrote `AGENTS.md` into `public/shirabe/` the way it
writes `CLAUDE.local.md` there today, it would silently destroy this file on
the next `niwa apply`. This confirms, with a concrete example inside the very
workspace under test, exactly the hazard
`DESIGN-interactive-codex-session.md` Decision 3 cites as the reason repo-level
`AGENTS.md` writes were deferred rather than shipped with a guard. **Writing
`AGENTS.md` into cloned repos should stay out of scope**, not because it's
theoretically risky, but because it would break a real file in this workspace
today.

The three options from the lead, evaluated:

- **Flatten everything (including every repo) into `$CODEX_HOME/AGENTS.md`.**
  Viable by size (Finding 2) but pushes 61% low-signal-when-elsewhere content
  onto every turn. Only worth doing with per-repo section headers explicit
  enough that the model can tell "this section is about a repo you are not
  in" — see the recommendation below.
- **Only workspace/group content in the global file; rely on each repo's own
  `AGENTS.md` for repo-level.** Doesn't work as stated: most repos in this
  workspace (6 of 7) have no `AGENTS.md` of their own, only a `CLAUDE.md`
  niwa did not touch (repo-committed) — a Codex session standing there gets
  nothing repo-specific at all unless `project_doc_fallback_filenames` is
  configured (see Finding 4). shirabe is the one repo where this option
  happens to already work today, for free, because shirabe authored its own
  `AGENTS.md`.
- **Have niwa write `AGENTS.md` into cloned repos.** Rejected — see collision
  evidence above. This matches and reinforces the already-landed design
  decision; nothing found here contradicts it.

### 4. What Codex actually ingests — verified live, not from strings

I ran `codex debug prompt-input` (codex-cli 0.147.0) against an isolated
`CODEX_HOME` under the job's tmp dir (never touched `~/.codex`), per the
safety rule. Concrete, reproducible results:

**Composition format (verified, not assumed).** With a project doc present in
both `$CODEX_HOME/AGENTS.md` and cwd's `AGENTS.md`, the model-visible prompt
carries one `developer`-role message whose text is:

```
# AGENTS.md instructions for <cwd>

<INSTRUCTIONS>
<content of $CODEX_HOME/AGENTS.md>

--- project-doc ---

<content of cwd/AGENTS.md>
</INSTRUCTIONS>
```

With only the global file present (no cwd `AGENTS.md`), the header drops the
`for <cwd>` suffix and there is no `--- project-doc ---` separator — it's just
the global content alone. Whatever niwa writes to `$CODEX_HOME/AGENTS.md`
therefore always appears **first**, and Codex's own literal
`--- project-doc ---` separator already marks the boundary between "global"
and "this directory" — a niwa-authored separator inside the global file
recommending against duplicating that exact string, since Codex's own runtime
insertion happens at the outer layer, not inside the file.

**`CLAUDE.md` is never read.** With `AGENTS.md` absent from cwd but
`CLAUDE.md` present, the composed prompt showed the global file only — no
`CLAUDE.md` content, confirming the lead's premise directly, not just by
report.

**Two config keys interact with this and are not documented in the
lead's brief — I found them in the config schema
(`strings` on the binary) and verified both live:**

- `project_doc_fallback_filenames` (`config.toml`, list of strings). When the
  cwd's `AGENTS.md` is **absent**, Codex tries each name in this list in
  cwd, first match wins, and uses it as the project-doc half of the
  concatenation shown above. Verified: with
  `-c 'project_doc_fallback_filenames=["CLAUDE.md"]'` and no `AGENTS.md` in
  cwd, the composed prompt's `--- project-doc ---` section carried the
  `CLAUDE.md` content verbatim. This is a **per-directory fallback name
  list, not a pointer to additional files by absolute path** — it does not
  give niwa a way to point Codex at extra instruction files outside
  `cwd`/`$CODEX_HOME`. I did not test what happens when both `AGENTS.md` and
  a fallback-listed file coexist in the same directory (budget did not allow
  it); the "fallback" naming and the single-file-wins shape observed strongly
  suggest first-match-only, not concatenation, but that specific case is
  unverified.
- `project_doc_max_bytes` (`config.toml`, integer). **Caps only the
  cwd-level project doc, not `$CODEX_HOME/AGENTS.md`.** Verified two ways: (1)
  a 25,012-byte cwd `AGENTS.md` passed through whole under the default cap; a
  30,014-byte `$CODEX_HOME/AGENTS.md` also passed through whole. (2) Setting
  `-c project_doc_max_bytes=100` truncated the **cwd** doc to ~100 bytes but
  left the 30,014-byte **global** file completely untouched (byte count of
  the composed prompt text was identical, 30,069, with and without the
  override). **This is the single most load-bearing finding for this lead's
  question**: `$CODEX_HOME/AGENTS.md` is effectively unbounded by the setting
  that bounds everything else Codex reads from a directory. It is the correct
  target for an eagerly-flattened, potentially-large composition — a
  per-directory project doc is the wrong place to put a large flatten because
  a future or differently-configured Codex install could silently truncate
  it.

I did not find, in the config schema or in `codex --help`, any key that lets
`$CODEX_HOME/config.toml` name additional instruction files by absolute path
outside cwd and `$CODEX_HOME` itself. The only two levers are the fallback
filename list (still cwd-scoped) and the byte cap (cwd-scoped only). This
closes the lead's question 4: there is no config-driven way around the
two-source (cwd, `$CODEX_HOME`) limit; niwa's only lever is what it writes
into those two places.

### 5. Freshness: re-materialization triggers, and what actually goes stale

Grepped every call site of the four write functions from Finding 1
(`InstallWorkspaceContent`, `InstallGroupContent`, `InstallRepoContent`,
`MaterializeWorkspaceRoot`) across `internal/cli/`. They are reached only
from: `niwa init` (`init.go:785`), `niwa apply` (`apply.go:245` plus the
apply pipeline `Applier.runPipeline` that calls the content installers),
`niwa create` (which runs the same apply pipeline), `niwa reset` (runs
`runPipeline`), and the worktree lifecycle (`ApplyToWorktree`). All five are
explicit, developer-invoked commands.

The one thing that looked like it might be an implicit trigger — the
ephemeral-session `SessionStart` hook (`internal/cli/instance_from_hook.go:150-230`,
`runInstanceHookStart`) — is not one for an *existing* instance. Reading it:
it validates the session ID, resolves the workspace root, checks a
three-part guard, then calls `provisionInstanceFunc` (`niwa create`'s
provisioning path) to create a **brand-new** instance for that session and
writes a session→instance mapping. It never re-applies an already-materialized
instance. There is no watcher, no file-hash check, and no periodic refresh
anywhere in the codebase I found.

Consequence for a composed `$CODEX_HOME/AGENTS.md`: because composition would
happen eagerly (concatenating already-rendered bytes at apply time, per this
lead's framing), it goes stale the moment any of its inputs change without a
following `niwa apply`. Concretely, in this workspace, editing
`public/niwa/CLAUDE.md` (a file checked into the niwa repo itself, which a
`git pull` inside the clone would also update) would not reach a
previously-materialized `$CODEX_HOME/AGENTS.md` until the developer re-runs
`niwa apply`. Claude does not have this problem today because its composition
is lazy (walked at read time from wherever the session's cwd is); a composed
Codex file inverts that into eager, snapshot semantics, and the more inputs
get folded into the flatten (repo content especially, being the part most
likely to change via `git pull`), the more surface area there is for silent
staleness. This is a real, not theoretical, cost of choosing eager
composition, and should be called out to the developer (e.g., a one-time
notice, or documentation) rather than left implicit.

## Implications

**The composed `$CODEX_HOME/AGENTS.md` should carry the workspace-root
template, the instance-root primary content, and the three companion layers
(`workspace-context.md`, `CLAUDE.overlay.md`, `CLAUDE.global.md` when
present) in full, plus a lightweight index of the group and repo tree — not
full per-repo bodies.** Concretely:

1. **Include, verbatim, with a section header naming its source path:**
   workspace-root content, instance-root primary content,
   `workspace-context.md`, `CLAUDE.overlay.md` (if present),
   `CLAUDE.global.md` (if present). This is the exact set the landed design
   left out (Finding 1) and is cheap (≈4,400 tokens total, Finding 2) — no
   selectivity argument applies to this layer.
2. **Include group-level content in full too** (879 + 859 bytes here) — same
   reasoning, negligible cost, always at least plausibly relevant since a
   Codex session's cwd is always under exactly one group.
3. **Do not inline full per-repo `CLAUDE.md`/`CLAUDE.local.md` bodies.** That
   is 61% of the flatten (Finding 2) and is exactly the part that is
   irrelevant on every turn except the (usually one) repo the developer is
   actually in. Instead emit a short per-repo index — name, group, one-line
   purpose if cheaply derivable, and (this is the actionable part) an
   explicit note that fuller repo guidance is not present in this file. This
   mirrors what `workspace-context.md`'s `## Repos` section already does
   structurally (`root_materializer.go:582-591`) — extend that pattern rather
   than inventing a new one.
4. **Pair this with `project_doc_fallback_filenames`, not as a substitute for
   it.** Since Codex never reads `CLAUDE.md`/`CLAUDE.local.md` from cwd
   without help, and niwa already writes `CLAUDE.local.md` at the repo level
   under Claude (a filename that is git-ignored and never collides with a
   repo's own file, unlike `AGENTS.md`), configuring
   `$CODEX_HOME/config.toml`'s `project_doc_fallback_filenames` to include
   `CLAUDE.local.md` would let a Codex session standing inside a repo pick up
   that repo's own content automatically via cwd-level ingestion — no
   collision risk (verified none of `CLAUDE.local.md`'s existing users are
   named `AGENTS.md`), and it stays wrong-but-safe for the one repo
   (shirabe) that already ships its own `AGENTS.md`, since Codex prefers the
   literal `AGENTS.md` name and only consults the fallback list when it is
   absent. This closes most of the gap Finding 3's rejected options leave
   open, without writing into a repo's git tree at all. This is a config.toml
   materialization decision, out of this lead's direct scope, but it directly
   changes what the composed `AGENTS.md` needs to carry (less, if this ships)
   and should be decided alongside it, not after.
5. **Delimit sections with plain markdown headers naming their scope
   explicitly** — e.g. `## Workspace` / `## Group: public` /
   `## Repos (index only — cd into a repo for its own guidance)` — because
   Codex has no structural equivalent of "this applies only when you're
   here"; the model has to infer scope from prose, so the prose has to say it.
   Do not reuse Codex's own `--- project-doc ---` string as a section
   delimiter (Finding 4) to avoid a confusing double-delimiter when Codex's
   own concatenation later wraps this file inside its own `<INSTRUCTIONS>`
   block.
6. **Put the flatten in `$CODEX_HOME/AGENTS.md`, not a per-directory project
   doc, specifically because it is exempt from `project_doc_max_bytes`**
   (Finding 4) — this is the one property that makes "compose eagerly into
   one file" a safe choice at all, independent of how large the flatten
   eventually grows.
7. **Document the staleness contract** (Finding 5): a one-line note in the
   composed file's own header (e.g. "materialized by `niwa apply` at
   <timestamp>; re-run `niwa apply` after editing any source content") costs
   nothing and sets the right expectation, since niwa has no live-refresh
   mechanism today and none is in scope here.

## Surprises

- **The composed `$CODEX_HOME/AGENTS.md` is net-new work, not a rename.** I
  initially expected the landed `interactive-codex-session` slice to have
  produced *some* form of this file, since it already threads an `Agent`
  discriminator through every write site. It doesn't — it renames the
  existing per-directory `CLAUDE.md`/`AGENTS.md` sites and explicitly declines
  to compose anything (its own words, quoted in Finding 1). Nothing writes to
  `$CODEX_HOME` today.
- **`project_doc_max_bytes` does not apply to the global file.** I expected
  either both files to share one cap or neither to have one; finding that
  Codex draws this exact distinction (bounded cwd doc, unbounded global doc)
  is a strong, unsolicited signal from Codex's own design about where a large
  composed file belongs.
- **A real collision already exists in the workspace under test.**
  `public/shirabe/AGENTS.md` is not a hypothetical example constructed to
  make a point — it's a real, recently-committed file with real content that
  the rejected "write AGENTS.md into every clone" option would destroy today
  in this exact instance.
- **`project_doc_fallback_filenames` is a plausible, low-risk path to
  per-repo Codex context that sidesteps the collision problem entirely**,
  using a filename (`CLAUDE.local.md`) niwa already writes for a different
  agent, that already carries no collision risk of its own (it's a
  synthesized, gitignored name). This wasn't in the lead's list of options
  and is worth surfacing to whichever design owns `config.toml`
  materialization (per the explore scope, that's lead 8,
  lead-config-materialization).

## Open Questions

- **Does niwa track what fed a composed `$CODEX_HOME/AGENTS.md`, the way
  `ManagedFiles`/`SourceFingerprint` tracks other materialized output**
  (`internal/workspace/materialize.go:97-110`, `SourceTuples`)? If so, a
  future increment could detect staleness (a source file's hash changed since
  last compose) and warn, rather than relying on the developer to remember to
  re-apply. This lead did not investigate the `ManagedFiles`/fingerprint
  machinery in depth — that's adjacent to lead-current-state's scope.
- **Does `project_doc_fallback_filenames` concatenate when both `AGENTS.md`
  and a fallback-listed file exist in the same directory, or is it strictly
  first-match?** Unverified (Finding 4) — matters for whether the
  `CLAUDE.local.md`-fallback idea in the Implications section is safe to
  build on for repos like shirabe that already have their own `AGENTS.md`.
- **Should the flatten include worktree-level content when a worktree is
  active?** Out of this lead's scope (owned by lead-worktrees), but the
  answer changes whether "repo-level index" in the Implications section needs
  a worktree variant.
- **What is the default value of `project_doc_max_bytes`?** Established only
  that it's higher than 25KB (an unmodified 25,012-byte file passed through);
  the exact default wasn't pinned down and would matter if niwa ever also
  wants to widen what it writes as a **per-repo** project doc (distinct from
  the global flatten this lead scoped).

## Summary

Nothing in niwa composes context into one file today — the landed
`interactive-codex-session` slice only renames `CLAUDE.md` to `AGENTS.md` at
three separate per-directory sites and explicitly deferred inlining the
`@import`-only companion layers, so a composed `$CODEX_HOME/AGENTS.md` is new
work, not a rename. The right shape is to inline the workspace/instance/group
layers and the three companion files in full (cheap, ~4,400 tokens combined,
verified live) while keeping full per-repo content (61% of a naive flatten,
~80KB/~20K tokens total) out of the global file in favor of a short index,
because I confirmed both that a real repo in this workspace
(`public/shirabe/AGENTS.md`) would be clobbered by writing `AGENTS.md` into
clones, and that `$CODEX_HOME/AGENTS.md` — unlike a per-directory project doc
— is exempt from Codex's `project_doc_max_bytes` cap, which is the property
that makes eager, global composition the right target in the first place. The
biggest open question is whether `project_doc_fallback_filenames` (a verified,
previously-unlisted config key) can safely deliver per-repo content via the
already-written, collision-free `CLAUDE.local.md` instead of ever inlining
repo bodies into the global file at all — that would reshape how thin the
composed `AGENTS.md` can stay, and it belongs alongside whoever decides
`config.toml` materialization.
