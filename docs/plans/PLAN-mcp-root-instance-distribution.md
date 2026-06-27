---
schema: plan/v1
status: Active
execution_mode: single-pr
upstream: docs/designs/current/DESIGN-mcp-root-instance-distribution.md
milestone: "MCP root/instance file distribution"
issue_count: 3
---

# PLAN: MCP root/instance file distribution

## Status

Active

## Scope Summary

Add verbatim (no `.local`) file distribution at the two non-repo levels --
the workspace root via a new `[root.files]` table and each instance root via
the now-live `[instance.files]` -- so a tool config like `.mcp.json` lands under
its exact name. The per-repo `[files]` `.local` behavior is untouched. The whole
change is one reviewable PR.

## Decomposition Strategy

**Horizontal, layered.** The change has three layers with a clear seam: the
config/merge layer (new fields), the copy layer (a verbatim materialization that
reuses a refactored copy core), and the wiring layer (calling the verbatim copy
from the two non-repo materialization paths, plus scaffold/docs and end-to-end
tests). The config layer (I1) and the copy-core layer (I2) are independent of
each other and can be built in parallel; the wiring and tests (I3) need both, so
it is the integrating gate. This is single-PR work: the issues are sequencing
units for one branch, not separate deliverables.

## Issue Outlines

### Issue 1: feat(config): add [root.files] and surface instance/root files on the effective config

**Complexity**: testable

**Goal**: Add a `RootConfig{ Files map[string]string }` and a
`Root RootConfig` field (`[root.files]`) to `WorkspaceConfig` in
`internal/config/config.go`; add `InstanceFiles` and `RootFiles
map[string]string` to `EffectiveConfig` in `internal/workspace/override.go`; and
populate them in `MergeInstanceOverrides` from `ws.Instance.Files` and
`ws.Root.Files` respectively, with empty-string entries dropped. Leave the
existing `EffectiveConfig.Files` seeding (from `ws.Files` + `ws.Instance.Files`)
unchanged so no current reader is disturbed.

**Acceptance Criteria**:
- [ ] `config.RootConfig` exists with a `Files map[string]string` field tagged
      `toml:"files,omitempty"`, and `WorkspaceConfig` has a
      `Root RootConfig` field tagged `toml:"root,omitempty"`.
- [ ] `EffectiveConfig` has `InstanceFiles` and `RootFiles` fields of type
      `map[string]string`.
- [ ] `MergeInstanceOverrides` populates `InstanceFiles` from `ws.Instance.Files`
      only (NOT from `ws.Files`) and `RootFiles` from `ws.Root.Files`, dropping
      keys whose value is the empty string.
- [ ] A config with a repo-level `[files]` entry and no `[instance.files]`
      yields an empty `EffectiveConfig.InstanceFiles` (guards the conflation
      fix described in the DESIGN).
- [ ] A `workspace.toml` declaring `[instance.files]` and `[root.files]` parses
      and round-trips those tables.
- [ ] `go test ./...` passes.

**Dependencies**: None

**Type**: code
**Files**: `internal/config/config.go`, `internal/workspace/override.go`

---

### Issue 2: refactor(workspace): extract a pluggable copy core and add verbatim file materialization

**Complexity**: testable

**Goal**: Extract the per-file copy body of `FilesMaterializer.materializeFile`
and the directory walk of `materializeDir` (in
`internal/workspace/materialize.go`) into shared helpers parameterized by a
`rename func(base string) string` strategy plus source/target roots. Rewire
`FilesMaterializer` to pass the existing `.local` strategy
(`injectLocalInfix`/`localRename`) so its behavior is byte-for-byte unchanged.
Add `materializeVerbatimFiles(ctx *MaterializeContext, files map[string]string)
([]string, error)` that uses an identity rename strategy, supports single-file
and directory (`trailing /`) sources, skips empty destinations, enforces
`checkContainment` on source (within `ctx.ConfigDir`) and destination (within
`ctx.RepoDir`), records sources via `ctx.recordSources`, and returns written
paths.

**Acceptance Criteria**:
- [ ] The per-file copy and directory walk are shared between the repo path and
      the verbatim path through a rename-strategy parameter.
- [ ] `FilesMaterializer` still inserts `.local` for explicit and directory
      destinations; all pre-existing `FilesMaterializer` tests pass unchanged.
- [ ] `materializeVerbatimFiles` copies a single-file mapping to
      `ctx.RepoDir/<dest>` under the exact destination name (no `.local`
      inserted).
- [ ] `materializeVerbatimFiles` copies a directory source (trailing `/`),
      preserving structure, with no `.local` inserted on any file.
- [ ] An empty-string destination is skipped (removal semantics).
- [ ] A source path that escapes `ctx.ConfigDir` (e.g. via `..`) is rejected
      with an error rather than copied; a destination escaping `ctx.RepoDir` is
      rejected.
- [ ] Written files carry the same restrictive file mode the existing
      materializer uses, and their sources are recorded for fingerprinting.
- [ ] `go test ./...` passes.

**Dependencies**: None

**Type**: code
**Files**: `internal/workspace/materialize.go`

---

### Issue 3: feat(workspace): materialize verbatim files at the workspace root and instance root

**Complexity**: critical

**Goal**: Call `materializeVerbatimFiles` from the two non-repo materialization
paths. In `InstallWorkspaceRootSettings`
(`internal/workspace/workspace_context.go`), materialize
`effective.InstanceFiles` into `instanceRoot` using the existing
`MaterializeContext` and append the written paths to the returned `written`
slice so they join `ManagedFiles` tracking (drift + `cleanRemovedFiles`). In
`MaterializeWorkspaceRoot`/`writeRootSettings`
(`internal/workspace/root_materializer.go`), build a `MaterializeContext` with
`RepoDir: workspaceRoot` and materialize `effective.RootFiles`, appending to the
returned `written` slice (overwrite-idempotent; callers discard the slice today).
Update the `workspace.toml` scaffold/template and the relevant contributor docs
to mention `[root.files]` and the now-live `[instance.files]`, and document the
naming asymmetry (`.local` at repo level, verbatim at the non-repo levels).

**Acceptance Criteria**:
- [ ] After `niwa apply`, an `[instance.files]` entry mapping a source to
      `.mcp.json` produces a verbatim `.mcp.json` at each instance root (no
      `.local`).
- [ ] After `niwa apply`, a `[root.files]` entry mapping a source to `.mcp.json`
      produces a verbatim `.mcp.json` at the workspace root (no `.local`).
- [ ] A newly created instance (`niwa create`) receives the declared
      `[instance.files]` file at its root verbatim.
- [ ] The instance-root file appears in the instance's `ManagedFiles`, and
      removing its `[instance.files]` entry and re-applying deletes the file via
      `cleanRemovedFiles`.
- [ ] The workspace-root file is re-written on apply and is overwrite-idempotent;
      removal-cleanup at the workspace root is not asserted (documented
      limitation).
- [ ] The per-repo `[files]` path is unaffected: repo file tests still pass and
      repo distribution still inserts `.local`.
- [ ] The `workspace.toml` scaffold/template and docs mention `[root.files]` and
      `[instance.files]` with the naming-asymmetry note.
- [ ] A functional `@critical` Gherkin scenario in `test/functional/features/`
      covers the apply and create paths for a verbatim `.mcp.json` at both
      levels.
- [ ] `go test ./...` and the functional suite pass.

**Dependencies**: <<ISSUE:1>>, <<ISSUE:2>>

**Type**: code
**Files**: `internal/workspace/workspace_context.go`,
`internal/workspace/root_materializer.go`, scaffold/template and
`docs/guides/` files, `test/functional/features/`

---

## Dependency Graph

_Single-PR plan: issue dependencies are carried by the Dependencies lines in the Issue Outlines above (I1 and I2 are independent; I3 depends on both); a separate issue-number graph is the multi-pr artifact and does not apply here._

## Implementation Sequence

**Critical path:** Issue 1 + Issue 2 (parallel) → Issue 3

Issues 1 and 2 are independent and can be built in parallel: Issue 1 touches the
config and merge layer, Issue 2 touches the materialization layer, and neither
depends on the other to compile. Issue 3 is the integrating gate -- it consumes
the effective fields from Issue 1 and the `materializeVerbatimFiles` entry point
from Issue 2, wires them into the two non-repo paths, and adds the end-to-end
(functional) coverage. Because this is a single-PR plan, all three land together
on one branch; the sequencing just orders the work within that branch.

**Parallelization opportunity:** Issues 1 and 2 can be worked simultaneously
before Issue 3 integrates them.
