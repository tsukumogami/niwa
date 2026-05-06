---
status: Accepted
upstream: docs/prds/PRD-niwa-init-creates-workspace-dir.md
problem: |
  niwa init initializes the workspace in cwd, requiring users to mkdir+cd
  before running it. When a positional `<name>` is given, it lands only in
  the global registry — not the workspace.toml's `[workspace] name`, the
  apply output, or status output — so commands disagree on what the
  workspace is called. The PRD requires init to create `<cwd>/<name>/` and
  for the explicit name to be the effective workspace name everywhere,
  without modifying the cloned `.niwa/workspace.toml`.
decision: |
  Add an optional `ConfigNameOverride string` field to `InstanceState`
  (no schema-version bump required; new optional fields are backward-
  compatible). `niwa init` populates this field when a positional `<name>`
  is given, after running upfront name validation and a caller-side
  `os.Lstat` existence check on `<cwd>/<name>` that emits a new
  `ErrTargetDirExists` sentinel via the existing `InitConflictError`
  shape. Add a `--rebind` boolean flag to `niwa init`; when the
  positional `<name>` collides with an existing registry entry pointing
  to a different `Root`, niwa errors with a new `ErrRegistryNameInUse`
  sentinel unless `--rebind` was given. Both `Applier.Create` (first-
  time instance creation, apply.go:262) and `Applier.Apply` (subsequent
  apply, apply.go:407) consult init state via `LoadState(workspaceRoot)`
  and prefer the override over `cfg.Workspace.Name` when populated.
  The cloned `.niwa/workspace.toml` is never modified on disk.
rationale: |
  Reusing `InstanceState` for the override matches the existing pattern
  for init-time choices (`SkipGlobal`, `NoOverlay`, `OverlayURL`) — the
  same persistence path, the same load-from-workspace-root convention
  Apply already uses, and the same JSON-omitempty default for absent
  values. The caller-side existence check keeps `CheckInitConflicts`
  signature-stable and lets niwa-state validation continue to run on the
  to-be-created path. Avoiding a schema bump keeps the change
  forward/backward compatible: old binaries reading new state ignore
  the unknown field; new binaries reading old state see an empty
  override and behave as today.
---

# DESIGN: niwa init creates workspace directory

## Status

Accepted

## Context and Problem Statement

The PRD specifies a UX change to `niwa init`: when a positional `<name>` is
given, niwa creates `<cwd>/<name>/` and initializes the workspace inside
it, and the explicit name becomes the effective workspace name across
every niwa command. Without a positional name, behavior is unchanged.

The implementation touches three concrete code surfaces:

1. **`internal/cli/init.go`** — argument parsing, mode resolution, and
   the entry point that runs preflight, materializes the workspace, and
   persists init state. Today `runInit` always uses `cwd` as the workspace
   root (the `cwd, err := os.Getwd()` at line 118 flows everywhere).
2. **`internal/workspace/preflight.go`** — `CheckInitConflicts(dir)` and
   the `InitConflictError` family. Today the function is signature-locked
   to "validate this single directory"; it does not know about a
   to-be-created target.
3. **`internal/workspace/state.go`** — `InstanceState` schema v3. Today
   the struct has `ConfigName *string` (the cloned `[workspace] name`)
   and `InstanceName string` (the per-instance directory name), both
   populated from `cfg.Workspace.Name` in two distinct places on
   `*Applier`: `Applier.Create` (apply.go:262, first-time instance
   creation) and `Applier.Apply` (apply.go:407, subsequent applies).

The architectural challenge is to thread an override value from
`niwa init`'s argument parsing through to both `Applier.Create` and
`Applier.Apply`'s instance-state writes, without (a) modifying the
cloned config on disk, (b) bumping the state schema version, or (c)
growing the `CheckInitConflicts` signature.
The PRD's preflight ordering and error sub-case routing constraints
also need a code-shape that keeps name validation, target-exists
detection, and existing niwa-state checks composable in the order
specified.

A separate, smaller surface area is the success message (R9): today
`printSuccess` (`internal/cli/init.go:383-405`) prints the workspace
name only, never the absolute path. The PRD requires the absolute path
in every mode.

## Decision Drivers

- **Backward compatibility.** State files written by older niwa binaries
  must continue to load. State files written by new niwa binaries must
  load on older binaries that don't know about the override (older
  binaries should fall back to the toml's `[workspace] name`, which is
  the existing behavior). This argues against a schema-version bump and
  for an optional field.
- **Cloned-config purity.** The cloned `.niwa/workspace.toml` is the
  upstream's file; modifying it on disk dirties the workspace against
  upstream HEAD and complicates any future re-sync. The override must
  live somewhere else.
- **Composable preflight ordering.** PRD R5/R6/R7/R8 prescribe a fixed
  order: name validation → target-exists → niwa-state sub-case routing →
  registry rebind detection. The implementation must make this order
  deterministic and testable.
- **Test coverage.** The change has unit-test surface (preflight,
  validation, state schema), CLI-shape surface (init.go control flow),
  and end-to-end surface (Gherkin). The PRD requires at least one
  `@critical` Gherkin scenario.
- **Minimal blast radius.** The PRD intentionally excludes `niwa create`
  and adds no new flags; the design should respect that and not
  refactor surrounding code beyond what the change requires.

## Considered Options

### Decision 1: Where to persist the name override

**Option 1A: Rewrite `.niwa/workspace.toml` `[workspace] name` field
in place after clone.** Init runs the override by editing the cloned
toml. Downstream readers continue to read from the toml — no schema
or code changes elsewhere.

- Pros: zero new state surface; downstream readers untouched.
- Cons: dirties the workspace against its source HEAD on the very first
  command; complicates any future re-sync from upstream; requires the
  toml to be writable; offers no path back to the upstream name if the
  user changes their mind.

**Option 1B: Add an optional `ConfigNameOverride string` field to
`InstanceState`.** Init persists the override to
`<workspaceRoot>/.niwa/instance.json` alongside the existing init-time
fields (`SkipGlobal`, `NoOverlay`, `OverlayURL`). `Applier.Create` and
other readers consult init state and prefer the override over
`cfg.Workspace.Name` when set. **(Chosen.)**

- Pros: matches the existing `SkipGlobal`/`NoOverlay`/`OverlayURL`
  pattern exactly; the cloned toml is never modified; reuses the
  established `LoadState(workspaceRoot)` plumbing in `Applier.Create`;
  optional field needs no schema-version bump.
- Cons: introduces a divergence between the toml's on-disk name and the
  effective name surfaced by interpretive commands (acknowledged in PRD
  Known Limitations).

**Option 1C: Override only the global registry's entry key.** Today's
behavior, technically: the explicit `<name>` is the registry key, and
the toml's `[workspace] name` flows into status/apply.

- Pros: zero code change.
- Cons: the precise UX inconsistency the PRD aims to fix. Rejected by
  the PRD.

**Chosen: Option 1B.** Aligns with the existing `InstanceState` init-
override pattern, keeps the cloned config clean, requires no schema
bump.

### Decision 2: Bump the InstanceState schema version

**Option 2A: Bump from v3 to v4.** Mirrors the v2 → v3 bump that
introduced `ConfigSource` (state.go:38-43).

- Pros: explicit signal that the schema grew; consistent with prior
  bumps.
- Cons: forces older niwa binaries to refuse to load new state files
  (state.go:246-249 returns an error when `state.SchemaVersion >
  SchemaVersion`); creates an artificial floor for users running mixed
  versions.

**Option 2B: Keep at v3, add the field as optional.** **(Chosen.)** The
new field uses `omitempty`; absent values decode as the zero value;
present values decode normally on new binaries and are silently
ignored on older binaries (Go's `encoding/json` ignores unknown fields
by default).

- Pros: forward/backward compatible. Old binaries running on a
  workspace initialized by a new binary fall back to the toml's
  `[workspace] name` — the historical behavior, never wrong, just
  missing the new override.
- Cons: less explicit signal; readers must check
  `len(s.ConfigNameOverride) > 0` rather than `s.SchemaVersion >= 4`.

**Chosen: Option 2B.** The override is purely additive optional state;
older binaries gracefully degrade to today's behavior. Reserve schema
bumps for changes where on-disk format actually shifts incompatibly.

### Decision 3: Where the existence check fires

**Option 3A: Inline in `runInit` before `CheckInitConflicts`.** The
caller computes `targetDir := filepath.Join(cwd, name)`, runs
`os.Lstat(targetDir)`, returns `ErrTargetDirExists` on success of the
stat, falls through to `CheckInitConflicts(targetDir)` otherwise.
**(Chosen.)**

- Pros: keeps `CheckInitConflicts` signature unchanged; separates
  filesystem pre-gate from niwa-state validation; no-name modes (where
  `targetDir == cwd` and the path obviously exists) bypass the existence
  check entirely.
- Cons: splits conflict-detection responsibility across two files.

**Option 3B: Add a target name parameter to `CheckInitConflicts`.**
`CheckInitConflicts(parentDir, targetName string)`; internal logic
branches on `targetName == ""`.

- Pros: single function owns all conflict logic.
- Cons: changes a stable API for one caller; mixes filesystem
  pre-gating with niwa-state validation; the no-name branch still
  needs special-casing internally.

**Option 3C: New helper in `workspace` package for target-exists,
called from `runInit` before `CheckInitConflicts`.** A function
`ValidateTargetDirNotExist(targetDir string) error` that returns
`ErrTargetDirExists` wrapped in `InitConflictError`.

- Pros: cleaner separation than 3A; the helper is reusable if a future
  command needs the same check.
- Cons: a layer of indirection for what is effectively three lines of
  code today.

**Chosen: Option 3A.** The check is small enough that inlining it in
`runInit` is the right size. If a second caller appears, lift to a
helper at that point (YAGNI for now).

### Decision 4: Where `os.MkdirAll(<cwd>/<name>)` runs

**Option 4A: In `runInit` after preflight, before the
mode-specific dispatch.** The caller creates the directory once, then
`modeScaffold`/`modeNamed`/`modeClone` operate inside it.

- Pros: single place to track the side effect; rollback on subsequent
  failure happens in one location.
- Cons: changes the contract of `Scaffold` and `MaterializeFromSource`
  callers (today they expect to write into an extant `cwd`).

**Option 4B: In `runInit`, threaded through as the new "workspace root"
parameter.** Same effect as 4A but explicit: the result of the mkdir
becomes `workspaceRoot`, which is then passed to all downstream calls
(`Scaffold(workspaceRoot, ...)`, `MaterializeFromSource(..., niwaDir,
...)`, `SaveState(workspaceRoot, state)`). **(Chosen.)**

- Pros: explicit data flow; existing call signatures already accept the
  workspace dir as a parameter; the change is "swap the name of the
  variable passed in," not "add a new side effect."
- Cons: every call site in `runInit` reads `workspaceRoot` instead of
  `cwd`; small naming churn.

**Option 4C: Inside the mode-specific scaffolding/materialization
functions.** Push the mkdir into `Scaffold` and
`MaterializeFromSource`.

- Pros: each mode handles its own setup.
- Cons: duplicates the side effect across two functions; rollback
  becomes mode-specific; harder to keep the preflight and mkdir in one
  reasoned step.

**Chosen: Option 4B.** Compute `workspaceRoot` once after preflight,
thread through all calls.

### Decision 5: Effective-name resolver

**Option 5A: Each downstream reader checks the override inline.**
`Applier.Create` reads `initState.ConfigNameOverride`; status formatter
does the same; etc.

- Pros: no new abstraction; the override is right at the surface where
  it's used.
- Cons: scatter logic that "decide which name to surface" across N
  files; risk of one site forgetting and reverting to
  `cfg.Workspace.Name`.

**Option 5B: Add a small helper, e.g.
`workspace.EffectiveConfigName(state *InstanceState, cfg
*config.WorkspaceConfig) string`, that encapsulates "use override if
set, else cfg.Workspace.Name".** Called by `Applier.Create` (when
constructing the post-apply state from cfg) and `niwa status`
formatting. **(Chosen.)**

- Pros: one source of truth; simple to test; signals the intent
  "this picks the right name."
- Cons: a tiny helper for what could be a one-liner.

**Chosen: Option 5B.** Worth the minor abstraction because the same
preference logic fires in at least two places; centralizing avoids
drift.

### Decision 6: Suggestion-text shape for `ErrTargetDirExists`

The PRD pins the exact text in R5: `"Pick a different name or remove
the path and retry."` No design-level choice remains; this is recorded
to acknowledge the constraint flowed from PRD to implementation.

### Decision 7: Registry collision handling — `--rebind` flag

The PRD's R8 specifies error-by-default with `--rebind` opt-in for the
case where the positional `<name>` collides with an existing registry
entry pointing elsewhere. Three implementation shapes were considered:

**Option 7A: New `ErrRegistryNameInUse` sentinel + `--rebind` flag on
`init`.** **(Chosen.)** A boolean flag parsed in `init.go`, an existing-
entry lookup via `globalCfg.LookupWorkspace(name)`, and a new sentinel
that wraps into `InitConflictError` for consistent error rendering. The
error's `Suggestion` contains both remediation paths verbatim: the
flag and the global config TOML path (`$XDG_CONFIG_HOME/niwa/config.toml`,
default `~/.config/niwa/config.toml`).

- Pros: matches the existing `InitConflictError` rendering pattern;
  scoped sentinel name makes the error grep-friendly; flag style
  matches existing `--skip-global` / `--no-overlay` long-form-only
  convention on `init`.
- Cons: a fourth sentinel in `preflight.go` for a non-filesystem
  condition (the others all describe filesystem state).

**Option 7B: Reuse the existing `--force` flag from other commands.**
Rejected per the PRD. `--force` on `reset`/`destroy`/`apply` skips
uncommitted-changes checks, a different semantic; conflating muddies
both call sites.

**Option 7C: Add a new `niwa forget <name>` subcommand and require
users to run it before re-init.** Rejected as scope creep for this
PRD. A dedicated unregister command may be worth filing separately,
but blocking this PRD on it forces a two-step workflow for a
straightforward rebind. The TOML-edit path in the error suggestion
covers the without-`niwa-forget` case adequately.

**Chosen: Option 7A.** New sentinel, new flag, error rendering through
the existing path.

## Decision Outcome

The chosen design implements the PRD as follows:

1. **State surface.** `InstanceState` gains
   `ConfigNameOverride string` with `json:"config_name_override,omitempty"`.
   Schema version stays at v3; the field is purely additive.
2. **Init control flow.** `runInit` parses the new `--rebind` flag,
   validates the positional `<name>`, computes
   `targetDir = filepath.Join(cwd, name)` when given, runs the new
   caller-side existence check, calls `CheckInitConflicts(targetDir)`
   for niwa-state sub-case routing, runs the registry-collision check
   (errors with `ErrRegistryNameInUse` when the name is registered
   elsewhere and `--rebind` was not given), creates the target
   directory with `os.Mkdir`, dispatches to the mode-specific
   scaffold/clone using `targetDir` as the workspace root, queues a
   stderr confirmation warning when `--rebind` was given, populates
   `ConfigNameOverride` in the init state when applicable, persists
   state via `SaveState(targetDir, state)`, and prints a success
   message that includes the absolute path.
3. **Preflight ordering.** Validation → existence check → niwa-state
   sub-case routing (workspace exists, orphan `.niwa/`) → nested-
   instance walk-up → registry collision check. The order is
   documented in `runInit` as a code comment and asserted by unit
   tests. R5 (target exists) takes precedence over R8 (registry
   collision) regardless of `--rebind`.
4. **Effective-name resolution.** A helper
   `workspace.EffectiveConfigName(state, cfg)` returns
   `state.ConfigNameOverride` if non-empty, else `cfg.Workspace.Name`.
   `Applier.Create` uses it when constructing post-apply state.
   `niwa status` formatting uses it. Other readers can adopt as needed.
5. **Override note.** When the override is being set and differs from
   the cloned config's `[workspace] name`, init emits the per-invocation
   stderr note specified in PRD R4. The note is not persisted to state.
6. **Error implementation.** New sentinels `ErrTargetDirExists` and
   `ErrRegistryNameInUse` in `preflight.go`. The caller wraps each in
   `InitConflictError{Detail, Suggestion}` per the existing pattern.
   For `ErrTargetDirExists`, `Detail` includes the absolute path and a
   path-type qualifier (`file`/`directory`/`symlink`) from `os.Lstat`.
   For `ErrRegistryNameInUse`, `Detail` includes the existing `Root`
   path verbatim, and `Suggestion` includes both remediation paths:
   the `--rebind` flag and the global config TOML path resolved at
   call time (so the message reflects the user's actual
   `$XDG_CONFIG_HOME` rather than a hardcoded default).
7. **Success message.** `printSuccess` is reworked to include the
   absolute workspace root path resolved via `filepath.EvalSymlinks`
   (matching how niwa elsewhere handles macOS `/var/...` →
   `/private/var/...`).

## Solution Architecture

### Components

- **`internal/workspace/validate.go` (new file)** — shared input
  validation.
  - New: `ValidateInitName(name string) error` (exported). Applies
    `^[a-zA-Z0-9._-]+$`, rejects literals `.`, `..`, `.niwa`, and
    empty string. The returned error quotes the offending input and
    includes a human-readable description of the allowed character
    set per PRD R7. Exported so future entry points that ingest
    workspace names (RPC, MCP, etc.) reuse the same rules.
- **`internal/cli/init.go`** — primary control flow change.
  - Modified: `initCmd` definition gains a `--rebind` boolean flag
    (long-form only, default `false`) wired to a new local variable
    consumed by `runInit`. Help text describes the registry-collision
    case it unblocks.
  - Modified: `runInit` reorganized to validate via
    `workspace.ValidateInitName(name)` before any filesystem touch,
    compute `workspaceRoot`, enforce preflight order, and thread
    `workspaceRoot` into existing calls. Final-component creation
    uses `os.Mkdir(workspaceRoot, 0o755)` (NOT `os.MkdirAll`) to fail
    on raced symlinks; the parent of `workspaceRoot` is `cwd` and is
    guaranteed to exist.
  - New: registry-collision check between niwa-state validation and
    the directory-creation step. Looks up the name in the loaded
    `GlobalConfig`; if an entry exists with `Root != workspaceRoot`
    and `--rebind` was not given, returns
    `InitConflictError{Err: ErrRegistryNameInUse, Detail, Suggestion}`.
    The suggestion text is built at call time from
    `config.GlobalConfigPath()` (defined in
    `internal/config/registry.go:129`, returns `(string, error)`) so
    it reflects the user's actual `$XDG_CONFIG_HOME`. If
    `GlobalConfigPath` returns an error (XDG resolution failure),
    the suggestion falls back to the literal string
    `~/.config/niwa/config.toml` rather than failing the whole init
    error path.
  - Modified: `printSuccess` now takes the workspace root path and
    includes it in the printed line. `runInit` resolves the path
    via `filepath.EvalSymlinks` before passing.
  - Modified: `buildInitState` accepts the optional override and
    populates `ConfigNameOverride` when set.
  - New: rebind-confirmation rendering — when `--rebind` was given
    and a registry entry was rewritten, emit a `WARNING:`-prefixed
    stderr line, both `Root` paths, and a trailing blank line for
    visual separation (per Security Considerations §6).
- **`internal/workspace/preflight.go`** — new sentinels.
  - New: `ErrTargetDirExists = errors.New("target path already exists")`.
  - New: `ErrRegistryNameInUse = errors.New("workspace name already registered")`.
  - Existing `CheckInitConflicts` is unchanged.
- **`internal/workspace/state.go`** — schema-additive change.
  - New field: `ConfigNameOverride string \`json:"config_name_override,omitempty"\``.
  - Schema version unchanged at v3.
- **`internal/workspace/effective_name.go` (new file)** —
  `EffectiveConfigName(state *InstanceState, cfg *config.WorkspaceConfig) (string, error)`
  helper. When `state.ConfigNameOverride` is non-empty, the helper
  re-validates it via `ValidateInitName` (defense in depth against
  persistence-boundary tampering, per Security Considerations §4) and
  returns the override on success, an error on validation failure.
  When the override is empty, returns `cfg.Workspace.Name` (no
  validation — already validated by config load).
- **`internal/workspace/apply.go`** — call-site updates at TWO sites
  in TWO different methods on `*Applier`. (Earlier drafts of this
  design referred to a single "Apply.Create" call site at line 407;
  that line is actually inside `Applier.Apply`, the subsequent-apply
  path. Both sites need updating.)
  - **Site 1: `Applier.Create` (around lines 258-276), first-time
    instance creation.** Replace `configName := cfg.Workspace.Name`
    (line 262) with `configName, err := EffectiveConfigName(initState,
    cfg)` (with error propagation). `initState` here is the workspace-
    root init state already loaded earlier in `Create` (around line
    231); the override flows through naturally, and the re-validation
    closes the persistence-boundary tampering vector. The
    `instanceNumberFromName` call at line 258 MUST receive the same
    effective `configName` (not raw `cfg.Workspace.Name`); the
    function does string comparison/prefix-parsing against
    `instanceName`, so passing the raw value when the override is in
    play makes the function return 0 for what should be instance 1.
  - **Site 2: `Applier.Apply` (around lines 407-425), subsequent
    apply on an existing instance.** Replace
    `configName := cfg.Workspace.Name` with the same
    `EffectiveConfigName(initState, cfg)` call. `Applier.Apply` does
    not load init state today; the implementation needs to load it
    from `filepath.Dir(instanceRoot)` (the workspace root, since
    instances are nested under workspaces). Without this update,
    subsequent applies overwrite `ConfigName` in instance state with
    the raw cfg value, and the override silently disappears on the
    first `niwa apply` run after init.
- **`internal/workspace/status.go`** — call-site update.
  - Status reads from instance state directly (state.ConfigName is
    already populated by Apply with the effective name); minimal
    change expected. If status ever loads `cfg` and reads
    `cfg.Workspace.Name`, it should switch to `EffectiveConfigName`.
- **README.md, internal/cli/init.go (Long help)** — documentation
  updates per PRD R11/R12.
- **`test/functional/features/`** — new `@critical` Gherkin scenario
  per PRD AC-25.

### Data Flow

```
niwa init my-name --from org/upstream
        |
        v
runInit
   |--- validateInitName("my-name") ----------> OK
   |--- targetDir = filepath.Join(cwd, "my-name")
   |--- os.Lstat(targetDir) -------------------> ErrNotExist (happy path)
   |--- workspace.CheckInitConflicts(targetDir) -> nil (target dir doesn't
   |                                                    exist; nested-instance
   |                                                    check walks parent)
   |--- registry collision check:
   |       globalCfg.LookupWorkspace("my-name")?
   |         entry exists AND entry.Root != targetDir:
   |           --rebind set => proceed, queue confirmation warning
   |           --rebind unset => return ErrRegistryNameInUse with
   |                             suggestion naming both --rebind and
   |                             config.GlobalConfigPath()
   |--- os.Mkdir(targetDir, 0o755)
   |--- workspace.MaterializeFromSource(ctx, src,
   |       source, targetDir+"/.niwa", fetcher, reporter)
   |--- if rebind queued: emit stderr warning naming both Root paths
   |--- buildInitState(..., overrideName="my-name") ->
   |       state.ConfigNameOverride = "my-name"
   |--- SaveState(targetDir, state)
   |--- if cfg.Workspace.Name != "my-name":
   |       fmt.Fprintln(stderr, "note: workspace name 'my-name'
   |                              overrides 'upstream' from cloned config.")
   |--- printSuccess(stdout, mode, "my-name", absPath(targetDir))


niwa apply  (first time)
        |
        v
Applier.Create  (apply.go:~258-278)
   |--- LoadState(workspaceRoot) -> initState
   |       (initState.ConfigNameOverride == "my-name")
   |--- configName, _ := EffectiveConfigName(initState, cfg)
   |       = "my-name" (override wins)
   |--- instanceNumber := instanceNumberFromName(configName, instanceName)
   |       (uses effective name, not raw cfg.Workspace.Name)
   |--- state := InstanceState{ ConfigName: &configName,
   |                            InstanceName: configName, ... }
   |--- SaveState(instanceRoot, state)
   |       writes <instanceRoot>/.niwa/instance.json
   |       — different file from the workspace-root one above


niwa apply  (subsequent)
        |
        v
Applier.Apply  (apply.go:~407-425)
   |--- workspaceRoot := filepath.Dir(instanceRoot)
   |--- LoadState(workspaceRoot) -> initState
   |       (still has ConfigNameOverride = "my-name")
   |--- configName, _ := EffectiveConfigName(initState, cfg)
   |       = "my-name"
   |--- state := InstanceState{ ConfigName: &configName, ... }
   |--- SaveState(instanceRoot, state)


niwa status (later)
        |
        v
formatStatus
   |--- LoadState(instanceRoot) -> ConfigName == "my-name"
   |       (Applier.Create / Applier.Apply already propagated it)
   |--- displays "my-name"
```

### Schema (in instance.json)

**Important file-path distinction.** The same `InstanceState` struct
is serialized to two different files at two different roots:

- `<workspaceRoot>/.niwa/instance.json` — the **init-state file**,
  written by `runInit` (init.go line 234: `SaveState(cwd, state)`
  where `cwd` is the workspace root). Carries init-time fields:
  `SkipGlobal`, `OverlayURL`, `DisclosedNotices`, and now
  `ConfigNameOverride`. `ConfigName`, `InstanceName`, and apply-time
  fields are typically empty here.
- `<instanceRoot>/.niwa/instance.json` — the **instance-state file**,
  written by `Applier.Create` and `Applier.Apply` (apply.go lines
  278 and 425: `SaveState(instanceRoot, state)`). Carries the
  effective `ConfigName`, `InstanceName`, `InstanceNumber`,
  `ManagedFiles`, etc.

Same struct, same filename, different directories. Both files are
loaded via `LoadState(<dir>)` which resolves to
`<dir>/.niwa/instance.json`. The override propagates as follows:
init writes `ConfigNameOverride` into the workspace-root file;
`Applier.Create` reads the workspace-root file, resolves the effective
name via `EffectiveConfigName`, and writes the resolved name as
`ConfigName` into the instance-root file. Subsequent `Applier.Apply`
runs read the workspace-root file again to keep the resolution
consistent across applies.

Today (post-apply instance-state file under instance dir, no
override path exists):
```json
{
  "schema_version": 3,
  "config_name": "upstream",
  "instance_name": "upstream",
  ...
}
```

After this PRD, post-init / pre-apply (init-state file at workspace
root, written by `runInit`):
```json
{
  "schema_version": 3,
  "config_name_override": "my-name",
  "skip_global": false,
  ...
}
```

After this PRD, post-apply (instance-state file under instance dir,
written by `Applier.Create`):
```json
{
  "schema_version": 3,
  "config_name": "my-name",
  "instance_name": "my-name",
  ...
}
```

### Error Rendering

The new `ErrTargetDirExists` flows through the existing
`InitConflictError` path with no rendering changes. Example output:

```
Error: /home/dan/foo already exists (directory)
  Pick a different name or remove the path and retry.
```

For the niwa-aware sub-cases (R6), existing error messages are
unchanged:

```
# <cwd>/foo/.niwa/workspace.toml exists
Error: found .niwa/workspace.toml
  Use niwa apply to update the existing workspace

# <cwd>/foo/.niwa/ exists without workspace.toml
Error: found .niwa directory without workspace.toml
  Remove the /home/dan/foo/.niwa directory and retry
```

For the new registry-collision case, the rendered error makes both
remediation paths explicit:

```
Error: workspace name 'my-team' is already registered (root: /old/path)
  Pass --rebind to retarget the entry to this directory, or remove
  the [registry.my-team] section from /home/dan/.config/niwa/config.toml
  and retry.
```

## Implementation Approach

The work splits cleanly into five steps that can land as one PR or be
sequenced. A single PR is recommended because the surfaces are tightly
coupled (init.go calls into preflight which references the new
sentinel, which is consumed by tests that depend on the state schema
change).

**Step 1: shared validation.** Add `internal/workspace/validate.go`
with the exported `ValidateInitName` function. Unit tests cover the
regex, the `.` / `..` / `.niwa` / empty rejections, and error message
shape (quoting + allowed-set description).

**Step 2: state schema (additive).** Add `ConfigNameOverride` to
`InstanceState`. Update existing state-file unit tests to confirm the
field marshals/unmarshals correctly with `omitempty` semantics. No
schema bump.

**Step 3: preflight sentinel.** Add `ErrTargetDirExists` to
`preflight.go`. No behavioral change yet; the sentinel is consumed in
Step 4.

**Step 4: init.go control flow.** Reorganize `runInit` to:
- Add the `--rebind` boolean flag to `initCmd` and thread its value
  into `runInit`.
- Call `workspace.ValidateInitName(name)` before any filesystem touch
  when a positional name is given.
- Compute `workspaceRoot` (= `targetDir = filepath.Join(cwd, name)`
  when name given, else cwd).
- Run the new caller-side existence check on `workspaceRoot` when name
  given (returns `ErrTargetDirExists` for any pre-existing path,
  routes to `ErrWorkspaceExists`/`ErrNiwaDirectoryExists` per R6).
- Call `CheckInitConflicts(workspaceRoot)`.
- Run the registry-collision check: load the global config, look up
  the name, compare `entry.Root` against `workspaceRoot`. If they
  differ and `--rebind` was not given, return `ErrRegistryNameInUse`
  with the suggestion text built from `config.GlobalConfigPath()`.
  R5 takes precedence over this check (target-exists wins) regardless
  of `--rebind`.
- `os.Mkdir(workspaceRoot, 0o755)` when name given (NOT `MkdirAll`;
  parent is cwd, guaranteed to exist; `Mkdir` fails on raced symlinks).
- Pass `workspaceRoot` (not `cwd`) into all downstream calls
  (`Scaffold`, `MaterializeFromSource`, `SaveState`).
- Populate `state.ConfigNameOverride` in `buildInitState`.
- When `--rebind` was given and a rebind actually happened, emit the
  prominent stderr confirmation (`WARNING:` prefix, both `Root`
  paths, trailing blank line) per Security §6.
- Emit the override note when applicable (per R4).
- Update `printSuccess` to include `absPath(workspaceRoot)` resolved
  through `filepath.EvalSymlinks`.

**Step 5: effective-name helper and call-site updates.** Add
`workspace.EffectiveConfigName` in a new file. The helper
re-validates `ConfigNameOverride` via `ValidateInitName` when present
(defense in depth per Security §4). Update **both** apply.go call
sites:

- `Applier.Create` (apply.go:~258-278): replace
  `configName := cfg.Workspace.Name` (line 262) with the helper call,
  and pass the resulting `configName` to `instanceNumberFromName`
  (line 258) instead of `cfg.Workspace.Name`. `Applier.Create`
  already loads init state from the workspace root earlier in the
  function (around line 231), so `initState` is in scope.
- `Applier.Apply` (apply.go:~407-425): replace
  `configName := cfg.Workspace.Name` (line 407) with the helper
  call. `Applier.Apply` does not load init state today; add a
  `LoadState(filepath.Dir(instanceRoot))` call at the top of the
  effective-name resolution block. Tolerate
  `state.ErrStateNotFound` here (an existing instance with a
  missing workspace-root init file falls back to
  `cfg.Workspace.Name`); other errors propagate.

Both call sites propagate any validation error from the helper.
Once both sites write the resolved name into the instance-state
file's `ConfigName` and `InstanceName`, downstream readers that
consult instance state (status, completion, etc.) get the right
value automatically. No other audit-required surfaces are expected;
v1 audit shows the two apply.go sites as the only writers of
`ConfigName` to instance state.

**Step 6: documentation and tests.** Update `init.go` `Long:` help
text. Update README quickstart, "shared workspace configs" section,
registry-replay example, and commands-table row per PRD R12. Add
unit tests for the new existence-check path, `EffectiveConfigName`
(including the persistence-boundary re-validation case), and the
override-note conditions. Add the `@critical` Gherkin scenario
covering end-to-end init → status → go for
`niwa init <name> --from <fixture>`. Add a unit test confirming
`os.Mkdir` (not `MkdirAll`) is the call site by exercising a symlink
race or by code inspection in the test.

### Test Plan Outline

**Unit tests (Go):**
- `internal/cli/init_test.go`: existing `TestRunInit_*` cases need
  assertion-path updates (the workspace root moves from cwd to
  `<cwd>/<name>` for named modes). New cases:
  - Named init creates `<cwd>/<name>/.niwa/workspace.toml`.
  - Named init when `<cwd>/<name>` is a regular file errors with
    `ErrTargetDirExists` and qualifier `file`.
  - Named init when `<cwd>/<name>` is a directory errors similarly
    with qualifier `directory`.
  - Named init when `<cwd>/<name>` is a symlink errors with qualifier
    `symlink`, regardless of resolution.
  - Named init when `<cwd>/<name>/.niwa/workspace.toml` exists routes
    to `ErrWorkspaceExists` (R6 sub-case routing).
  - Named init when `<cwd>/<name>/.niwa/` exists without
    `workspace.toml` routes to `ErrNiwaDirectoryExists`.
  - `validateInitName` rejects `"foo bar"`, `"foo/bar"`, `..`, `.`,
    `""` with quoted-input errors.
  - Registry collision without `--rebind`: errors with
    `ErrRegistryNameInUse`, no filesystem writes, registry unchanged,
    suggestion text contains both `--rebind` and the resolved global
    config path.
  - Registry collision with `--rebind`: success path proceeds,
    registry `Root` updated, prominent stderr warning emitted, old
    directory untouched.
  - `--rebind` passed when no collision exists: succeeds normally
    (flag is a no-op when there is nothing to rebind); no warning
    emitted.
  - R5 precedence over R8: target-exists wins over registry collision
    even when `--rebind` is given.
  - Override note emitted when explicit name differs from cloned name.
  - Override note NOT emitted when names match.
- `internal/workspace/state_test.go`: `ConfigNameOverride` round-trips
  through SaveState/LoadState; `omitempty` keeps it out of zero-value
  marshals; old state files (no override field) load with empty value.
- `internal/workspace/preflight_test.go`: `ErrTargetDirExists` and
  `ErrRegistryNameInUse` are exposed and wrap via `InitConflictError`.
- New `internal/workspace/effective_name_test.go`: helper returns
  override when set, falls back to cfg name when not, handles nil
  state.

**Functional tests (Gherkin):**
- New `@critical` scenario in `test/functional/features/critical-path.feature`
  (or a new feature file): from a fresh tmp dir, run
  `niwa init my-ws --from <fixture>`, then assert
  `<cwd>/my-ws/.niwa/workspace.toml` exists, the registry has
  `my-ws` with `Root = <cwd>/my-ws`, `niwa go my-ws` from outside
  lands in `<cwd>/my-ws`, and `niwa status` shows `my-ws`.
- Add a scenario covering target-dir-exists rejection.
- Add a scenario covering registry-collision rejection without
  `--rebind` (asserts `ErrRegistryNameInUse`, error mentions both
  `--rebind` and the global config path, no filesystem writes, registry
  unchanged).
- Add a scenario covering successful rebind with `--rebind` (asserts
  the registry `Root` is updated, the old directory at the previous
  `Root` is untouched, and `niwa go <name>` from outside lands in the
  new location).
- Existing scenarios calling `niwa init from config repo "<name>"`
  may need step-implementation updates if they pre-create the target
  directory; spot-audit during implementation.

## Security Considerations

A Phase 5 security review identified the threats below. The design
applies mitigations inline where they're cheap and preserves the
PRD's explicit UX choices where they conflict, with the trade-off
documented for each case.

### Addressed by implementation

1. **Path manipulation via `<name>`.** The positional name flows into
   `filepath.Join(cwd, name)` and is used as a directory name. Without
   validation, names containing `/`, `..`, or absolute paths would
   redirect the workspace creation outside cwd. PRD R7 pins
   validation to `^[a-zA-Z0-9._-]+$` with explicit `.` / `..` rejection
   and empty-string rejection, applied upfront. Because the regex is
   ASCII-positive-allowlist, it inherently excludes all control
   characters, all Unicode bidi/RTL/LTR override characters, newlines,
   tabs, and NUL bytes — terminal-escape injection at this layer is
   foreclosed.

2. **`.niwa` blacklist.** The regex permits `.niwa` (the literal
   directory name niwa itself uses for state). A workspace named
   `.niwa` would create `<cwd>/.niwa/.niwa/workspace.toml`, where the
   outer `.niwa` is exactly the marker `CheckInitConflicts` walks up
   looking for. Subsequent niwa commands run from the parent
   directory would walk into a confused state. **Mitigation:**
   `validateInitName` rejects the literal string `.niwa` explicitly,
   alongside `.` and `..`. Other "semantically dangerous"
   leading-dot names (`.git`, `.ssh`) are NOT blacklisted; they're
   user-domain choices and PRD-deferred regex tightening would be
   the right place for any broader policy.

3. **Symlink TOCTOU between Lstat and directory creation.** The
   pre-flight uses `os.Lstat` (not `os.Stat`) so symlinks at
   `<cwd>/<name>` are detected without following. There is a window
   between `os.Lstat` returning `ErrNotExist` and the subsequent
   directory creation in which an attacker could race a symlink into
   the gap. If `os.MkdirAll` were used (as drafted), a raced
   symlink-to-attacker-controlled-directory would silently succeed —
   the clone would land in the attacker's location. **Mitigation:**
   the implementation uses `os.Mkdir(targetDir, 0o755)` for the final
   component (parent components, if any, are guaranteed to exist —
   `targetDir`'s parent is `cwd`). `os.Mkdir` fails if the target
   exists at all, including as a symlink, closing the symlink-race
   variant.

4. **`ConfigNameOverride` re-validation at apply time.**
   `ConfigNameOverride` is plain text in `<workspaceRoot>/.niwa/instance.json`.
   A persistence-boundary attacker (any process with write access to
   that file) could rewrite the override between `niwa init` and
   `niwa apply`. Since the override flows into `InstanceState.InstanceName`
   and is used as a path segment downstream, a poisoned value could
   cause directory traversal at apply time. **Mitigation:**
   `Applier.Create` re-validates the override against the same
   regex+blacklist used by init (`workspace.ValidateInitName`, exported
   from the workspace package for shared use). On validation failure,
   apply errors out rather than using the value. This costs a single
   regex check on each apply and closes the persistence-boundary
   attack.

5. **Shared validation entry point.** `ValidateInitName` is exported
   from `internal/workspace` so that any future entry point that
   ingests a workspace name (RPC, remote trigger, MCP tool call, etc.)
   reuses the same rules. The validation must be applied on the
   post-decode string at every entry point.

6. **Registry rebind requires explicit `--rebind` flag.** Without
   affirmative consent, a user lured to run
   `niwa init <known-name>` from an attacker-controlled cwd would
   silently rebind the registry so subsequent `niwa go <name>`
   landed in the attacker's location. **Mitigation applied:** the
   collision is detected before any filesystem write and surfaced
   as `ErrRegistryNameInUse` (PRD R8). The error names the existing
   `Root` so the user can verify what is registered, and the
   suggestion offers two remediations: pass `--rebind` to retarget
   the entry explicitly, or remove the entry by editing the global
   config TOML at the path resolved at call time. Users who
   genuinely intend to rebind add one word to their command; users
   tricked into running from an attacker cwd see a clear refusal
   instead of a silent hijack. The rebind path itself still emits a
   prominent `WARNING:`-prefixed stderr confirmation naming both
   `Root` paths, so an automated agent passing `--rebind`
   programmatically still leaves an audit trail.

### Documented as accepted trade-offs

7. **`EvalSymlinks` resolution in the success message.** PRD R9
   specifies `filepath.EvalSymlinks` for the success message path
   (matching macOS `/var/...` → `/private/var/...` handling
   elsewhere). If `<cwd>` contains a symlink in its ancestry, the
   resolved path printed on stdout could disclose attacker-influenced
   path segments. The threat is information disclosure / social
   engineering, not privilege escalation. **Trade-off accepted per
   PRD R9.** Users in security-sensitive contexts who need an
   un-resolved path can derive it from the registered `Root` via
   `niwa go <name> --pwd` or equivalent (no design change here).

8. **Path disclosure in errors.** Error messages and the rebind
   warning include absolute paths, which contain the username (e.g.,
   `/home/<user>/...`). This is a pre-existing niwa convention
   (`internal/cli/go.go:128, 132, 219` and most other CLI errors).
   Operators in regulated CI environments who paste niwa errors into
   external systems should be aware. **Out of scope to change** for
   this PRD; documented for transparency.

### Out of scope, documented for completeness

9. **Concurrent `niwa init <name>`.** Two concurrent invocations
   from the same cwd would both pass `os.Lstat`; the first
   `os.Mkdir` wins, the second errors. After the winning init's
   `os.Mkdir`, downstream operations (clone, state write, registry
   update) are not protected by a process-level lock. The window for
   a second invocation to interfere is small and the failure modes
   (clone errors, state-file write conflicts) are visible. niwa does
   not have a cross-process locking story for `init` today; adding
   one is **out of scope** for this PRD. Users running automated
   pipelines should serialize init invocations or accept the small
   risk of partial materialization.

10. **Symlink in cwd's ancestry.** If the user's `cwd` itself
    contains an attacker-controlled symlink in its ancestry,
    `filepath.Join(cwd, name)` lands in the attacker's location.
    This is **pre-existing user-domain behavior** (the user chose
    their cwd). The design does not attempt to detect this case;
    documented as a non-goal.

11. **Clean-break old-pattern footgun.** A user running
    `mkdir foo && cd foo && niwa init foo` produces a silently-nested
    `foo/foo/` workspace. PRD Known Limitations accepts this; the
    success-message absolute path (R9) makes it visible.

### Out of scope: untouched surfaces

The change does not touch authentication, network surfaces, secret
material, vault providers, or privilege-escalation pathways. State
files live at `<workspaceRoot>/.niwa/instance.json` with `0o644`
permissions (file) inside a `0o755` workspace directory — same
permissions as today. The `ConfigNameOverride` field stores plain
text; it does not contain credentials, tokens, or secret material.

## Consequences

### Positive

- Users initializing a named workspace get the directory created for
  them; the typical first-time UX collapses from three steps to one.
- The explicit name is consistent across every niwa command, removing
  the long-standing `niwa go` ↔ `niwa status` disagreement.
- The override mechanism is a small, isolated extension of an existing
  pattern. Future state-borne overrides for other config fields can
  follow the same shape.
- The cloned config stays clean against upstream HEAD, preserving any
  future re-sync story.

### Negative

- Users who internalized the old `mkdir + cd + niwa init` flow hit a
  behavior change. When their old pattern keeps directory and name
  matching (`mkdir foo && cd foo && niwa init foo`), they get a
  silently-nested `foo/foo/` workspace; the absolute-path success
  message exposes this but doesn't actively warn (per PRD's rejection
  of the muscle-memory heuristic).
- A reader inspecting `.niwa/workspace.toml` directly sees the upstream
  name, not the override. Tooling that reads the toml without
  consulting state is misled; the design assumes such tooling is
  out of scope (debug-only).
- Two name fields now carry meaning: the toml's `[workspace] name`
  (provenance/upstream identity) and the override (user identity).
  This is a subtle but documented divergence.

### Mitigations

- **For old-pattern users:** R9's absolute-path success message
  surfaces unintended nesting; PRD R12 updates the README quickstart
  so new users don't learn the old pattern.
- **For divergence:** the `EffectiveConfigName` helper (Decision 5)
  centralizes resolution so future readers can adopt the right path
  with one line.
- **For the toml inspector:** the override field name
  (`config_name_override`) makes the divergence explicit when
  inspecting `.niwa/instance.json`.

### Migration

- No migration needed for state files. Old state files load with an
  empty `ConfigNameOverride` and behave as today. New state files
  with a populated override degrade gracefully on older binaries to
  today's behavior (override silently ignored).
- No migration needed for cloned workspace.toml files; they are
  never modified by this change.
