---
schema: plan/v1
status: Active
execution_mode: single-pr
split_mode_source: none
tracking_level: none
upstream: docs/designs/DESIGN-dispatch-default-permission-mode.md
milestone: "Dispatch default permission mode"
issue_count: 5
---

# PLAN: dispatch-default-permission-mode

## Status

Active

## Scope Summary

Add the `[global] dispatch_permission_mode` machine setting, wire it into
`niwa dispatch` between the explicit flag and the workspace posture with a
fail-closed check at step (2a), add the `niwa config set/unset
dispatch-permission-mode` commands, start the workspace posture's
deprecation with a parse warning, and document it.

## Decomposition Strategy

**Horizontal.** The config accessor is the one shared contract: dispatch and
the setter both call it, so it lands first. Dispatch, the setter, and the
deprecation warning are independent of each other once it exists. Docs come
last because they describe the final stderr lines and setter output. Under
the repo's default consolidated delivery nothing forces a split, so all five
land in one PR.

## Issue Outlines

### Issue 1: Add the dispatch_permission_mode value set and accessor

**Goal**: Give `internal/config` the single definition of the machine
setting and its six accepted values, per the DESIGN's config surface.

**Acceptance Criteria**:
- [ ] `GlobalSettings.DispatchPermissionMode string` with TOML tag
      `dispatch_permission_mode,omitempty` and a doc comment stating the
      precedence and that it outranks the workspace posture on purpose
- [ ] `DispatchPermissionModes` lists `default`, `acceptEdits`, `plan`,
      `auto`, `dontAsk`, `bypassPermissions` in that order
- [ ] `ParseDispatchPermissionMode` trims, returns `""` for empty or
      whitespace-only input, returns the value for each of the six, and for
      anything else (including `Auto` and `atuo`) returns an error naming
      `dispatch_permission_mode`, the value, and all six values
- [ ] `(*GlobalConfig).DispatchPermissionMode()` applies it and treats a nil
      receiver as unset
- [ ] Table tests cover all six values, whitespace, case, empty, invalid,
      and nil receiver; `go test ./internal/config/...` passes

**Dependencies**: None

**Complexity**: testable

### Issue 2: Wire the machine setting into dispatch

**Goal**: Make `niwa dispatch` fail closed at (2a) on an unloadable config or
invalid value and forward the machine setting between the flag and the
workspace posture, printing R9's line.

**Acceptance Criteria**:
- [ ] Step (2a) returns an error naming the config path when
      `LoadGlobalConfig` fails, and an error carrying the accessor's message
      and the path when the value is invalid; both happen before the
      workspace config is read, and no instance directory is created
- [ ] `derivePermissionMode(explicit, host, recorded, flags)` returns
      `(mode, source)`; flag beats host beats workspace `bypass`; host and
      workspace rungs apply only when `flags.PermissionMode` is
      `--permission-mode`; the explicit flag applies to every agent unchanged
- [ ] Stderr carries
      `niwa dispatch: using --permission-mode <mode> from [global] dispatch_permission_mode in <path>`
      for a host-sourced mode, the existing workspace line byte for byte for a
      workspace-sourced mode, and no permission-mode line otherwise
- [ ] Unit tests cover the derivation table, including a Codex spec getting
      nothing from any host value and `default` over a workspace `bypass`
- [ ] Dispatch-level tests cover the PRD acceptance matrix for argv and
      stderr: each of the six values, flag-over-host, flag-over-workspace,
      empty and whitespace values, Codex with a host value, Codex with the
      flag, invalid value (with and without the flag, and for Codex), invalid
      TOML, non-string value, an unreadable file, and a missing file
- [ ] `TestDispatch_Inbound_UnreadableHostConfig` is rewritten to expect the
      new error; existing `derivePermissionMode` tests move to the new
      signature; the comments at (2a), (9a-derive) and (9b-host) describe the
      fail-closed behavior; `docs/guides/session-message-acceptance.md` no
      longer says an unreadable config is tolerated
- [ ] A test shows `niwa watch` launches its review session with identical
      argv with and without a machine setting present
- [ ] With no machine setting, argv and stderr match today's for Claude and
      Codex under posture `bypass`, `ask`, and none (regression table test)
- [ ] Flag plus host setting yields exactly one `--permission-mode` pair
- [ ] `go test ./...` passes

**Dependencies**: <<ISSUE:1>>

**Complexity**: critical

### Issue 3: Add `niwa config set/unset dispatch-permission-mode`

**Goal**: Let a developer write and remove the machine setting from the CLI,
validated by the same accessor.

**Acceptance Criteria**:
- [ ] `niwa config set dispatch-permission-mode <mode>` (alias
      `dispatch_permission_mode`) rejects an empty argument and any value the
      accessor rejects, leaving the file byte-identical; otherwise writes the
      trimmed value, keeps other `[global]` keys, creates the file when
      missing, replaces an earlier value on a second set, and prints
      `Dispatch permission mode set to <mode> in <path>`
- [ ] `niwa config unset dispatch-permission-mode` removes the key, keeps
      other keys, and prints `Dispatch permission mode removed.`; with the key
      absent it exits zero and prints `No dispatch permission mode set.`
- [ ] Long help lists the six values and the precedence
- [ ] Tests over file contents and stdout for each case, using a temporary
      `XDG_CONFIG_HOME`

**Dependencies**: <<ISSUE:1>>

**Complexity**: testable

### Issue 4: Warn that the workspace permissions posture is deprecated

**Goal**: Start the deprecation of `[claude.settings] permissions` without
changing what it does.

**Acceptance Criteria**:
- [ ] `config.Parse` appends a warning containing `dispatch_permission_mode`
      and `~/.config/niwa/config.toml` when `[claude.settings]` or
      `[instance.claude.settings]` declares `permissions`, and no such warning
      otherwise
- [ ] Tests cover both levels and the absent case, and command-level tests
      show `niwa apply`, `niwa create` and `niwa reset` each print it once
- [ ] A test confirms an `ask` posture still writes
      `permissions.defaultMode: "default"` into the instance-root, repo, and
      workspace-root settings files; the implementer names the existing test
      that covers this or adds one

**Dependencies**: None

**Complexity**: simple

### Issue 5: Document the dispatch permission mode

**Goal**: Give developers one place that explains the machine setting.

**Acceptance Criteria**:
- [ ] `docs/guides/dispatch-permission-mode.md` has sections for the key and
      its location, the six values, the precedence, Codex behavior, the
      setter, the fail-closed behavior, the workspace posture deprecation,
      and Claude Code's silent fallback to its default mode
- [ ] The README's `[global]` settings list links to the guide
- [ ] Prose follows the repo's writing conventions (no emojis, none of the
      banned words)

**Dependencies**: <<ISSUE:2>>, <<ISSUE:3>>

**Complexity**: simple

## Dependency Graph

## Implementation Sequence

Critical path: 1, then 2, then 5. Issue 3 can run alongside 2 once 1 is in,
and 4 has no dependencies and can be done at any point. Issue 2 carries the
fail-closed behavior and the test fallout, so it is the one to review most
carefully.
