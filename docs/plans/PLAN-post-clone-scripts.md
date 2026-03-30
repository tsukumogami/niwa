---
schema: plan/v1
status: Draft
execution_mode: single-pr
upstream: docs/designs/DESIGN-post-clone-scripts.md
milestone: "Post-clone scripts"
issue_count: 2
---

# PLAN: Post-Clone Scripts

## Status

Draft

## Scope Summary

Implement the setup directory convention for running repo-provided scripts after
clone/apply. Configurable default directory, per-repo override/disable, lexical
ordering, stop-on-error per repo.

## Decomposition Strategy

**Horizontal.** Two issues: config types and resolution first, then script execution
and pipeline integration. The second depends on the first.

## Issue Outlines

### 1. Add setup_dir config and resolution

**Goal:** Add `SetupDir` to workspace metadata and repo overrides. Implement
resolution logic: repo override -> workspace default -> `"scripts/setup"`.

**Acceptance criteria:**
- `WorkspaceMeta.SetupDir` field (`string`, `toml:"setup_dir,omitempty"`)
- `RepoOverride.SetupDir` field (`*string`, `toml:"setup_dir,omitempty"`)
- Resolution function: `ResolveSetupDir(ws, repoName) string` returns effective
  directory path or empty string (disabled)
- `*string` nil = not set (use workspace default), `""` = disabled, `"path"` = override
- Config parsing tests with `setup_dir` at workspace and repo levels
- Resolution tests: default, workspace override, repo override, repo disable

**Dependencies:** None

**Complexity:** simple

### 2. Script execution and pipeline integration

**Goal:** Implement `RunSetupScripts` that scans a directory for executable scripts
and runs them in lexical order. Integrate as Step 6.75 in the apply pipeline.

**Acceptance criteria:**
- `RunSetupScripts(repoDir, setupDir string) *SetupResult` function
- Scans top-level of `repoDir/setupDir` for executable files
- Runs in lexical order; stops remaining scripts on first non-zero exit
- Non-executable files produce a warning and are skipped
- Missing or empty directory silently skipped
- Progress output: repo name, script name, success/failure
- Pipeline integration: runs after materializers, before managed file tracking
- Tests: scripts succeed, directory missing, script fails (stop remaining),
  disabled via empty string, non-executable file warning, empty directory,
  lexical ordering verified

**Dependencies:** <<ISSUE:1>>

**Complexity:** testable

## Dependency Graph

```mermaid
graph LR
    I1["1: Setup dir config"]
    I2["2: Script execution"]

    I1 --> I2

    classDef ready fill:#bbdefb
    classDef blocked fill:#fff9c4

    class I1 ready
    class I2 blocked
```

**Legend**: Blue = ready, Yellow = blocked

## Implementation Sequence

Sequential: issue 1 first (config types), then issue 2 (execution + pipeline).
No parallelization possible.
