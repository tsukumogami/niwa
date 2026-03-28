# Decision 3: How should init handle conflicts and edge cases?

## Question

How should `niwa init` handle conflicts and edge cases (existing `.niwa/`, re-init, init inside instance)?

## Chosen Approach: Fail-fast with targeted detection

Detect each conflict case, refuse the operation with a clear error message explaining what was found and what the user should do instead. No `--force` flag in the initial implementation.

## Cases and Behavior

### Case 1: workspace.toml already exists in current directory

`.niwa/workspace.toml` exists at `$PWD/.niwa/workspace.toml`.

**Behavior:** Exit with error.

```
Error: this directory is already a niwa workspace (.niwa/workspace.toml exists)
  Run "niwa apply" to update this workspace, or remove .niwa/ to start fresh.
```

This directly implements the PRD acceptance criterion.

### Case 2: Running init inside an existing instance

`DiscoverInstance(cwd)` finds a `.niwa/instance.json` in the current directory or a parent. The user is inside a managed workspace instance and probably doesn't intend to nest a workspace root here.

**Behavior:** Exit with error.

```
Error: current directory is inside a workspace instance at /path/to/instance
  Navigate to a directory outside any existing workspace to run init.
```

### Case 3: .niwa/ directory exists but contains neither workspace.toml nor instance.json

Something created a `.niwa/` directory but it's not a recognized niwa artifact. Could be a partial failure, manual creation, or a different tool.

**Behavior:** Exit with error.

```
Error: .niwa/ directory already exists but contains no recognized configuration
  Remove .niwa/ manually if you want to initialize a new workspace here.
```

Refusing here avoids silently claiming ownership of an unknown directory. The user can `rm -rf .niwa/` and retry -- a single command.

### Case 4: Partial init failure (cleanup)

If init fails partway through (e.g., after creating `.niwa/` but before writing `workspace.toml`), a subsequent init attempt hits Case 3.

**Behavior:** Same as Case 3. The error message tells the user to remove `.niwa/` and retry. This is simple and predictable. An atomic write approach (write workspace.toml to a temp file, then rename) reduces the window for partial state, but if it happens, the user gets a clear recovery path.

## Detection Order

Check in this order, stop at the first match:

1. Check `$PWD/.niwa/workspace.toml` exists -- Case 1
2. Check `$PWD/.niwa/` exists (without workspace.toml) -- Case 3
3. Walk up from `$PWD` looking for `.niwa/instance.json` -- Case 2

Case 2 is checked last because the upward walk is more expensive and Cases 1/3 are local filesystem checks. Case 3 before Case 2 also means that if someone has a stale `.niwa/` in the current directory *and* is inside an instance, we report the local conflict first (more actionable).

## Alternatives Considered

### --force flag to overwrite

Adding `--force` to bypass conflicts. Rejected for the initial implementation because:
- It adds a code path that needs testing and can lead to data loss.
- Users can achieve the same result with `rm -rf .niwa/ && niwa init`.
- Can be added later if user friction warrants it, without breaking changes.

### Automatic cleanup of partial state

Detecting and auto-recovering from partial init failures. Rejected because:
- Hard to distinguish "partial init" from "something else put files in .niwa/".
- The manual recovery path (`rm -rf .niwa/`) is one command.
- Automatic cleanup of unknown state risks destroying user data.

### Warning instead of error for Case 2

Printing a warning but allowing init to proceed when inside an instance. Rejected because nesting a workspace root inside an instance is almost certainly a mistake and creates confusing state where both `Discover` (walks up for workspace.toml) and `DiscoverInstance` (walks up for instance.json) could find conflicting configurations.

## Implementation Notes

The detection logic should use existing functions:
- `os.Stat(filepath.Join(cwd, ".niwa", "workspace.toml"))` for Case 1
- `os.Stat(filepath.Join(cwd, ".niwa"))` for Case 3
- `workspace.DiscoverInstance(cwd)` for Case 2

All checks run before any filesystem writes, so init either succeeds fully or changes nothing.
