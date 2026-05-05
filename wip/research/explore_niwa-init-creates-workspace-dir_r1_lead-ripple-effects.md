# Lead: What ripple effects hit other niwa commands and infrastructure?

## Findings

### Sub-Question 1: `niwa create [workspace-name]` — Shared Papercut & Convention Decision

**Current Behavior:**
- `niwa create [workspace-name]` looks up the workspace in the global registry (line 89 in create.go)
- It discovers the workspace root from the registry entry: `workspaceRoot := filepath.Dir(configDir)` (line 120)
- It computes an instance name and creates the instance at `filepath.Join(workspaceRoot, instanceName)` (line 130)
- The workspace root **must exist and be accessible** for `EnumerateInstances` to work (line 124)
- **No mkdir**: instances are created in pre-existing workspace roots, never creates the root directory

**Does it have the same papercut?**
Yes. Users must manually create the workspace root directory first, then run `niwa create`. This is identical to the current init papercut.

**Convention Decision:**
- **Option A (Extend)**: Add a positional `[directory-name]` arg to `create`. When given, `create` would mkdir `<cwd>/<directory-name>/` and create the first instance inside it, analogous to the new init behavior. Registry `Root` points to the new directory.
- **Option B (Leave Alone)**: Keep `create` working only with pre-existing workspace roots. Users manually mkdir before calling create.

**Recommendation: Option A (Extend).** Rationale:
1. **Consistency**: Both init and create represent "new workspace bootstrapping." Same UX pattern reduces cognitive load.
2. **Discoverability**: Users familiar with the new init behavior will expect create to work the same way.
3. **Low Risk**: The change is self-contained—only affects the positional arg parsing and one mkdir call. No downstream code changes needed because `EnumerateInstances` and `computeInstanceName` already work correctly once the root exists.
4. **Parallel Feature**: Both commands then share the convention: "with positional <name>, create the directory and land inside it."

---

### Sub-Question 2: `niwa go <target>` — Registry Root Handling

**Current Behavior:**
- `niwa go <target>` reads `entry.Root` from the global registry (line 113 in go.go)
- It validates that the root directory exists (line 114): `if _, err := os.Stat(root); os.IsNotExist(err)`
- Used by: `resolveWorkspaceRoot()` (line 104), `resolveWorkspaceRepo()` (line 148), `resolveContextAware()` (line 192)
- When instances are enumerated: `workspace.EnumerateInstances(root)` (line 149)
- No assumptions about root being cwd-at-init-time; root is always read from registry

**Impact of change:**
✓ **No hidden assumptions.** After `niwa init <name>` creates `<cwd>/<name>/` and registers `Root = <cwd>/<name>/`, calling `niwa go <name>` will correctly land in the new directory. The stat check at line 114 ensures the directory exists. `EnumerateInstances` scans that directory for instances.

**Verification:** The code is defensive and registry-driven, not cwd-dependent. No changes needed.

---

### Sub-Question 3: Other Workspace-Aware Commands — Path Resolution Patterns

**Scan Results:**

#### A. Commands Using `DiscoverInstance()` (walk up from cwd to find instance)
These are **safe** because they discover from cwd dynamically, not from init-time state.

1. **`niwa status`** — discover.go uses `DiscoverInstance(cwd)` (line 85 status.go). Falls back to `config.Discover` for summary view.
2. **`niwa destroy`** — uses `workspace.ResolveInstanceTarget(cwd, nameArg)` which internally calls `DiscoverInstance` (line 45 destroy.go).
3. **`niwa reset`** — uses `workspace.ResolveInstanceTarget(cwd, nameArg)` (line 50 reset.go).
4. **`niwa go <target>`** — uses `DiscoverInstance(cwd)` for context-aware resolution (line 178 go.go).
5. **`niwa apply`** — uses `workspace.ResolveApplyScope(cwd, applyInstance)` which calls `DiscoverInstance(cwd)` (line 88 apply.go).
6. **`niwa session list`** — uses `resolveInstanceRoot()` which prioritizes `NIWA_INSTANCE_ROOT` env var, falls back to `discoverInstanceRoot(cwd)` (line 55 session.go).
7. **`niwa session register`** — requires explicit `NIWA_INSTANCE_ROOT` env var (line 34 session_register.go); error if not set. Safe.
8. **`niwa task list/show`** — uses `resolveTasksDir()` which prioritizes `NIWA_INSTANCE_ROOT`, falls back to `discoverInstanceRoot(cwd)` (line 79 task.go).
9. **`niwa mcp-serve`** — requires explicit `NIWA_INSTANCE_ROOT` env var (line 23 mcp_serve.go); error if not set. Safe.

**✓ All safe.** They resolve paths from cwd at invocation time, not from init-time registry state.

#### B. Commands Using Registry Root (`entry.Root`)
These read from registry but are also safe.

1. **`niwa go -w <workspace>`** — reads `entry.Root` and validates it exists. Works correctly with new init behavior.
2. **`niwa apply <workspace-name>`** — reads `entry.Root` via `resolveRegistryScope()` (line 81 apply.go), then enumerates instances. Works correctly.
3. **`niwa create [workspace-name]`** — reads `entry.Root` from registry entry (line 98 create.go).

**✓ All safe.** They read from registry (which is updated by init to point to the new dir) and never assume root is cwd.

#### C. No Load-Bearing Assumptions About Init-Time cwd
Grep results show:
- **No commands read "Root" from StateDir** (`.niwa/instance.json`). State files record it but commands re-derive it.
- **No cwd-walking code assumes it's the workspace root.** All discovery functions walk *up* to find instances or config, then locate the workspace root from configDir's parent.
- **Registry is the source of truth** for workspace location (not a memory of init-time cwd).

#### D. Specific Commands—Spot Checks

**Mesh (mesh_watch.go, mesh_report_progress.go):**
- `mesh_watch.go`: Complex event loop, but resolves instance paths via env var or discovery. No cwd assumptions. ✓
- `mesh_report_progress.go`: Takes `instanceRoot` as input; no cwd assumptions. ✓

**Hint & Landing (hint.go, landing.go):**
- `hint.go`: Checks `_NIWA_SHELL_INIT` env var; navigation-agnostic. ✓
- `landing.go`: Writes paths to NIWA_RESPONSE_FILE (negotiated with shell wrapper); no cwd assumptions. ✓

**Channels (channels.go):**
- Resolution logic is in `config.WorkspaceConfig` parsing and flag priority. No workspace location logic. ✓

---

## Implications

### What Needs Changes
**None.** All commands either:
1. Discover workspace location dynamically from cwd (safe across init location changes)
2. Read from registry (which init updates to point to the new directory)
3. Accept explicit env vars (for test/hook injection)

### What Should Be Done (Optional Enhancement)
**Extend `niwa create` to accept a positional directory-name argument**, creating the workspace root and landing inside it (paralleling the new init behavior). This is not required for correctness but improves UX consistency.

Concrete change:
- Add positional arg to `createCmd.Use` → `"create [directory-name]"`
- In `runCreate()`, detect when `len(args) == 1` and it's **not** a registry lookup (no `--from`, no registry entry), then:
  - Create `filepath.Join(cwd, args[0])` as the workspace root
  - Register the root directory (not a parent)
  - Proceed with instance creation inside it

Implementation is isolated to create.go; no ripple effects to other commands.

---

## Surprises

1. **No "root-at-init-time" assumptions found.** Every command either discovers dynamically or reads from registry. This is solid design—the codebase doesn't bake in init-time state.

2. **Env var injection strategy.** Commands like session, task, mcp-serve use `NIWA_INSTANCE_ROOT` env var for test/hook override, not command-line args. This is intentional (PRD-aligned) and means changes to registry Root are completely isolated from the mesh layer.

3. **Registry entries are mutable.** The design assumes the registry can change (e.g., user deletes and re-inits a workspace). Commands validate Root exists at call time, which is defensive but also means they'll gracefully fail if a registry entry becomes stale.

---

## Open Questions

1. **Should `niwa create [directory-name]` be implemented now, or deferred?** Current code doesn't require it, but consistency UX suggests doing it soon. Decision: product call.

2. **What about `niwa init <name>` when the workspace is already registered in the global registry?** If a user runs `niwa init tsuku` and tsuku already has a SourceURL in the registry, the init command (line 105-106) will try to clone. Should the new behavior (mkdir logic) apply to that flow? Answer: yes, consistent with the design. Create `<cwd>/<name>/`, then clone into it.

3. **Documentation:** Need to update init/create help text to explain the new convention (with positional arg). Currently, no hint that init **now creates the directory**.

---

## Summary

The new `niwa init <name>` behavior (create directory, init inside) has **zero impact** on other niwa commands because all workspace-location resolution is dynamic (discovery from cwd) or registry-driven (read and validate). `niwa go <target>` and all instance-aware commands (status, apply, destroy, reset, session, task, mesh) resolve paths at invocation time, never rely on cwd-at-init-time. The only minor enhancement recommended is extending `niwa create` to adopt the same "create directory" convention for parallel UX—a small, self-contained change to create.go with no downstream ripples.

