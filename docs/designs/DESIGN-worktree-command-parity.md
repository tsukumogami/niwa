---
status: Planned
problem: |
  niwa's worktree commands (`niwa worktree create|destroy|list|attach|detach`)
  are asymmetric with the workspace-instance lifecycle (`niwa create|apply|destroy`).
  `niwa worktree create` only does `git worktree add` + a bare `.niwa/sessions/`
  scaffold; it installs none of the CLAUDE content a repo checkout gets from `niwa
  apply` (CLAUDE.local.md, repo materializers, and — for workspace context — the
  `.claude/rules/` import). There is no worktree re-sync analog of `apply`, no
  per-worktree customization surface, and the installers live in internal/workspace
  while internal/worktree is a leaf, so naive reuse risks an import cycle.
decision: |
  Make `niwa worktree create` materialize-then-install (mirroring how `niwa create`
  scaffolds then runs the apply pipeline), add `niwa worktree apply` as the
  idempotent re-sync analog, and keep `destroy/list/attach/detach`. The shared
  content-install logic lives in a new `internal/workspace` entry point
  (ApplyToWorktree) that reuses the existing installers and is orchestrated from the
  CLI layer — workspace already imports worktree, so the leaf stays a leaf. A
  worktree gets the owning repo's content + the workspace-context rules import and
  a generated worktree-specific layer (purpose/branch), customizable via a
  `[claude.content.worktree]` template and worktree lifecycle hooks.
rationale: |
  `niwa create` already equals scaffold + apply-pipeline, so the symmetric worktree
  shape (create = git-add + worktree-apply; apply = idempotent re-install) falls out
  naturally and reuses the instance installers rather than forking them. Putting the
  orchestration in internal/workspace (not the leaf internal/worktree, not ad-hoc CLI
  glue) respects the dependency direction the mesh removal established and matches the
  bootstrap precedent.
upstream: docs/prds/PRD-worktree-command-parity.md
---

# DESIGN: worktree command parity

## Status

Planned

## Upstream Design Reference

Implements PRD-worktree-command-parity (`docs/prds/PRD-worktree-command-parity.md`,
Accepted), requirements R1–R9. Tactical design in a tactical repo; no parent
strategic design. Builds on the mesh-removal change that made `internal/worktree/`
a leaf package and `niwa worktree` a first-class command group.

## Context and Problem Statement

A code-reality review established the current asymmetry precisely.

**Instance level** — `niwa create` (`internal/cli/create.go`) builds a
`workspace.Applier` and calls `Applier.Create()`, which runs the **apply pipeline**
(`internal/workspace/apply.go` `runPipeline`, ~9 ordered steps): clone repos →
`InstallWorkspaceContent` (instance `CLAUDE.md`) → `InstallWorkspaceContext` +
`InstallWorkspaceRootSettings` (`workspace-context.md`, `.claude/rules/workspace-imports.md`,
`.claude/settings.json`) → `InstallGroupContent` → `InstallGlobalClaudeContent` →
`InstallRepoContent` (per-repo `CLAUDE.local.md`) → repo materializers (settings,
env, files, hooks via `DiscoverHooks`/`DiscoverEnvFiles`) → setup scripts → state.
`niwa apply` re-runs the same pipeline idempotently. So **instance create = scaffold
+ apply-pipeline; instance apply = the pipeline alone.** A repo checkout therefore
emerges fully formed.

**Worktree level** — `niwa worktree create` (`internal/cli/session_lifecycle_cmd.go`)
calls `worktree.CreateSession` (`internal/worktree/worktree.go`), which does
`git worktree add <instanceRoot>/.niwa/worktrees/<repo>-<sid>` on a new branch and
`scaffoldWorktreeNiwa` (creates only `.niwa/sessions/`). **No CLAUDE content is
installed.** A worktree lives under the instance, so Claude Code's upward
CLAUDE.md walk reaches the instance `CLAUDE.md`, but the repo's `CLAUDE.local.md` is
a `.local`/untracked file that does not travel into a separate `git worktree`, and
`.claude/rules/` is loaded only for the launched project root — not walked-up
parents — so the worktree sees neither the repo content nor `workspace-context.md`.

**Constraints from the review:**
- `internal/worktree/` is a **leaf** (stdlib only; imports neither the deleted
  `internal/mcp` nor `internal/workspace`). `internal/workspace/` imports
  `internal/worktree/` (for `CreateSession`). Content-install orchestration must not
  invert that.
- The installers (`InstallRepoContent`, the materializers, `InstallWorkspaceContext`)
  all live in `internal/workspace/` and are written against `*config.WorkspaceConfig`
  + an instance root + a target dir. They are reusable against a worktree path.
- Precedent: `internal/cli/init.go` + `workspace.RunBootstrap` already compose
  workspace-package work with `worktree.CreateSession` via closures, without a cycle.

The problem this design solves: define the symmetric worktree command surface, the
content a worktree receives, the customization surface, and the shared-code
architecture — satisfying R1–R9 without an import cycle or a forked installer.

## Decision Drivers

- **Symmetry the contributor can predict (R4).** The worktree verbs should map onto
  the instance lifecycle where operations correspond; gaps should be deliberate.
- **Reuse, not fork (R6, R8).** Worktree content install must call the same
  installers `niwa apply` uses; instance behavior must not change.
- **Leaf stays a leaf (R9).** No import cycle: `internal/worktree/` must not import
  `internal/workspace/`.
- **A worktree is a first-class working context (R1, R2).** Parity with a repo
  checkout, plus a worktree-specific layer keyed on purpose/branch.
- **Idempotent re-sync (R3).** The `apply` analog must be safely re-runnable.
- **Scope discipline.** Identify the full surface; the PLAN phases it (create-parity
  first). Don't expand above the worktree/repo level or revive any mesh surface.

## Considered Options

### Decision A — The symmetric verb mapping

How should the worktree verb set mirror `niwa create | apply | destroy`?

**Chosen: A1 — `create` = materialize+install, add `apply` as the re-sync analog,
keep `destroy/list/attach/detach`.** The mapping:

| Instance verb | Worktree analog | Notes |
|---|---|---|
| `niwa create` (scaffold + apply-pipeline) | `niwa worktree create` (git worktree add + worktree-apply) | one-time; errors if the worktree exists |
| `niwa apply` (idempotent pipeline) | **`niwa worktree apply`** (NEW) | idempotent re-install of a worktree's content |
| `niwa destroy` | `niwa worktree destroy` | exists; add the uncommitted-work guard (Decision E) |
| `niwa list` (registry/instances) | `niwa worktree list` | exists; lists worktrees, not instances |
| — | `niwa worktree attach`/`detach` | worktree-specific; no instance analog (deliberate gap) |

This exactly mirrors the instance shape: instance `create` internally runs the apply
pipeline, so worktree `create` internally runs worktree-apply; `apply` at both levels
is the idempotent content sync. `attach`/`detach` are documented as worktree-only
(launching/locking a tool in a checkout has no instance-level meaning).

*Rejected: A2 — create-only, no `apply`.* Fold content install into `create` and add
no re-sync verb. Rejected: it breaks R3 (no idempotent re-sync after config changes)
and breaks the symmetry — the instance level's defining verb is `apply`.

*Rejected: A3 — make `create` idempotent and drop a separate `apply`.* One verb that
both creates and re-syncs. Rejected: it diverges from the instance semantics
(`create` is one-time, `apply` is the re-runnable one) — the opposite of the symmetry
goal, and it muddies "create fails if the worktree exists."

### Decision B — Where the shared content-install logic lives (import-cycle)

**Chosen: B1 — a new `internal/workspace` entry point, orchestrated from the CLI.**
Add `workspace.ApplyToWorktree(cfg, instanceRoot, worktreePath, repo, purpose, branch, ...)`
to `internal/workspace/` that reuses the existing installers/materializers against the
worktree path. The CLI commands (`niwa worktree create`/`apply`) call
`worktree.CreateSession` (or resolve an existing worktree) and then
`workspace.ApplyToWorktree`. `internal/workspace/` already imports
`internal/worktree/`, so this composes with no new dependency and the leaf stays a
leaf — the same shape as `init.go`/`RunBootstrap`.

*Rejected: B2 — extract installers into a new shared package both import.* Cleaner in
the abstract, but the installers are tightly bound to `*config.WorkspaceConfig`,
`ClassifiedRepo`, the snapshot/state model, and the apply pipeline; lifting them is a
large, risky churn for no behavioral gain.

*Rejected: B3 — `internal/worktree/` imports `internal/workspace/` and installs
content itself.* Direct import cycle (`workspace` imports `worktree`). Non-starter,
and it would re-entangle the leaf the mesh removal deliberately created.

### Decision C — What content a worktree receives

A worktree is a checkout of **one** repo, living under the instance. What does
worktree-apply install?

**Chosen: C1 — owning-repo content + the workspace-context rules import + a
worktree-specific layer.** Worktree-apply, for the worktree's repo, runs:
- `InstallRepoContent` for that repo, targeted at the worktree root (its
  `CLAUDE.local.md` + subdir content) — the parity payload.
- the repo materializers (settings, env, files, hooks) targeted at the worktree.
- a `.claude/rules/worktree-imports.md` carrying an absolute `@import` to the
  instance's `workspace-context.md` (and overlay/global), so the worktree — as its
  own launched project root — sees workspace context the way the instance root does.
  (Reuses the `writeWorkspaceRulesFile` mechanism.)
- a generated **worktree-specific layer**: a section appended to the worktree's
  `CLAUDE.local.md` (or a dedicated `.claude/rules/worktree-context.md`) naming the
  purpose and branch (R2).

*Rejected: C2 — repo content only, rely on the CLAUDE.md walk-up for workspace
context.* Simpler, but the walk-up does NOT load the instance's `.claude/rules/`
(confirmed: rules load for the launched root, not walked-up parents), so the worktree
would silently lack `workspace-context.md` — failing the "first-class context" goal.

*Rejected: C3 — copy the entire instance content tree into the worktree.* Over-broad
(group/global/other-repo content the worktree doesn't need) and duplicative; the
walk-up already supplies instance/group `CLAUDE.md`.

### Decision D — The worktree customization surface

**Chosen: D1 — a `[claude.content.worktree]` template + worktree lifecycle hooks,
mirroring the instance content/hook surfaces.** Add an optional
`[claude.content.worktree].source` template (expanded with worktree variables
`{purpose}`, `{branch}`, `{repo_name}`, `{worktree_path}` alongside the existing
`{workspace}`/`{workspace_name}`) that produces the worktree-specific layer, and a
worktree-events hook directory (analog of `DiscoverHooks`) run on
create/apply/destroy. When unset, the generated default (purpose/branch section) is
used. This is the worktree counterpart of `[claude.content.repos.*]` + the hook
discovery surface.

*Rejected: D2 — no template, hard-coded purpose/branch section only.* Meets R2's
minimum but not R5 (no customization surface); maintainers couldn't shape worktree
context without editing code.

*Rejected: D3 — reuse the repo content entry for worktrees.* Conflates two distinct
layers (the repo's durable content vs. the ephemeral worktree's purpose-specific
context) and gives no place for purpose/branch variables.

### Decision E — Destroy symmetry

**Chosen: E1 — keep worktree destroy's branch/attach-lock handling, add the
uncommitted-work guard.** `git worktree remove` already deletes the installed content
(it lives inside the worktree dir), so destroy needs no content-specific teardown.
Add the instance-parity safety check: warn on uncommitted/unpushed work in the
worktree before removal unless `--force` (mirroring `CheckUncommittedChanges`). The
worktree-specific behaviors with no instance analog (attach-lock guard, branch delete,
idempotent terminal state) are retained and documented as deliberate.

*Rejected: E2 — leave destroy unchanged.* Leaves the asymmetry the PRD flagged (no
uncommitted-work guard) — a real footgun when a worktree holds unpushed work.

## Decision Outcome

The worktree command surface becomes: `niwa worktree create` (git worktree add +
worktree-apply), `niwa worktree apply` (idempotent content re-sync), `niwa worktree
destroy` (with the uncommitted guard), plus `list`/`attach`/`detach`. Shared content
logic lives in `workspace.ApplyToWorktree`, reusing the instance installers, invoked
from the CLI alongside `worktree.CreateSession`. A worktree receives its repo's
content, a `.claude/rules/worktree-imports.md` importing workspace-context, and a
purpose/branch layer customizable via `[claude.content.worktree]` + worktree hooks.
This satisfies R1 (repo-parity content), R2 (purpose/branch layer), R3 (idempotent
`apply`), R4 (documented verb mapping), R5 (customization surface), R6/R8 (reuse, no
instance change), R7 (destroy symmetry), R9 (leaf preserved).

## Solution Architecture

### Components

- **`internal/workspace/` — new `ApplyToWorktree` entry point.** Signature roughly
  `ApplyToWorktree(ctx, cfg *config.WorkspaceConfig, instanceRoot, worktreePath,
  group, repo, purpose, branch string, opts) ([]string, error)`. Internally:
  resolves the repo's content entry and calls `InstallRepoContent` against
  `worktreePath`; runs the repo materializers; writes
  `<worktreePath>/.claude/rules/worktree-imports.md` with an absolute `@import` to
  `<instanceRoot>/workspace-context.md` (+ overlay/global) via the existing
  `writeWorkspaceRulesFile`/`appendToWorkspaceRulesFile`; renders the worktree layer
  from `[claude.content.worktree]` (or the default) with worktree template vars;
  runs worktree-event hooks. Idempotent (re-runnable), like the instance pipeline.
- **`internal/worktree/` — unchanged contract, stays a leaf.** `CreateSession`
  continues to do git-worktree-add + state; it does NOT gain content logic. (It may
  expose the worktree path/branch/purpose it produced so the CLI can pass them to
  `ApplyToWorktree`.)
- **`internal/cli/` — orchestration.** `niwa worktree create` calls
  `worktree.CreateSession` then `workspace.ApplyToWorktree`. `niwa worktree apply`
  resolves an existing worktree (from session state) and calls
  `workspace.ApplyToWorktree` only. Mirrors `init.go`/`RunBootstrap` composition.
- **`internal/config/` — `[claude.content.worktree]`.** New optional content entry
  + worktree template variables. Additive; absent = default behavior.

### Data flow: `niwa worktree create <repo> <purpose>`

1. CLI resolves instance root + loads config.
2. `worktree.CreateSession` → git worktree add at `.niwa/worktrees/<repo>-<sid>`,
   writes session state, returns `(sid, worktreePath, branch)`.
3. CLI calls `workspace.ApplyToWorktree(cfg, instanceRoot, worktreePath, group,
   repo, purpose, branch)` → installs repo content + materializers + worktree rules
   import + worktree-specific layer.
4. On failure after step 2, the existing CreateSession rollback applies; ApplyToWorktree
   failures are surfaced (and re-runnable via `niwa worktree apply`).

`niwa worktree apply <session-id>` runs steps 1 + 3 against the existing worktree.

### Import-cycle analysis

`internal/workspace/` imports `internal/worktree/` (existing). `ApplyToWorktree` lives
in `internal/workspace/`, so it freely reuses the installers and may reference
worktree types. `internal/cli/` imports both. `internal/worktree/` imports neither —
unchanged leaf. No cycle.

## Implementation Approach

Phased so a usable slice lands first; the PLAN sequences these (not all necessarily
in the current branch).

**Phase 1 — create-parity (the load-bearing slice).** Add `workspace.ApplyToWorktree`
reusing `InstallRepoContent` + repo materializers + the workspace-context rules
import; wire `niwa worktree create` to call it after `CreateSession`; generate the
default purpose/branch layer. Acceptance: a created worktree has the repo's
`CLAUDE.local.md` + `.claude/` accessories + workspace-context visibility + a
purpose/branch section. Functional test mirroring the repo-apply assertions.

**Phase 2 — `niwa worktree apply`.** Add the command; resolve an existing worktree
from session state; call `ApplyToWorktree` idempotently. Acceptance: re-running
produces no spurious changes; updates content after a config change.

**Phase 3 — customization surface.** Add `[claude.content.worktree]` + worktree
template variables + worktree-event hook discovery. Acceptance: a configured template
shapes the worktree layer; a worktree hook runs on create/apply.

**Phase 4 — destroy symmetry.** Add the uncommitted-work guard to `niwa worktree
destroy` (mirroring `CheckUncommittedChanges`), `--force` to bypass.

Verification across phases: `go build/vet/test ./...`; instance-command behavior
unchanged (R8); `internal/worktree/` still a leaf (R9).

## Security Considerations

- **Template expansion / path containment.** The worktree layer expands variables
  (`{purpose}`, `{branch}`, `{worktree_path}`) into generated content. `purpose` is
  user-supplied. Reuse the existing `installContentFile`/`checkContainment`
  symlink-aware containment checks so a crafted source/template cannot write outside
  the worktree; treat `purpose` as data interpolated into file *content* (not a path
  component) — the worktree dir name already derives from a sanitized sid, not raw
  purpose. **Mitigation:** route all worktree-content writes through the existing
  containment-checked installer; do not interpolate `purpose` into filesystem paths.
- **Hook execution.** Worktree-event hooks execute scripts, like instance hooks.
  Same trust model as the existing `DiscoverHooks` surface (scripts come from the
  workspace config repo the operator already trusts). **Mitigation:** reuse the
  existing hook-install path and its provenance; no new external input.
- **No new network/secret surface.** ApplyToWorktree reuses the instance installers;
  vault/secret resolution is unchanged and instance-scoped. No agent-facing surface
  is introduced (the mesh stays removed).
- **Reduced-risk note.** This is additive reuse of audited installers against a new
  target path; it introduces no new deserialization, listener, or auth surface.

## Consequences

### Positive

- A worktree is a first-class CLAUDE working context at parity with a repo checkout,
  plus purpose/branch orientation — no manual setup for agents launched there.
- The worktree and instance levels share one installer path; they cannot drift.
- The command surface is predictable: `create`/`apply`/`destroy` mean the same shape
  at both levels.

### Negative / trade-offs

- `workspace.ApplyToWorktree` adds a second public entry into the install machinery;
  it must stay a thin reuse of the pipeline steps, not a divergent copy (enforced by
  review + the R6 acceptance test that asserts shared code paths).
- A new `[claude.content.worktree]` config surface is additional API to document and
  maintain. Mitigated by making it optional with a sensible default.
- Worktree-apply re-running materializers/hooks must be genuinely idempotent;
  non-idempotent hooks are an operator concern (documented), as they already are for
  instance apply.

### Neutral

- The worktree path and branch model are unchanged; this design only adds content
  installation on top of the existing `CreateSession`.
- `attach`/`detach` remain worktree-only with no instance analog — a documented,
  intentional asymmetry, not a gap to fill.
