---
schema: design/v1
status: Current
upstream: docs/prds/PRD-dispatch-default-permission-mode.md
problem: |
  `niwa dispatch` derives a worker's permission mode only from the explicit
  flag or the instance's recorded workspace posture, so every machine running
  a workspace gets the same mode. The PRD asks for a `[global]
  dispatch_permission_mode` key in the machine config that sits between the
  flag and the workspace posture, is validated fail-closed, means nothing to
  Codex, has a `niwa config` setter, and starts the workspace posture's
  deprecation.
decision: |
  Add `DispatchPermissionMode` to `config.GlobalSettings` with one validating
  accessor in `internal/config` that both dispatch and the setter call. In
  `runDispatch`, make step (2a) fail when the machine config can't be loaded
  or the key is invalid, before the workspace config is read. Extend
  `derivePermissionMode` with a host argument and a typed source result, and
  print the new stderr line from the source. Add a
  `dispatch-permission-mode` pair to `niwa config set/unset` modeled on the
  default-harness commands, and append a deprecation warning in
  `config.Parse` when `[claude.settings]` or `[instance.claude.settings]`
  declares `permissions`.
rationale: |
  Every piece reuses a seam that already exists for a sibling feature: the
  `[global]` struct and loader, the single derivation function, the setter
  pattern, and Parse's warnings list. Keeping the mode check in one config
  function means the dispatch path and the setter can't disagree about the
  value set. Moving the fail-closed check to (2a), rather than near the
  derivation at (9a), is what keeps a broken setting from provisioning an
  instance first. Rejected alternatives: validating at the derivation site
  (too late), a niwa-owned vocabulary (the PRD settled on Claude's
  spellings), and folding the host value into the recorded instance state
  (it would freeze a machine setting into a per-instance record).
decision_provenance: inline-resolved
---

# DESIGN: dispatch-default-permission-mode

## Status

Current

## Context and Problem Statement

The PRD (`docs/prds/PRD-dispatch-default-permission-mode.md`, R1 to R14)
settles what the feature does. This design settles where each piece lives in
niwa's code and in what order dispatch does things.

Today's relevant code:

- `internal/config/registry.go` holds `GlobalConfig` and `GlobalSettings`,
  the `[global]` table of `~/.config/niwa/config.toml`.
  `LoadGlobalConfigFrom` returns an empty config for a missing file and an
  error for an unreadable or unparseable one.
- `internal/cli/dispatch.go` `runDispatch` loads that config once at step
  (2a), best-effort: an error is kept in `gcErr` and every consumer treats it
  as "unset". The workspace config is read right after, at (2b), for agent
  resolution. The instance is provisioned much later.
- At (9a-derive), `derivePermissionMode(explicit, recorded, flags)` returns
  the explicit flag, else `bypassPermissions` when the agent's permission
  flag is `--permission-mode` and the recorded posture
  (`InstanceState.ClaudePermissions`) is `bypass`, else nothing. A `derived`
  bool drives one stderr line.
- `buildDispatchPassthrough` turns the chosen mode into
  `<flags.PermissionMode> <mode>`: `--permission-mode` for Claude,
  `--sandbox` for Codex.
- `internal/cli/config_default_harness.go` is the one `niwa config
  set/unset` pair for a `[global]` key.
- `config.Parse` collects non-fatal warnings in a list that `niwa apply`,
  `create` and `reset` print.

## Decision Drivers

- **Fail closed before side effects.** PRD R7 and R8 require a bad value or
  an unloadable config to stop dispatch before any instance directory
  exists.
- **One definition of the value set.** Dispatch and the setter must accept
  exactly the same six values (R2, R10).
- **No change without the key.** R13 pins today's argv and stderr when the
  key is absent.
- **Agent neutrality through existing seams.** The derivation already
  branches on `flags.PermissionMode`; Codex must get nothing from the host
  key (R5) while the flag keeps reaching it (R6).
- **Small surface.** Reuse the sibling features' patterns rather than adding
  a new configuration layer.

## Considered Options

All four questions are standard-tier and were resolved inline, within this
design's evaluation; none was contested enough to need a separate decision
record.

### Decision 1: Where the value is validated

**Chosen: one accessor in `internal/config`, called at dispatch (2a) and by
the setter.** A `ParseDispatchPermissionMode(raw string) (string, error)`
function trims the value, returns `""` for empty, and returns the canonical
value or an error naming the key, the value, and the six accepted values.
`GlobalConfig.DispatchPermissionMode()` applies it to the loaded value.
Provenance: inline.

*Alternative: validate inside `derivePermissionMode`.* The derivation runs at
(9a-derive), after the instance is created, so a bad value would provision an
instance and then fail, leaving the deferred destroy to clean it up. R7 asks
for no instance directory at all. Rejected.

*Alternative: validate in `ParseGlobalConfig` (TOML decode).* That would make
every command that loads the machine config fail on a bad
`dispatch_permission_mode`, including `niwa config unset`, the one command a
developer needs to fix it. Rejected.

### Decision 2: How an unloadable machine config is handled

**Chosen: dispatch returns an error at (2a) when `LoadGlobalConfig` fails.**
A missing file already loads as an empty config, so only an unreadable file,
a TOML error (a non-string `dispatch_permission_mode` included), or a
failure to resolve the config path reach this branch. The error wraps the
loader's and names the path. Provenance: inline.

*Alternative: fail only when the file's raw bytes mention
`dispatch_permission_mode`.* It would keep today's tolerance for unrelated
breakage, but a substring check is fragile and an unparseable file can't be
trusted to say what it holds. Rejected; the PRD accepts the narrower
tolerance as a known limitation.

### Decision 3: Shape of the derivation

**Chosen: `derivePermissionMode(explicit, host, recorded string, flags)
(mode string, source permissionSource)`**, where `permissionSource` is a
small enum: `sourceNone`, `sourceFlag`, `sourceHost`, `sourceWorkspace`. The
host rung applies only when `flags.PermissionMode == "--permission-mode"`,
the same guard the workspace rung uses. The caller prints R9's line from the
source: the new line for `sourceHost`, the existing line unchanged for
`sourceWorkspace`, nothing otherwise. The state-file warning keeps its
condition (`stateErr != nil && mode == ""`). Provenance: inline.

*Alternative: resolve the host value in the caller and pass it as
`explicit`.* Fewer lines, but the caller would then need its own record of
whether the value came from the flag, and the "flag always wins regardless
of agent" rule (R6) would blur into the host rule, which must not reach
Codex. Rejected.

### Decision 4: Where the deprecation warning is raised

**Chosen: in `config.Parse`, appended to `warnings` when
`cfg.Claude.Settings` or `cfg.Instance.Claude.Settings` has a `permissions`
key.** Parse already owns deprecation warnings (`[claude.content]`), and
apply, create and reset already print its list, once per parse. Provenance:
inline.

*Alternative: warn from `instancePermissionsPosture` during materialization.*
That function runs per instance and per apply step, so the warning would
repeat, and it runs inside the hook-driven path that deliberately prints no
config warnings. Rejected.

## Decision Outcome

The host key rides the existing `[global]` struct and loader. A single
config function owns the value set, and dispatch calls it at (2a), turning
step (2a) from best-effort into a gate for this one key and for loadability.
The derivation gains one rung and a typed source, which is all the stderr
change needs. The setter copies the default-harness commands. The
deprecation is one more Parse warning. Nothing touches the recorded instance
state, materialization, `niwa watch`, or `buildDispatchPassthrough`.

## Solution Architecture

### `internal/config/registry.go`

```go
// In GlobalSettings:
DispatchPermissionMode string `toml:"dispatch_permission_mode,omitempty"`

// DispatchPermissionModes is the closed set, in display order.
var DispatchPermissionModes = []string{
    "default", "acceptEdits", "plan", "auto", "dontAsk", "bypassPermissions",
}

// ParseDispatchPermissionMode trims raw and returns "" for an empty value,
// the value itself when it is in DispatchPermissionModes, or an error:
//   invalid [global] dispatch_permission_mode "atuo"; accepted values are
//   default, acceptEdits, plan, auto, dontAsk, bypassPermissions
func ParseDispatchPermissionMode(raw string) (string, error)

// DispatchPermissionMode returns the validated machine setting ("" when
// unset). A nil receiver is unset.
func (g *GlobalConfig) DispatchPermissionMode() (string, error)
```

The field comment states the precedence and that it outranks the workspace
posture, reversing `DefaultDispatchHarness`'s polarity on purpose.

### `internal/cli/dispatch.go`

Step (2a) becomes:

```go
gc, gcErr := config.LoadGlobalConfig()
if gcErr != nil {
    return fmt.Errorf("niwa: error: loading %s: %w", globalConfigPathForError(), gcErr)
}
hostPermissionMode, err := gc.DispatchPermissionMode()
if err != nil {
    return fmt.Errorf("niwa: error: %w (in %s)", err, globalConfigPathForError())
}
```

`globalConfigPathForError` returns `config.GlobalConfigPath()` or a fixed
`~/.config/niwa/config.toml` fallback when the path itself can't be
resolved. With `gcErr` now always nil past (2a), the later `gcErr == nil`
guards stay correct and can be simplified in the same change.

At (9a-derive):

```go
permissionMode, source := derivePermissionMode(dispatchPermissionMode,
    hostPermissionMode, recordedPermissions, spec.Flags)
switch source {
case sourceHost:
    fmt.Fprintf(stderr, "niwa dispatch: using --permission-mode %s from [global] dispatch_permission_mode in %s\n", permissionMode, cfgPath)
case sourceWorkspace:
    fmt.Fprintf(stderr, "niwa dispatch: derived --permission-mode %s from the workspace's declared permissions posture\n", permissionMode)
}
```

The data flow for a dispatch is:

```
config.toml --(2a) LoadGlobalConfig--> gc --DispatchPermissionMode--> host (or error: stop)
--permission-mode flag ------------------------------------------> explicit
instance.json claude_permissions --(9a) LoadState------------------> recorded
derivePermissionMode(explicit, host, recorded, flags) --> (mode, source)
buildDispatchPassthrough(..., mode) --> argv
```

### `internal/cli/config_permission_mode.go` (new)

`configSetPermissionModeCmd` (`dispatch-permission-mode <mode>`, alias
`dispatch_permission_mode`) and `configUnsetPermissionModeCmd`, registered
under `configSetCmd` and `configUnsetCmd`. Set rejects an empty argument,
calls `config.ParseDispatchPermissionMode`, loads, assigns, saves with
`SaveGlobalConfigTo`, and prints
`Dispatch permission mode set to <mode> in <path>`. Unset prints
`No dispatch permission mode set.` when the field is empty, otherwise clears
it, saves, and prints `Dispatch permission mode removed.` The long help
lists the values and the precedence.

### `internal/config/config.go`

After the `[claude.content]` block in `Parse`:

```go
if hasPermissionsPosture(&cfg) {
    warnings = append(warnings, "[claude.settings] permissions is deprecated for dispatch; "+
        "set [global] dispatch_permission_mode in ~/.config/niwa/config.toml instead "+
        "(niwa config set dispatch-permission-mode <mode>). The workspace value still applies until it is removed")
}
```

`hasPermissionsPosture` checks `cfg.Claude.Settings["permissions"]` and,
when `cfg.Instance.Claude` is non-nil, its settings map.

### Docs

`docs/guides/dispatch-permission-mode.md` covers the key, values,
precedence, Codex, the setter, and Claude Code's silent fallback. The
README's `[global]` settings list gets a line linking to it.

## Implementation Approach

1. **Config surface.** Add the field, the value set, the parser and the
   accessor with table tests (valid values, whitespace, case, empty, nil
   receiver).
2. **Dispatch.** Make (2a) fail closed, extend `derivePermissionMode` with
   the host rung and source, print the R9 line. Unit-test the derivation
   table and add dispatch-level tests over argv and stderr for the PRD's
   acceptance matrix, including Codex and the no-instance-directory checks.
   The fail-closed change has known fallout handled in the same step:
   `TestDispatch_Inbound_UnreadableHostConfig` expects dispatch to succeed
   over a broken machine config and is rewritten to expect the new error;
   the existing `derivePermissionMode` tests move to the new signature; and
   the comments at (2a) and (9b-host) plus
   `docs/guides/session-message-acceptance.md`, which describe an unreadable
   config as tolerated, are updated.
3. **Setter.** Add the `config set/unset dispatch-permission-mode` commands
   with tests over file contents and stdout.
4. **Deprecation warning.** Add the Parse warning with tests for present and
   absent keys at both levels.
5. **Docs.** Add the guide and the README link.

Steps 1 and 2 must land together or in order; 3, 4 and 5 depend only on 1.

## Security Considerations

The feature changes how much autonomy a dispatched worker gets, so it is
security-relevant even though it adds no new input channel.

- **Who controls the value.** Only the machine config, a file in the
  developer's own config directory, can set the host rung. A cloned
  workspace can't, and the host rung outranks the workspace-derived flag, so
  a shared workspace config can no longer override the mode the machine
  owner chose for dispatch. It can still raise one where the machine owner
  chose nothing, as today, and it still writes its own settings files during
  materialization; this feature only governs the launch flag.
- **Fail closed.** An invalid value or an unloadable file stops dispatch
  instead of falling through to a workspace `bypass`. This is the main
  safety property and is tested directly. A missing file is not a failure: it
  loads as no setting and the workspace rung applies, as today. A developer
  who points `XDG_CONFIG_HOME` somewhere else gets the setting in that
  location or none.
- **Codex guard is a string match.** The host rung is gated on
  `flags.PermissionMode == "--permission-mode"`, the same guard the workspace
  rung uses. A test pins that a Codex spec gets no host-derived flag, so a
  future change to the flag spellings can't silently route the value into
  `--sandbox`.
- **No argument injection.** The value is checked against a closed set
  before it reaches argv, and `buildDispatchPassthrough` already passes it as
  a discrete argv element.
- **Codex isolation.** The host value never reaches Codex's `--sandbox`, so a
  machine setting can't switch off Codex's sandbox or record Codex trust.
- **Silent downgrade.** Claude Code starts in its default mode when a mode is
  disallowed; that errs toward more prompting, not less.
- **Unchanged.** The explicit flag stays unvalidated, as today; the operator
  typed it.

## Consequences

**Positive.** One setting per machine gives the developer the mode they
want everywhere on that machine. The value set lives in one function. The
stderr line makes the source of a worker's mode visible.

**Negative.** A malformed machine config now stops dispatch where it used to
be skipped, which may surprise a developer with an unrelated typo in that
file. `niwa config set` drops comments from the file, as the harness setter
already does. Shared workspace configs that declare `permissions` print a
deprecation warning on every apply until their maintainers drop the key.

**Mitigations.** Both new errors name the file and the problem.
`niwa config unset dispatch-permission-mode` and `set` stay usable when the
bad value is a string outside the accepted set, because validation sits
outside the TOML decode. A TOML syntax error or a non-string value makes
every config command fail to load the file, as it does today for any key, so
the error message names the file for the developer to fix by hand.
