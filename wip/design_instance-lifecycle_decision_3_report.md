# Decision 3: What should `niwa status` display and how?

## Context

PRD R13 requires `niwa status` to show workspace health with context-aware behavior:
- From workspace root: summary of all instances
- From inside an instance: detailed view of that instance
- Optional `[instance]` argument to target a specific instance

The existing infrastructure provides all the data needed:
- `InstanceState` has `Repos` (map of name to `RepoState` with URL and cloned flag), `ManagedFiles` (with sha256 hashes), `InstanceName`, `InstanceNumber`, `Created`, `LastApplied`
- `CheckDrift` compares current file hash against recorded hash, handles deleted files
- `EnumerateInstances` scans subdirectories for `.niwa/instance.json`
- `DiscoverInstance` walks up from cwd to find the current instance
- `LoadState` deserializes instance state

## Decision

### Context Detection

Use the same discovery pattern as `apply`: try `DiscoverInstance(cwd)` first. If it succeeds and no explicit argument was given, show the detailed single-instance view. If it fails (no instance found walking up), try `EnumerateInstances(cwd)` to show the summary view. An explicit `[instance]` argument resolves through the global registry, then shows the detailed view for that instance.

### Summary View (from root)

```
Instances:

  tsuku-6    5 repos   0 drifted   applied 2h ago
  other-ws   3 repos   1 drifted   applied 3d ago
```

One line per instance. Columns: instance name, repo count, drift count, relative time since last apply. Sorted alphabetically by name. This keeps output scannable when managing multiple workspaces.

Drift count is computed by running `CheckDrift` on every managed file in each instance. Since this is filesystem-only (sha256 hashing), it stays fast even with many files.

If no instances are found, print: `No instances found in <dir>`

### Detailed View (from instance or with argument)

```
Instance: tsuku-6
Config:   tsuku
Root:     /home/user/dev/workspace/tsuku-6
Created:  2026-03-25 14:30
Applied:  2026-03-27 09:15

Repos:
  niwa       cloned
  tsuku      cloned
  koto       cloned
  vision     missing

Managed files:
  CLAUDE.md                          ok
  public/CLAUDE.md                   ok
  public/niwa/CLAUDE.local.md        drifted
  private/CLAUDE.md                  removed
```

Sections:
1. **Header** -- instance name, config name, root path, timestamps.
2. **Repos** -- each repo name with "cloned" or "missing" status. A repo shows "missing" when `RepoState.Cloned` is false or the directory no longer exists on disk (verify with `os.Stat` on the expected path).
3. **Managed files** -- each file path (relative to instance root) with status: "ok", "drifted", or "removed". Computed via `CheckDrift`.

### Implementation Structure

Add a `StatusReport` type in `internal/workspace/status.go`:

```go
type RepoStatus struct {
    Name   string
    URL    string
    Status string // "cloned", "missing"
}

type FileStatus struct {
    Path   string
    Status string // "ok", "drifted", "removed"
}

type InstanceStatus struct {
    Name        string
    ConfigName  string
    Root        string
    Created     time.Time
    LastApplied time.Time
    Repos       []RepoStatus
    Files       []FileStatus
    DriftCount  int
}
```

A `ComputeStatus(state *InstanceState) (*InstanceStatus, error)` function does the work: iterates repos to check directory existence, runs `CheckDrift` on each managed file, counts drifted files. The CLI command in `internal/cli/status.go` handles context detection and formatting.

### What We Skip for v0.1

- **Machine-readable output (--json)**: Not required per constraints. The `InstanceStatus` struct is already JSON-serializable, so adding `--json` later is trivial.
- **Color output**: Plain text only. Color can be layered on without changing the data model.
- **Exit codes for drift**: The command always exits 0. A future `--check` flag could exit non-zero when drift is detected (useful in CI).

## Alternatives Considered

1. **Table-based output with aligned columns everywhere**: Adds complexity for formatting (column width calculation) with little benefit given the small number of items. The summary view uses loose alignment; the detailed view uses a simple list.

2. **Show file hashes in detailed view**: Too noisy for the default view. The status ("ok"/"drifted"/"removed") conveys what matters. Full hashes would belong in a `--verbose` flag.

3. **Verify repo contents (git status within each repo)**: Out of scope. niwa manages workspace structure, not repo working state. Running `git status` in each repo would be slow and conflates concerns.

## Confidence

High. The data model is already in place. The context detection reuses existing functions. The output format follows standard CLI conventions (summary for multiple items, detail for a single item). No new dependencies needed.
