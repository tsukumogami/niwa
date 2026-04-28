---
schema: plan/v1
status: Draft
execution_mode: single-pr
upstream: docs/designs/DESIGN-worker-permissions.md
milestone: "Worker session permissions"
issue_count: 3
---

# PLAN: Worker session permissions

## Status

Draft

## Scope summary

Wire worker session permission inheritance into the niwa mesh daemon: a new workspace
package helper reads the coordinator's configured permission mode once at daemon
startup, and the spawn logic uses that mode plus a curated fallback Bash tool list for
non-bypass workers.

## Decomposition strategy

**Horizontal decomposition.** The three components have clear, stable interfaces and
one is a strict prerequisite for the others: the workspace helper and fallback tool
list must exist before the daemon wiring can import them, and the daemon wiring must
exist before functional tests can verify the spawn arguments. Integration risk is low
because all three issues touch separate files with no runtime interaction during build.

## Issue outlines

### Issue 1: feat(workspace): add WorkerPermissionMode helper and WorkerFallbackBashTools

**Goal**: Add `WorkerPermissionMode(instanceRoot string) string` to `internal/workspace`
and `WorkerFallbackBashTools []string` to `internal/mcp/allowed_tools.go`. These are
the exported identifiers that the daemon wiring in issue 2 depends on.

**Acceptance criteria**:
- [ ] `WorkerPermissionMode` compiles and passes `go vet ./internal/workspace/...`
- [ ] Unit tests cover: file absent → `"acceptEdits"`, `defaultMode = "bypassPermissions"` → `"bypassPermissions"`, `defaultMode = "askPermissions"` → `"acceptEdits"`, permissions key absent → `"acceptEdits"`, malformed JSON → `"acceptEdits"`, empty `defaultMode` → `"acceptEdits"`
- [ ] `WorkerFallbackBashTools` compiles and passes `go vet ./internal/mcp/...`
- [ ] No new exported names beyond `WorkerPermissionMode` and `WorkerFallbackBashTools`
- [ ] `settingsPermissionsDoc` is unexported and carries the comment warning against widening the struct

**Dependencies**: None

**Files**: `internal/workspace/permissions.go`, `internal/workspace/permissions_test.go`, `internal/mcp/allowed_tools.go`

---

### Issue 2: fix(mesh): use coordinator permission mode for worker sessions

**Goal**: Replace the hardcoded `--permission-mode=acceptEdits` in `spawnWorker` with
a runtime value resolved once at daemon startup from `settings.local.json` via
`workspace.WorkerPermissionMode`, stored in `spawnContext`, and applied to every
subsequent worker spawn.

**Acceptance criteria**:
- [ ] `mesh_watch.go` compiles and passes `go vet ./internal/cli/...`
- [ ] `spawnContext` has a `workerPermMode string` field
- [ ] `workspace.WorkerPermissionMode` is called exactly once at daemon startup (grep confirms no per-spawn call in `spawnWorker`)
- [ ] `spawnWorker` no longer hardcodes the literal `"acceptEdits"` in command args
- [ ] A workspace with `permissions = "bypass"` causes workers to receive `--permission-mode=bypassPermissions`
- [ ] A workspace with no permissions config causes workers to receive `--permission-mode=acceptEdits` and the curated Bash patterns in `--allowed-tools`
- [ ] The `tools` slice append in `spawnWorker` does not mutate `ClaudeAllowedTools` or `WorkerFallbackBashTools` (uses a local copy)

**Dependencies**: Blocked by <<ISSUE:1>>

**Files**: `internal/cli/mesh_watch.go`

---

### Issue 3: test(mesh): add functional scenarios for worker permission inheritance

**Goal**: Add two `@critical` Gherkin scenarios to `test/functional/features/mesh.feature`
covering both branches: bypass path (coordinator configured with bypass, worker spawned
with `bypassPermissions`) and fallback path (no bypass configured, worker spawned with
`acceptEdits` and curated Bash patterns).

**Acceptance criteria**:
- [ ] Two `@critical` Gherkin scenarios added to `mesh.feature`
- [ ] Both scenarios pass under `make test-functional-critical`
- [ ] Bypass scenario verifies `--permission-mode=bypassPermissions` in spawn args
- [ ] Bypass scenario verifies fallback Bash patterns are absent from `--allowed-tools`
- [ ] Fallback scenario verifies `--permission-mode=acceptEdits` in spawn args
- [ ] Fallback scenario verifies `Bash(gh *)` and `Bash(git *)` present in `--allowed-tools`
- [ ] No existing scenarios in `mesh.feature` are broken

**Dependencies**: Blocked by <<ISSUE:2>>

**Files**: `test/functional/features/mesh.feature`

## Implementation issues

_(single-pr mode — no GitHub issues)_

## Dependency graph

```mermaid
graph LR
    I1["#1: workspace helper + fallback tools"]
    I2["#2: wire permission mode into spawn"]
    I3["#3: functional test coverage"]

    I1 --> I2
    I2 --> I3

    classDef done fill:#c8e6c9
    classDef ready fill:#bbdefb
    classDef blocked fill:#fff9c4

    class I1 ready
    class I2,I3 blocked
```

**Legend**: Green = done, Blue = ready, Yellow = blocked

## Implementation sequence

**Critical path**: Issue 1 → Issue 2 → Issue 3

All three issues are on the critical path. No parallelism is possible — each issue
depends on the previous one completing. Start with issue 1, which has no blocking
dependencies.
