---
status: Current
problem: |
  niwa cannot materialize a verbatim-named, non-gitignored file at the two
  non-repo levels of a workspace -- the workspace root and each instance root.
  The per-repo [files] materializer forces a .local infix and runs only inside
  repos; [instance.files] is parsed and merged but never read by either non-repo
  materialization path; and no workspace-root file table exists in the schema.
decision: |
  Add a verbatim (no .local) file-copy path for the two non-repo levels: activate
  [instance.files] through a dedicated effective field materialized by the
  instance-root path, and add a new [root.files] table materialized by the
  workspace-root path. Both reuse a shared copy core whose rename step is
  pluggable, leaving the per-repo .local strategy untouched. Instance-root files
  join the existing ManagedFiles tracking for drift and cleanup; workspace-root
  files are overwrite-idempotent like the other root-managed files.
rationale: |
  Activating the dead field rather than inventing a parallel one keeps existing
  config valid; a dedicated effective field avoids the existing conflation where
  effective.Files blends repo [files] with [instance.files]. A pluggable rename
  step adds verbatim distribution without changing repo behavior. Reusing the
  instance ManagedFiles store gives cleanup for free; the workspace root has no
  such store today, so its files follow the existing overwrite-idempotent model.
upstream: docs/prds/PRD-mcp-root-instance-distribution.md
---

## Status

Current

## Context and Problem Statement

niwa materializes managed configuration at three levels. The repos it clones
get a general file-copy surface (`[files]`), per-repo context, settings, env,
and hooks. The two non-repo container levels -- the workspace root (the parent
directory holding `.niwa/workspace.toml` and the instance subdirectories) and
each instance root (a managed sandbox holding cloned repos as subdirectories) --
get workspace context and a `.claude/settings.json`, but no general file
distribution. This feature adds verbatim file distribution to those two non-repo
levels so a tool config that loads by an exact filename, such as a Claude Code
project `.mcp.json`, can be delivered there.

### Re-verification of the prior findings

The five findings that motivated this work were re-checked against the current
source (worktree of `main`). All five hold; one additional design-critical
nuance was found.

1. **`[files]` forces `.local` and targets repos only -- CONFIRMED.** The sole
   `FilesMaterializer` lives at `internal/workspace/materialize.go:1106` and is
   registered only in the per-repo sets (`defaultRepoMaterializers` and
   `worktreeRepoMaterializers`) in `internal/workspace/worktree_content.go:495`
   and `:509`. For an explicit destination filename it calls `injectLocalInfix`
   (`materialize.go:51`), which rewrites `.mcp.json` -> `.mcp.local.json`
   (`materializeFile`, `materialize.go:1220`-`1235`). `localRename`
   (`materialize.go:1092`) inserts `.local` before the extension. The infix
   exists so files match the `*.local*` pattern managed repos gitignore.

2. **`[instance.files]` is dead -- CONFIRMED.** `InstanceConfig.Files`
   (`internal/config/config.go:244`) is parsed and merged by
   `MergeInstanceOverrides` (`internal/workspace/override.go:240`-`249`), but the
   two non-repo materialization paths -- `writeRootSettings`
   (`internal/workspace/root_materializer.go`) and `InstallWorkspaceRootSettings`
   (`internal/workspace/workspace_context.go:242`) -- forward only
   `effective.Claude.Settings`, `effective.Plugins`, and
   `effective.Claude.Marketplaces` into `buildSettingsDoc`. Neither reads any
   files field. Nothing materializes it.

3. **No workspace-root files table -- CONFIRMED.** `WorkspaceConfig`
   (`internal/config/config.go:222`-`238`) carries `Files` (repo-level) and an
   `Instance InstanceConfig` (with its own `Files`); there is no table whose
   target is the workspace root.

4. **Non-repo levels don't need the infix -- CONFIRMED.** The instance root is a
   non-git directory; `EnsureInstanceGitignore`
   (`internal/workspace/gitignore.go:33`) writes a `*.local*` pattern into an
   instance-root `.gitignore` only so an *outer* tree the instance may be nested
   in inherits the exclusion (`gitignore.go:16`-`32`). The container directory
   itself is not a tracked repo, so there is no repo gitignore for a `.local`
   infix to satisfy at these levels.

5. **niwa's own design intended verbatim names -- CONFIRMED divergence.**
   `docs/designs/current/DESIGN-file-distribution.md` ("Decision Outcome", lines
   170-179) states "Explicit destination filenames bypass renaming" and (lines
   156-159) names "tool-specific config files that must have exact names" as the
   case the opt-out protects. The implementation diverged: `materializeFile`
   injects `.local` even for explicit filenames unless the author pre-types
   `.local` themselves, defeating the opt-out. This feature does not "fix"
   `materializeFile` (the repo level legitimately wants `.local`); it adds
   verbatim distribution at the levels where the infix has no purpose.

**Additional nuance (design-critical).** `MergeInstanceOverrides`
(`internal/workspace/override.go:151`-`168`) seeds `EffectiveConfig.Files` from
`copyStringMap(ws.Files)` -- the **repo-level** `[files]` table -- and then
merges `ws.Instance.Files` on top (`override.go:240`-`249`). So
`effective.Files` at the non-repo paths is a blend of repo `[files]` and
`[instance.files]`. This conflation is currently harmless only because nothing
reads `effective.Files` there. Activating instance-root materialization by
reading `effective.Files` directly would wrongly distribute repo-targeted
`[files]` entries verbatim at the instance root. The design must therefore route
instance-root distribution through a field sourced **only** from
`[instance.files]`.

### Why the verbatim name matters

Claude Code loads a project MCP config from `.mcp.json` in the directory a
session starts in. Renamed to `.mcp.local.json` it is never read. Delivering the
file under its exact name at the workspace root and instance root is the whole
point of the feature.

## Decision Drivers

- The per-repo `[files]` `.local` behavior must not change (PRD R8): it is
  correct for tracked repo working trees.
- Existing config that already sets `[instance.files]` must stay valid -- the
  fix is to give the field an effect, not to rename it.
- The conflation in `effective.Files` must not leak repo entries into the
  instance root.
- Reuse existing materialization plumbing (`MaterializeContext`, source
  recording, `written`-path tracking) rather than building a parallel mechanism.
- Source-path containment must be enforced exactly as the existing materializers
  do (PRD R9).
- One reviewable PR (single-PR plan).

## Considered Options

### Decision 1: How to distribute to the instance root

#### Chosen: Activate `[instance.files]` via a dedicated effective field

Add `EffectiveConfig.InstanceFiles map[string]string`, populated in
`MergeInstanceOverrides` from `ws.Instance.Files` **only** (not seeded from
`ws.Files`). `InstallWorkspaceRootSettings` materializes `InstanceFiles`
verbatim into `instanceRoot` and appends the written paths to the `written`
slice it already returns, so they join `ManagedFiles` tracking.

This keeps `[instance.files]` as the author-facing surface (no config break),
and the dedicated field sidesteps the `effective.Files` conflation: repo
`[files]` entries never reach the instance root.

#### Alternatives considered

**Materialize the existing `effective.Files` at the instance root.** Smallest
change, but it would distribute repo-targeted `[files]` entries (meant for repos
with `.local`) verbatim into the instance root. Rejected: wrong files, wrong
naming.

**Invent a new instance table (e.g. `[instance_files]`).** Orphans the existing
`[instance.files]` field and any config already using it. Rejected: the field
already exists and is documented as the instance-root override surface
(`config.go:246`-`248`).

### Decision 2: How to distribute to the workspace root

#### Chosen: Add a `[root.files]` table

Add `RootConfig struct { Files map[string]string }` and a `Root RootConfig`
field on `WorkspaceConfig` (`[root.files]` in TOML), surfaced as
`EffectiveConfig.RootFiles` (or read directly from `cfg.Root.Files` by the
workspace-root path). `MaterializeWorkspaceRoot` / `writeRootSettings`
materialize it verbatim into `workspaceRoot`.

`[root.files]` reads naturally against the existing `[instance.files]`: instance
files target each instance root, root files target the workspace root.

#### Alternatives considered

**`[workspace.files]` on `WorkspaceMeta`.** The `[workspace]` table holds
metadata (name, version, default_branch). Adding distribution there conflates
metadata with materialization. Rejected.

**Reuse the top-level `[files]` for the root too.** That table is repo-scoped
and `.local`-rewritten; overloading it with a verbatim root meaning would make
one table mean two contradictory things by context. Rejected.

### Decision 3: Naming behavior at the non-repo levels

#### Chosen: Verbatim (no infix), via a pluggable rename step

Factor the per-file copy core out of `FilesMaterializer.materializeFile` into a
shared helper that takes a `rename func(string) string` strategy. The repo path
passes the existing `.local` strategy (`injectLocalInfix` / `localRename`); the
two non-repo paths pass an identity strategy (verbatim). Behavior for repos is
byte-for-byte unchanged; existing repo file tests must still pass.

#### Alternatives considered

**Always insert `.local` (status quo).** Breaks every tool config that needs an
exact name -- the original problem. Rejected.

**Per-entry opt-out at the non-repo level.** The non-repo levels never want
`.local` (no repo gitignore to satisfy), so a per-entry toggle is unused
complexity. Rejected: verbatim is the only sensible behavior there.

### Decision 4: Tracking, drift, and cleanup

#### Chosen: Reuse instance ManagedFiles; workspace root stays overwrite-idempotent

Instance-root files are appended to the `written` slice
`InstallWorkspaceRootSettings` returns; that slice already flows into the
instance's `ManagedFiles` (`apply.go:1283`), so drift detection and
`cleanRemovedFiles` (`apply.go:1619`) apply with no new infrastructure --
removing an `[instance.files]` entry and re-applying deletes the file.

The workspace root has no managed-file state store: `MaterializeWorkspaceRoot`'s
return value is discarded by its callers (`internal/cli/apply.go:196`,
`internal/cli/init.go:767`), and its other outputs (`settings.json`, `CLAUDE.md`,
root skills) are overwrite-idempotent and never auto-removed. Workspace-root
distributed files follow that same model: rewritten every apply, no removal
cleanup. Removing a `[root.files]` entry leaves the previously written file in
place until manually deleted.

#### Alternatives considered

**Build a workspace-root managed-file store now.** Would give the workspace root
the same removal-cleanup as the instance root, but no such store exists and the
other root-managed files don't have one either; adding it is a separate,
larger change. Rejected for this PR; recorded as a follow-up in Consequences.

### Decision 5: MCP trust prompt (secondary consideration)

#### Chosen: Out of scope for code; `permissions = "bypass"` covers the case

`buildSettingsDoc` honors only the `permissions` key from `[claude.settings]`
(`materialize.go:390`), mapping `"bypass"` -> `defaultMode: bypassPermissions`
and `"ask"` -> `askPermissions` (`permissionsMapping`, `materialize.go:249`);
any other key (e.g. `enableAllProjectMcpServers`) is silently dropped. A
workspace that sets `permissions = "bypass"` runs the root and instance sessions
in `bypassPermissions` mode, under which Claude Code does not gate project MCP
servers behind a per-session trust prompt. That covers the motivating case
without new niwa code.

Emitting a dedicated `enableAllProjectMcpServers` (or `enabledMcpjsonServers`)
setting would require extending `buildSettingsDoc`'s honored-key set, which
changes settings semantics for every level that calls it. That is a separate
concern with its own design; it is recorded as an explicit follow-up, not built
here.

## Decision Outcome

niwa gains verbatim file distribution at the two non-repo levels. Authors
declare instance-root files under the now-live `[instance.files]` and
workspace-root files under a new `[root.files]`. A shared copy core with a
pluggable rename step distributes them verbatim while the per-repo
`FilesMaterializer` keeps its `.local` strategy unchanged. Instance-root files
are tracked and cleaned through the existing `ManagedFiles` machinery;
workspace-root files are overwrite-idempotent like the other root-managed files.
The `enableAllProjectMcpServers` question is deferred; `permissions = "bypass"`
is the supported way to auto-load the delivered MCP server today.

## Solution Architecture

### Config example (the motivating case)

To deliver the same `.mcp.json` to the workspace root and every instance root,
an author adds two tables to `workspace.toml`. The key is the source path
(relative to the `.niwa` config dir); the value is the verbatim destination (an
explicit filename, no trailing slash, so no `.local` is inserted):

```toml
# .niwa/workspace.toml

# Workspace root (the parent dir holding the instance subdirectories).
[root.files]
"mcp.json" = ".mcp.json"

# Every instance root (the now-live table).
[instance.files]
"mcp.json" = ".mcp.json"

# So a session at either level auto-loads the project MCP server without a
# per-session trust prompt. permissions = "bypass" maps to Claude Code's
# bypassPermissions mode (see Decision 5); it is the supported way to suppress
# the prompt today. Drop this block if a trust prompt is acceptable.
[claude.settings]
permissions = "bypass"
```

Given a source file `mcp.json` in the config dir, `niwa apply` produces
`<workspaceRoot>/.mcp.json` and `<eachInstanceRoot>/.mcp.json` verbatim (and
`niwa create` produces it at a new instance root). The source is named
`mcp.json` rather than `.mcp.json` only to keep it a visible template in the
config repo; mapping `".mcp.json" = ".mcp.json"` works identically.

The `[claude.settings]` block is independent of the file tables: it is the
existing settings surface, shown here because the trust-prompt behavior
(Decision 5) is part of making the *delivered* file useful. A workspace that
already runs in `bypassPermissions` mode needs no addition.

### Components

**`config.RootConfig`** (`internal/config/config.go`) -- new
`struct { Files map[string]string \`toml:"files,omitempty"\` }`, referenced by a
new `Root RootConfig \`toml:"root,omitempty"\`` field on `WorkspaceConfig`. This
is the `[root.files]` surface.

**`EffectiveConfig`** (`internal/workspace/override.go:17`) -- gains
`InstanceFiles map[string]string` and `RootFiles map[string]string`.
`MergeInstanceOverrides` populates `InstanceFiles` from `ws.Instance.Files`
(empty-string entries dropped, mirroring the existing removal semantics) and
`RootFiles` from `ws.Root.Files`. The existing `Files` field and its conflated
seeding are left untouched to avoid disturbing any current reader.

**Shared copy core** (`internal/workspace/materialize.go`) -- the per-file copy
body of `materializeFile` (read source, containment-check source, resolve
target via a rename strategy, containment-check target, mkdir, write,
`recordSources`) is extracted into a helper parameterized by a
`rename func(base string) string` and the source/target roots. The directory
walk in `materializeDir` is similarly parameterized. `FilesMaterializer` calls
the helper with the `.local` strategy; a new verbatim entry point calls it with
identity.

**`materializeVerbatimFiles(ctx *MaterializeContext, files map[string]string)`**
(`internal/workspace/materialize.go`) -- iterates `files` (sorted, empty-dest
skipped), copies each source to `ctx.RepoDir` verbatim using the shared core,
records sources, and returns written paths. Reused by both non-repo paths
(`ctx.RepoDir` is the workspace root or instance root respectively).

### Call sites

- **Instance root:** `InstallWorkspaceRootSettings`
  (`workspace_context.go:242`) already builds a `MaterializeContext` with
  `RepoDir: instanceRoot`. After writing settings, it calls
  `materializeVerbatimFiles(mctx, effective.InstanceFiles)` and appends the
  result to `written`. Tracking and cleanup come for free.

- **Workspace root:** `MaterializeWorkspaceRoot` / `writeRootSettings`
  (`root_materializer.go`) build a `MaterializeContext` with
  `RepoDir: workspaceRoot` (constructed locally for this purpose) and call
  `materializeVerbatimFiles(mctx, effective.RootFiles)`, appending to the
  `written` slice `MaterializeWorkspaceRoot` returns. The callers still discard
  that slice today, so these files are overwrite-idempotent (Decision 4).

### Data flow

```
workspace.toml
  [instance.files] ──► MergeInstanceOverrides ──► EffectiveConfig.InstanceFiles
  [root.files]     ──► MergeInstanceOverrides ──► EffectiveConfig.RootFiles

InstallWorkspaceRootSettings(instanceRoot)
  └─ materializeVerbatimFiles(ctx{RepoDir: instanceRoot}, InstanceFiles)
       └─ shared copy core (identity rename) ─► <instanceRoot>/<dest> (verbatim)
       └─ written ─► ManagedFiles ─► drift + cleanRemovedFiles

MaterializeWorkspaceRoot(workspaceRoot)
  └─ materializeVerbatimFiles(ctx{RepoDir: workspaceRoot}, RootFiles)
       └─ shared copy core (identity rename) ─► <workspaceRoot>/<dest> (verbatim)
       └─ written (discarded by caller; overwrite-idempotent)
```

### Destination boundary

Destinations resolve under the level's project root (PRD R7). `checkContainment`
(reused from the existing materializers) rejects any destination escaping the
target root. The feature does not special-case `.claude/` or `.niwa/`; authors
are expected to target the project root (e.g. `.mcp.json`), and the design
documents that protected internal directories are not the intended target.

## Implementation Approach

### Phase 1: Config and merge

- Add `RootConfig` and `WorkspaceConfig.Root`; add `InstanceFiles` and
  `RootFiles` to `EffectiveConfig`.
- Populate both in `MergeInstanceOverrides` from their own tables, with
  empty-string removal. Leave the existing `Files` seeding alone.
- Unit tests: `[instance.files]` and `[root.files]` parse and surface on the
  effective config; repo `[files]` does **not** appear in `InstanceFiles` or
  `RootFiles` (guards the conflation fix).

### Phase 2: Shared copy core and verbatim materialization

- Extract the per-file copy core and the directory walk from `materializeFile` /
  `materializeDir` into rename-strategy-parameterized helpers; rewire
  `FilesMaterializer` to pass the `.local` strategy. Confirm existing repo file
  tests pass unchanged.
- Add `materializeVerbatimFiles` using the identity strategy.
- Unit tests: verbatim single-file and directory-source copy (no `.local`),
  empty-dest removal, source path-traversal rejection, destination containment.

### Phase 3: Wire the two non-repo paths

- Call `materializeVerbatimFiles` in `InstallWorkspaceRootSettings` (append to
  `written`) and in `MaterializeWorkspaceRoot` / `writeRootSettings`.
- Update the workspace.toml scaffold/template and docs to mention `[root.files]`
  and the now-live `[instance.files]`.
- Unit tests: instance-root file appears in `ManagedFiles` and is removed by
  `cleanRemovedFiles` when its entry is dropped; workspace-root file is written
  verbatim.
- Functional `@critical` scenario (per the repo's testing convention): a
  workspace declaring `.mcp.json` under `[instance.files]` and `[root.files]`
  produces verbatim `.mcp.json` at the instance root (and workspace root) after
  `apply`, and at a newly created instance root after `create`.

## Security Considerations

Source paths for distributed files are validated with `checkContainment`
against the config directory -- the same boundary the existing `FilesMaterializer`
and env/content materializers enforce -- so `..` traversal and symlink escapes
are rejected before any file is read. Destination paths are containment-checked
against the target root (workspace root or instance root), so a malicious or
mistaken destination cannot write outside the intended level.

File distribution copies bytes verbatim: no template expansion and no shell
execution, so the attack surface is the already-trusted config repo (the user
chose to clone it). Distributed files are written with the same restrictive file
mode the existing materializers use, so a delivered `.mcp.json` is not
world-readable by default.

The feature does not relax any permission posture on its own. The only
permission-related interaction is documentation: a workspace that opts into
`permissions = "bypass"` already runs in `bypassPermissions` mode; this feature
does not introduce that mode, it only notes that the mode is what lets a
delivered MCP server load without a prompt.

## Consequences

### Positive

- A workspace-wide tool config (the motivating `.mcp.json`) is delivered by
  config alone to the workspace root and every instance root, verbatim, with no
  per-instance hand-copying.
- `[instance.files]` stops being a dead field; existing config that set it now
  has an effect.
- The `effective.Files` conflation is sidestepped by dedicated fields, removing
  a latent landmine for any future reader of that map at the non-repo levels.
- The per-repo `.local` behavior is untouched; the shared copy core keeps repo
  distribution byte-for-byte the same.
- Instance-root files get drift detection and cleanup for free from the existing
  `ManagedFiles` machinery.

### Negative

- Two naming behaviors now exist across levels (`.local` for repos, verbatim for
  non-repo levels). This is inherent to the problem -- the levels differ -- but
  it is one more rule to know.
- Workspace-root distributed files are not removal-cleaned: dropping a
  `[root.files]` entry leaves the previously written file until it is deleted by
  hand. This matches the existing root-managed-file model but is weaker than the
  instance-root behavior.
- Sessions started inside a managed repo subdirectory are not covered (Claude
  Code does not walk up to an ancestor `.mcp.json`). Accepted per the PRD.

### Mitigations

- The naming asymmetry is documented in one sentence in the config docs:
  `.local` at repo level (gitignore accommodation), verbatim at the non-repo
  levels (no repo gitignore to satisfy).
- A workspace-root managed-file store is recorded as a follow-up; until then,
  the overwrite-idempotent behavior is documented so authors know root-level
  removals need a manual delete.
- The `enableAllProjectMcpServers` honored-key extension is recorded as an
  explicit follow-up; `permissions = "bypass"` is the documented path for
  auto-loading the delivered MCP server in the meantime.
