---
status: Proposed
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
  shape. `Apply.Create` and downstream readers consult init state via the
  existing `LoadState(workspaceRoot)` path and prefer the override over
  `cfg.Workspace.Name` when populated. The cloned `.niwa/workspace.toml`
  is never modified on disk.
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

Proposed

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
   populated by `Apply.Create` from `cfg.Workspace.Name` (apply.go:407-411).

The architectural challenge is to thread an override value from
`niwa init`'s argument parsing through to `Apply.Create`'s instance
state without (a) modifying the cloned config on disk, (b) bumping the
state schema version, or (c) growing the `CheckInitConflicts` signature.
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
fields (`SkipGlobal`, `NoOverlay`, `OverlayURL`). `Apply.Create` and
other readers consult init state and prefer the override over
`cfg.Workspace.Name` when set. **(Chosen.)**

- Pros: matches the existing `SkipGlobal`/`NoOverlay`/`OverlayURL`
  pattern exactly; the cloned toml is never modified; reuses the
  established `LoadState(workspaceRoot)` plumbing in `Apply.Create`;
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
`Apply.Create` reads `initState.ConfigNameOverride`; status formatter
does the same; etc.

- Pros: no new abstraction; the override is right at the surface where
  it's used.
- Cons: scatter logic that "decide which name to surface" across N
  files; risk of one site forgetting and reverting to
  `cfg.Workspace.Name`.

**Option 5B: Add a small helper, e.g.
`workspace.EffectiveConfigName(state *InstanceState, cfg
*config.WorkspaceConfig) string`, that encapsulates "use override if
set, else cfg.Workspace.Name".** Called by `Apply.Create` (when
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

## Decision Outcome

The chosen design implements the PRD as follows:

1. **State surface.** `InstanceState` gains
   `ConfigNameOverride string` with `json:"config_name_override,omitempty"`.
   Schema version stays at v3; the field is purely additive.
2. **Init control flow.** `runInit` validates the positional `<name>`,
   computes `targetDir = filepath.Join(cwd, name)` when given, runs
   the new caller-side existence check, calls
   `CheckInitConflicts(targetDir)` for niwa-state sub-case routing,
   creates the target directory with `os.MkdirAll`, dispatches to the
   mode-specific scaffold/clone using `targetDir` as the workspace root,
   detects registry rebind and queues a stderr warning, populates
   `ConfigNameOverride` in the init state when applicable, persists
   state via `SaveState(targetDir, state)`, and prints a success
   message that includes the absolute path.
3. **Preflight ordering.** Validation → existence check → niwa-state
   sub-case routing (workspace exists, orphan `.niwa/`) → nested-instance
   walk-up → registry rebind detection. The order is documented in
   `runInit` as a code comment and asserted by unit tests.
4. **Effective-name resolution.** A helper
   `workspace.EffectiveConfigName(state, cfg)` returns
   `state.ConfigNameOverride` if non-empty, else `cfg.Workspace.Name`.
   `Apply.Create` uses it when constructing post-apply state.
   `niwa status` formatting uses it. Other readers can adopt as needed.
5. **Override note.** When the override is being set and differs from
   the cloned config's `[workspace] name`, init emits the per-invocation
   stderr note specified in PRD R4. The note is not persisted to state.
6. **Error implementation.** New sentinel `ErrTargetDirExists` in
   `preflight.go`. The caller wraps it in `InitConflictError{Detail,
   Suggestion}` per the existing pattern. Detail includes the absolute
   path and a path-type qualifier (`file`/`directory`/`symlink`) from
   `os.Lstat`.
7. **Success message.** `printSuccess` is reworked to include the
   absolute workspace root path resolved via `filepath.EvalSymlinks`
   (matching how niwa elsewhere handles macOS `/var/...` →
   `/private/var/...`).

## Solution Architecture

### Components

- **`internal/cli/init.go`** — primary control flow change.
  - New: `validateInitName(name string) error` returning a typed
    validation error that quotes the offending input.
  - Modified: `runInit` reorganized to compute `workspaceRoot`,
    enforce preflight order, and thread `workspaceRoot` into existing
    calls.
  - Modified: `printSuccess` now takes the workspace root path and
    includes it in the printed line. `runInit` resolves the path
    via `filepath.EvalSymlinks` before passing.
  - Modified: `buildInitState` accepts the optional override and
    populates `ConfigNameOverride` when set.
- **`internal/workspace/preflight.go`** — new sentinel.
  - New: `ErrTargetDirExists = errors.New("target path already exists")`.
  - Existing `CheckInitConflicts` is unchanged.
- **`internal/workspace/state.go`** — schema-additive change.
  - New field: `ConfigNameOverride string \`json:"config_name_override,omitempty"\``.
  - Schema version unchanged at v3.
- **`internal/workspace/effective_name.go` (new file)** —
  `EffectiveConfigName(state *InstanceState, cfg *config.WorkspaceConfig) string`
  helper. Returns `state.ConfigNameOverride` if non-empty, else
  `cfg.Workspace.Name`. State may be nil (returns cfg.Workspace.Name).
- **`internal/workspace/apply.go`** — call-site updates.
  - In `Apply.Create` (around line 407-411), replace
    `configName := cfg.Workspace.Name` with
    `configName := EffectiveConfigName(initState, cfg)` and
    `InstanceName: configName`. The init state is already loaded at
    line 231; the override flows naturally.
- **`internal/workspace/status.go`** — call-site update.
  - At line 69-70, after reading `state.ConfigName`, also consult the
    override path via `EffectiveConfigName` if `cfg` is in scope
    (status reads from instance state directly today, so the override
    propagates through `Apply.Create`'s state write — minimal change
    expected).
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
   |--- os.MkdirAll(targetDir, 0o755)
   |--- workspace.MaterializeFromSource(ctx, src,
   |       source, targetDir+"/.niwa", fetcher, reporter)
   |--- detect rebind: globalCfg.LookupWorkspace("my-name")?
   |       Root != targetDir => emit stderr warning
   |--- buildInitState(..., overrideName="my-name") ->
   |       state.ConfigNameOverride = "my-name"
   |--- SaveState(targetDir, state)
   |--- if cfg.Workspace.Name != "my-name":
   |       fmt.Fprintln(stderr, "note: workspace name 'my-name'
   |                              overrides 'upstream' from cloned config.")
   |--- printSuccess(stdout, mode, "my-name", absPath(targetDir))


niwa apply  (later)
        |
        v
Apply.Create
   |--- LoadState(workspaceRoot) -> initState (with ConfigNameOverride="my-name")
   |--- configName := EffectiveConfigName(initState, cfg)
   |       = initState.ConfigNameOverride = "my-name"
   |--- InstanceState{ ConfigName: &configName,
   |                  InstanceName: configName, ... }
   |--- SaveState(instanceRoot, state)


niwa status (later)
        |
        v
formatStatus
   |--- LoadState(instanceRoot) -> ConfigName == "my-name" (already
   |                              propagated by Apply)
   |--- displays "my-name"
```

### Schema (in instance.json)

Before:
```json
{
  "schema_version": 3,
  "config_name": "upstream",
  "instance_name": "upstream",
  ...
}
```

After (init-state file at workspace root, before any apply):
```json
{
  "schema_version": 3,
  "config_name_override": "my-name",
  "skip_global": false,
  ...
}
```

After apply (instance-state file under instance dir):
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

## Implementation Approach

The work splits cleanly into five steps that can land as one PR or be
sequenced. A single PR is recommended because the surfaces are tightly
coupled (init.go calls into preflight which references the new
sentinel, which is consumed by tests that depend on the state schema
change).

**Step 1: state schema (additive).** Add `ConfigNameOverride` to
`InstanceState`. Update existing state-file unit tests to confirm the
field marshals/unmarshals correctly with `omitempty` semantics. No
schema bump.

**Step 2: preflight sentinel.** Add `ErrTargetDirExists` to
`preflight.go`. No behavioral change yet; the sentinel is consumed in
Step 3.

**Step 3: init.go control flow.** Reorganize `runInit` to:
- Add `validateInitName` and apply it before any filesystem touch.
- Compute `workspaceRoot` (= `targetDir` when name given, else cwd).
- Run the new existence check on `workspaceRoot` when name given.
- Call `CheckInitConflicts(workspaceRoot)`.
- `os.MkdirAll(workspaceRoot)` when name given (and target didn't exist).
- Detect registry rebind; queue stderr warning.
- Pass `workspaceRoot` (not `cwd`) into all downstream calls.
- Populate `state.ConfigNameOverride` in `buildInitState`.
- Emit the override note when applicable (per R4).
- Update `printSuccess` to include `absPath(workspaceRoot)` resolved
  through `filepath.EvalSymlinks`.

**Step 4: effective-name helper and call-site updates.** Add
`workspace.EffectiveConfigName` in a new file. Update
`Apply.Create` to use it when constructing post-apply state (apply.go
~line 407-411). Audit other readers of `cfg.Workspace.Name` for places
where the effective name should be used; in practice the v1 audit
shows `Apply.Create` is the load-bearing surface — once it writes the
override into the instance-level state file's `ConfigName` and
`InstanceName`, downstream readers that consult the instance state
(status, completion, etc.) get the right value automatically.

**Step 5: documentation and tests.** Update `init.go` `Long:` help
text. Update README quickstart, "shared workspace configs" section,
registry-replay example, and commands-table row per PRD R12. Add
unit tests for `validateInitName`, the new existence-check path,
`EffectiveConfigName`, and the override-note conditions. Add the
`@critical` Gherkin scenario covering end-to-end init → status →
go for `niwa init <name> --from <fixture>`.

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
  - Registry rebind detected: warning emitted, success path proceeds,
    registry `Root` updated, old directory untouched.
  - Override note emitted when explicit name differs from cloned name.
  - Override note NOT emitted when names match.
- `internal/workspace/state_test.go`: `ConfigNameOverride` round-trips
  through SaveState/LoadState; `omitempty` keeps it out of zero-value
  marshals; old state files (no override field) load with empty value.
- `internal/workspace/preflight_test.go`: `ErrTargetDirExists` is
  exposed and wraps via `InitConflictError`.
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
- Existing scenarios calling `niwa init from config repo "<name>"`
  may need step-implementation updates if they pre-create the target
  directory; spot-audit during implementation.

## Security Considerations

The change introduces three new vectors worth scrutiny:

1. **Path manipulation via `<name>`.** The positional name flows into
   `filepath.Join(cwd, name)` and is used as a directory name. Without
   validation, names containing `/`, `..`, or absolute paths would
   redirect the workspace creation outside cwd. The PRD pins
   validation (R7) to the existing `^[a-zA-Z0-9._-]+$` regex with
   explicit `.` / `..` rejection and empty-string rejection, applied
   before any filesystem write. This forecloses traversal and absolute-
   path injection at the input layer.

2. **Symlink and TOCTOU on the existence check.** The pre-flight uses
   `os.Lstat` (not `os.Stat`) so symlinks are not followed; a malicious
   symlink at `<cwd>/<name>` can't trick the check into thinking the
   target doesn't exist. There is a TOCTOU window between `os.Lstat`
   returning `ErrNotExist` and the subsequent `os.MkdirAll` (a third
   party could race a malicious file or symlink into the target before
   `MkdirAll`). Mitigation: `os.MkdirAll` succeeds when the target
   already exists as a directory and the content goes inside it; for a
   raced regular file or symlink, `os.MkdirAll` would fail with a
   meaningful error (file exists or path component is not a directory),
   which short-circuits the rest of init. The race window is narrow
   and the failure mode is loud, so no further mitigation (e.g.,
   atomic-create-via-mkdir-then-stat) is warranted in v1.

3. **Stderr message content.** The override note (R4) and the rebind
   warning (R8) include user-supplied values (`<name>`, registry
   `Root` paths). niwa already prints user-supplied paths to stderr
   in many places (`internal/cli/go.go:128, 132, 219` etc.); the new
   messages follow the same convention. There is no shell-execution
   surface where these strings would be re-interpreted; the risk is
   limited to terminal-escape injection if a user-controlled name
   reached stderr. Validation in R7 rejects control characters (the
   regex excludes them), closing this vector.

The change does not touch authentication, network surfaces, secret
material, or privilege-escalation pathways. The override field in
state is plain text; it does not store credentials. State files
already live at `~/.niwa/.../instance.json` with the same permissions
they have today.

A jury-style security review may surface additional considerations;
the design includes a Phase 5 security agent run before the PR opens.

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
