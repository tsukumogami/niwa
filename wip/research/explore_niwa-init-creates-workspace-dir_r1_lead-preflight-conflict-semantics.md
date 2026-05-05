# Lead: What conflict semantic should the pre-flight check enforce on <cwd>/<name>?

## Findings

### 1. Current "Exists" Semantic
**File:** `/Users/danielgazineu/dev/niwaw/tsuku/tsukumogami-2/public/niwa/internal/workspace/preflight.go:49-87`

`CheckInitConflicts(dir)` today checks the cwd-scoped directory for:
- `.niwa/workspace.toml` (existing workspace)
- `.niwa/` directory present but without workspace.toml (orphaned state)
- Any enclosing instance by walking upward via `DiscoverInstance(absDir)`

The function only inspects nested niwa metadata, not arbitrary filesystem conflicts. It does NOT fail on:
- Existing regular files at that path
- Existing non-empty directories (other than .niwa/)
- Symlinks

This is intentional: the check is niwa-specific, not a general "is this dir empty?" guard.

**Test patterns:** `preflight_test.go:10-178` shows three positive conflict cases (workspace exists, orphaned .niwa/, inside instance) and one negative case (clean dir). No tests cover what happens if a regular file or non-empty non-niwa directory exists.

### 2. Sentinel Errors and InitConflictError Shape
**File:** `/Users/danielgazineu/dev/niwaw/tsuku/tsukumogami-2/public/niwa/internal/workspace/preflight.go:10-37`

Three sentinel errors exist:
```go
var (
  ErrWorkspaceExists = errors.New("workspace already exists")
  ErrInsideInstance = errors.New("directory is inside an existing instance")
  ErrNiwaDirectoryExists = errors.New(".niwa directory exists without workspace config")
)
```

`InitConflictError` wraps a sentinel with:
- `Err` (the sentinel)
- `Detail` (contextual message, e.g., "found .niwa/workspace.toml")
- `Suggestion` (user-facing remediation, e.g., "Use niwa apply to update")

Error formatting: `"<Err>: <Detail>. <Suggestion>"`

**Consumer:** `/Users/danielgazineu/dev/niwaw/tsuku/tsukumogami-2/public/niwa/internal/cli/init.go:123-130` extracts `conflict.Detail` and `conflict.Suggestion` and formats them separately:
```go
if errors.As(err, &conflict) {
  return fmt.Errorf("%s\n  %s", conflict.Detail, conflict.Suggestion)
}
```

Only `runInit` calls `CheckInitConflicts(cwd)` (line 124). No other callers exist in the codebase.

### 3. DiscoverInstance Behavior (Nested-Instance Check)
**File:** `/Users/danielgazineu/dev/niwaw/tsuku/tsukumogami-2/public/niwa/internal/workspace/state.go:274-295`

```go
func DiscoverInstance(startPath string) (string, error) {
  // Walks upward from startPath, looking for .niwa/instance.json
  // Returns the directory containing the instance, or error if not found
}
```

Called unconditionally in `CheckInitConflicts` (preflight.go:77). If the directory is inside an existing instance (parent or ancestor contains `.niwa/instance.json`), error on `ErrInsideInstance`.

**Implication for new flow:** The nested-instance check as written would still apply if we pass `targetDir` instead of `cwd`. Since the target dir doesn't exist yet, `DiscoverInstance(targetDir)` would walk upward from the same parent (cwd), so the behavior is equivalent: "is the parent hierarchy inside an instance?"

### 4. Function Signature: Single vs. Dual Param
**Current signature:** `CheckInitConflicts(dir string) error`

**Call site:** `init.go:124` passes `cwd` (current working directory).

**For new flow options:**
1. **Option A (two params):** `CheckInitConflicts(parentDir string, targetName string) error`
   - Caller computes `targetDir := filepath.Join(parentDir, targetName)` internally
   - Simpler for the caller; function handles path computation
   - Pros: single source of truth inside CheckInitConflicts; easier to extend
   - Cons: breaks backward compatibility if there are other callers

2. **Option B (caller computes):** Keep signature, caller passes `filepath.Join(cwd, name)`
   - Explicit at call site
   - Pros: minimal internal change; caller controls path logic
   - Cons: split responsibility

3. **Option C (overload or wrapper):** Add a second function `CheckInitConflictsForTarget(cwd, name string)`
   - Preserves the existing `CheckInitConflicts(cwd)` for no-name modes
   - Allows both modes to coexist during transition
   - Pros: explicit; backward compatible
   - Cons: code duplication if the logic differs only in the target

**Current-only-caller analysis:** `init.go:124` is the only call. `runInit` receives `args []string` from cobra; with the new flow logic, it will decide whether to compute a target path or use cwd. The caller already has all the info needed to compute the target.

### 5. Detection Order and Priority
**File:** `preflight_test.go:106-149` documents expected priority:
1. `.niwa/workspace.toml` exists → `ErrWorkspaceExists` (Case 1)
2. `.niwa/` exists without workspace.toml → `ErrNiwaDirectoryExists` (Case 3)
3. Inside an existing instance → `ErrInsideInstance` (Case 2)

Tests confirm: "workspace beats orphaned dir beats inside instance." The order in the code (lines 59-84) matches: workspace check first, orphaned dir second, nested instance last.

For a new target-dir check, any "target already exists" detection should likely fit at the **beginning** (before workspace, orphaned dir, nested instance) — the target dir doesn't exist yet in the happy path, so this is a gate on whether we can even attempt to create it.

### 6. "Already Exists" Semantic for Target Dir
**Decision already made (per explore scope):** "Error if `<cwd>/<name>` already exists."

**Question:** What does "exists" mean?
- Any filesystem path (file, dir, symlink)?
- Only directories?
- Only non-empty directories?

**Current code insight:** `preflight.go:59` uses `os.Stat(workspaceConfig)` and checks `err == nil` to detect workspace.toml (file must exist). `preflight.go:68` uses `os.Stat(niwaDir)` and checks both `err == nil && info.IsDir()` to detect `.niwa/` (must be a directory). This suggests the code is type-aware.

**Strictest sensible policy:** Reject any pre-existing path at the target location (file, directory, symlink, block device, etc.). Justification:
- Prevents accidental overwrites of files (if target is a file).
- Prevents "reuse of existing dir" patterns that could silently merge state.
- Matches the semantic of "create this directory as a new workspace" — an error on anything already there is the cleanest contract.

**Evidence for strictness:** The scope doc (line 57-61) says "Error if `<cwd>/<name>` already exists. No 'use if empty' or 'reuse' paths." — this suggests no nuance, just reject outright.

## Implications

### Recommended Function Signature
**Use Option B (caller computes target):**
```go
func CheckInitConflicts(dir string) error
```

**Rationale:**
- The only caller (`init.go:124`) already has the information needed to compute the target directory before calling the preflight check.
- Changing to a two-parameter signature breaks the API for a function that today is in an internal package and only has one caller; since that caller is also in the same codebase, refactoring is low-risk.
- However, the simpler approach is to keep the signature and let the caller handle `filepath.Join(cwd, name)` when a target name is provided.
- This preserves backward compatibility if the function is ever exported or used elsewhere in the future (unlikely given the internal package, but safe).

**In init.go, the change would look like:**
```go
var targetDir string
if name != "" {
  targetDir = filepath.Join(cwd, name)
} else {
  targetDir = cwd
}
if err := workspace.CheckInitConflicts(targetDir); err != nil {
  // ... handle error
}
```

### "Exists" Semantic
**Recommended: Reject any pre-existing path at the target.**

```go
// At the start of CheckInitConflicts, add before the existing checks:
targetInfo, err := os.Stat(dir)
if err == nil {
  // Path exists (file, dir, symlink, etc.)
  return &InitConflictError{
    Err:        ErrTargetDirExists,
    Detail:     fmt.Sprintf("path %s already exists", dir),
    Suggestion: "Remove or rename the existing path and retry",
  }
}
if !errors.Is(err, os.ErrNotExist) {
  // Some other filesystem error (permission denied, etc.)
  return fmt.Errorf("checking target path: %w", err)
}
// Path does not exist; continue to niwa-specific checks
```

**But wait:** This assumes we're always checking a target that shouldn't exist. For no-name modes (`niwa init` and `niwa init --from` without positional name), the target **is** the cwd, which obviously exists. So this check must be **conditional** on the caller having a non-empty target name.

**Revised approach:** The "target already exists" check belongs in the caller (`init.go`), not in `CheckInitConflicts`:
- When `name != ""` (new flow), check `filepath.Exists(filepath.Join(cwd, name))` before calling `CheckInitConflicts`.
- When `name == ""` (no-name mode), skip that check; call `CheckInitConflicts(cwd)` as before.
- `CheckInitConflicts` continues to do niwa-specific conflict detection (workspace exists, orphaned .niwa/, nested instance).

This keeps the function's concerns clean: it's a niwa-state validator, not a general filesystem validator.

### Nested-Instance Check Behavior
**For new flow:** No change needed.

`DiscoverInstance(targetDir)` (where `targetDir` doesn't exist yet) would walk upward from its parent, looking for `.niwa/instance.json`. Since the target dir doesn't exist, `filepath.Abs(targetDir)` still resolves the parent path correctly, and the walk proceeds normally. The effective behavior is: "is the would-be-parent directory inside an existing instance?"

This is correct: if `<cwd>` contains an existing instance, we should not initialize a new workspace as `<cwd>/<name>`, even if `<cwd>/<name>` doesn't exist yet.

### New Sentinel Error
**Recommended: Add `ErrTargetDirExists`** (but place the check in the caller, not in `CheckInitConflicts`).

**File to modify:** `/Users/danielgazineu/dev/niwaw/tsuku/tsukumogami-2/public/niwa/internal/workspace/preflight.go:10-20`

```go
var (
  ErrWorkspaceExists = errors.New("workspace already exists")
  ErrInsideInstance = errors.New("directory is inside an existing instance")
  ErrNiwaDirectoryExists = errors.New(".niwa directory exists without workspace config")
  ErrTargetDirExists = errors.New("target directory already exists")
)
```

Use this in `init.go` when a target name is provided:
```go
if name != "" {
  if _, err := os.Stat(filepath.Join(cwd, name)); err == nil {
    return &InitConflictError{
      Err: workspace.ErrTargetDirExists,
      Detail: fmt.Sprintf("%s already exists", filepath.Join(cwd, name)),
      Suggestion: "Remove or rename the existing path and retry",
    }
  } else if !errors.Is(err, os.ErrNotExist) {
    return fmt.Errorf("checking target path: %w", err)
  }
}
```

Then call `CheckInitConflicts` on the target (or cwd if no name), which validates niwa-state conflicts.

## Surprises

1. **The nested-instance check already has the right semantics for the new flow.** Even though the target dir doesn't exist, `DiscoverInstance` walks upward correctly. No code change needed there.

2. **The "exists" semantic is split:** A general "path exists" check belongs in the caller, not in the preflight validator. The validator is niwa-centric (workspace, orphaned .niwa/, instance nesting), not filesystem-centric. This is a clean separation of concerns.

3. **Only one caller exists.** The fact that `CheckInitConflicts` is only called from `init.go:124` makes the signature question moot — refactoring is low-risk either way. We can add a new sentinel without worrying about extern consumers.

4. **The scope doc's "error if exists" is agnostic to what "exists" means.** The decision was made to reject pre-existing targets, but not whether "exists" includes only dirs, or any path. Code review/implementation will need to clarify this, but the strictest interpretation (any path) is the safest default.

## Open Questions

1. **Does the "path exists" check happen in the caller or in `CheckInitConflicts`?**
   - If in the caller (`init.go`), `CheckInitConflicts` is niwa-state-only and stays simple.
   - If in `CheckInitConflicts`, the function must know whether it's validating an existing cwd (no-name mode) or a non-existent target (named mode). This requires either a flag parameter or signature change.
   - **Recommendation (pending design review):** Put it in the caller for separation of concerns. But the implementation PR will confirm the final choice.

2. **For no-name modes (`niwa init`, `niwa init --from`), does the nested-instance check still apply to cwd?**
   - Current code says yes. The scope doc doesn't explicitly exclude this.
   - **Assumption:** Yes, unchanged. If cwd is inside an instance, reject regardless of whether a name is given.

3. **What user-facing error message should target-dir-exists produce?**
   - E.g., "foo already exists. Remove or rename it and try again" vs. "target directory foo is not empty. Use --reuse or remove it first"
   - The scope doc says no reuse option, so the message should be straightforward rejection.
   - **Recommendation:** "X already exists. Remove or rename it and retry niwa init <name>."

## Summary

The pre-flight check for the new `niwa init <name>` flow should use a two-stage validation: (1) caller checks whether `<cwd>/<name>` path already exists (any filesystem type), rejecting with new sentinel `ErrTargetDirExists` if so; (2) `CheckInitConflicts(targetDir)` validates niwa-state conflicts (workspace exists, orphaned .niwa/, nested instance) on the target. The nested-instance check already works correctly on non-existent paths since `DiscoverInstance` walks upward from the parent. No change to `CheckInitConflicts` signature needed; caller computes target path before validating. The function becomes the niwa-specific validator, the caller handles filesystem pre-gates.
