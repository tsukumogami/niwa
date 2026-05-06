---
status: Draft
problem: |
  Users initializing a niwa workspace from a remote config must currently
  pre-create the target directory and `cd` into it before running niwa init,
  forcing the awkward `mkdir foo && cd foo && niwa init foo --from org/repo`
  pattern. When an explicit name is given, niwa only persists it in the
  global registry; status, apply, and other commands continue to read the
  cloned `[workspace] name` from the toml, leaving the user-given name
  visible only to `niwa go`. Onboarding is noisier than necessary and the
  user sees inconsistent names across commands.
goals: |
  Users can initialize a workspace with a single command from any directory
  by naming it: `niwa init <name>` creates `<cwd>/<name>/` and sets up the
  workspace there, with the chosen name shown consistently across every
  niwa command. The behavior of `niwa init` and `niwa init --from <src>`
  (no positional name) does not change.
---

# PRD: niwa init creates workspace directory

## Status

Draft

## Problem Statement

Niwa today initializes a workspace in the current working directory.
Following the README quickstart, a first-time user must:

```
mkdir my-workspace && cd my-workspace
niwa init my-project --from org/repo
```

The `mkdir + cd` step adds friction with no value: the user already typed
the workspace name in the `niwa init` command, so niwa has all the
information it needs to create the directory itself.

A second, related problem hides inside the same command. When a user
provides an explicit `<name>` argument, niwa registers that name in the
global registry but doesn't apply it anywhere else. The cloned
`.niwa/workspace.toml` retains its upstream `[workspace] name`, and every
other niwa command (`niwa status`, `niwa apply`, status output, the mesh
layer) reads from the toml. So a user who runs
`niwa init my-team --from org/upstream-config` sees their chosen name
`my-team` only when they run `niwa go`; everywhere else, niwa shows
whatever the upstream config declared.

The combined effect is that workspace initialization makes users do
manual work niwa could do for them, then silently disagrees with them
about the name they chose.

## Goals

1. **Single-command init.** When a user supplies a positional `<name>`,
   niwa creates `<cwd>/<name>/` and initializes the workspace inside it.
   No mkdir, no cd.
2. **Consistent naming.** The positional `<name>`, when given, is the
   effective workspace name everywhere niwa surfaces a name (status,
   apply, registry, on-disk path), regardless of what the cloned
   `--from` config declares.
3. **Predictable failure.** When the target directory or path already
   exists, niwa rejects the operation cleanly before any writes happen.
4. **Backward-compatible no-name flow.** `niwa init` (no args) and
   `niwa init --from <src>` (no positional name) continue to initialize
   in the current directory; their behavior is unchanged.
5. **Clear migration.** Users who learned the old `mkdir + cd + init`
   flow find updated documentation and a success message that surfaces
   the workspace's absolute path, so they can immediately see where the
   workspace landed.

## User Stories

**US-1**: As a first-time niwa user following the README quickstart, I
run `niwa init my-project --from org/recipe` from my home directory and
land with a workspace at `~/my-project/`, without remembering to mkdir or
cd first.

**US-2**: As a developer setting up a shared workspace from my team's
config, I run `niwa init my-team --from my-org/workspace-config` from
any parent directory and find the workspace at `<cwd>/my-team/`,
predictably named.

**US-3**: As a power user managing several concurrent niwa workspaces,
I spin up a new one with a single `niwa init <name> --from <src>`
command, without context-switching directories first.

**US-4**: As an existing user with a registered workspace, I run
`cd ~/projects && niwa init my-team` to re-clone the registered config
into `~/projects/my-team/`. Niwa warns me on stderr that the registry
entry's root path is being rebound from its previous location.

**US-5**: As a user passing `niwa init my-name --from org/upstream`,
when I subsequently run `niwa status` and `niwa apply`, I see the name
`my-name` rather than whatever the upstream config declared.

**US-6**: As a user invoking the old pattern by habit
(`mkdir foo && cd foo && niwa init foo --from src`), the success message
shows the workspace path `<cwd>/foo/foo/`, making the unintended nesting
immediately visible so I can correct course.

**US-7**: As a CI/automation operator, I read the `niwa init --help`
output and the README to update my pipeline scripts. The new directory
creation behavior is described explicitly in both places, with no
interpretation required.

## Requirements

### Functional Requirements

**R1**: When `niwa init <name>` is invoked with an explicit positional
`<name>`, niwa MUST create `<cwd>/<name>/` and initialize the workspace
inside it. This applies whether or not `--from` is also given.

**R2**: When `niwa init` is invoked without a positional `<name>` (zero
args, with or without `--from`), niwa MUST initialize the workspace in
the current working directory. Behavior is unchanged from today.

**R3**: When `niwa init <name> --from <src>` is invoked, the explicit
`<name>` MUST be the effective workspace name everywhere niwa surfaces
a name: `niwa status`, `niwa apply`, the global registry, the success
message, and any other CLI output. The cloned `[workspace] name` field
in `.niwa/workspace.toml` MUST NOT be modified on disk; the override
MUST be persisted in instance state and consulted by downstream readers.

**R4**: When the cloned `--from` config's `[workspace] name` differs from
the explicit positional `<name>`, niwa MUST emit a one-time stderr note
on init success indicating the override
(e.g., "note: workspace name `<name>` overrides `<config-name>` from
cloned config").

**R5**: When `niwa init <name>` is invoked and `<cwd>/<name>` already
exists at any path type (file, directory, symlink, other), niwa MUST
reject the operation with a `InitConflictError` before any filesystem
writes. The error MUST identify the conflicting absolute path and the
path type (file / directory / symlink), and MUST suggest a single
remediation option. No subdirectory of `<cwd>/<name>` may be created.

**R6**: When `<cwd>/<name>` already exists AND contains a niwa workspace
(`.niwa/workspace.toml` is present at `<cwd>/<name>/.niwa/workspace.toml`),
niwa MUST surface the existing `ErrWorkspaceExists` error with the
"Use `niwa apply` to update" suggestion, instead of the generic target-
exists error. When `<cwd>/<name>/.niwa/` exists without a
`workspace.toml`, niwa MUST surface the existing `ErrNiwaDirectoryExists`
error with the "Remove the `.niwa` directory" suggestion.

**R7**: The positional `<name>` MUST be validated against the existing
`^[a-zA-Z0-9._-]+$` regex (the same regex that governs `[workspace] name`
in `internal/config/config.go`). The literals `.` and `..` MUST be
rejected explicitly (they pass the regex but are path-traversal
sentinels). Empty string MUST be rejected. Validation MUST run before
any filesystem write, and the error MUST quote the offending input.

**R8**: When the positional `<name>` matches an existing global registry
entry whose `Root` differs from the new target path, init MUST succeed
and MUST emit a stderr warning indicating the registry entry's `Root`
has been rebound from the previous location. (This preserves the
documented `cd ~/other-dir && niwa init my-team` pattern.)

**R9**: On init success in any mode (named, scaffold, clone), the success
message MUST include the absolute path of the workspace root.

**R10**: `niwa go <name>` MUST land in `<cwd>/<name>/` after a successful
`niwa init <name>` invocation. (This follows automatically from R1 + the
existing registry-driven `niwa go` resolution; called out as a
requirement to make the cross-command consistency testable.)

### Non-Functional Requirements

**R11**: The `Long:` help text on `initCmd` MUST describe the new
behavior explicitly: that a positional `<name>` causes niwa to create
`<cwd>/<name>/`, and that the explicit name overrides the cloned
`[workspace] name`. The three modes (no name, name only, name + `--from`)
MUST remain documented.

**R12**: The project README MUST be updated so that all `niwa init`
examples reflect the new behavior. The quickstart MUST drop the
`mkdir + cd` step. The "shared workspace configs" section, the registry-
replay example, and the commands table row for `niwa init [name]` MUST
be updated.

**R13**: Functional test coverage MUST include at least one
`@critical`-tagged Gherkin scenario exercising the new `niwa init <name>`
flow end-to-end (creates the directory, registers the name, supports
`niwa go <name>`).

## Acceptance Criteria

### Directory creation

- [ ] AC-1: From an empty directory, `niwa init my-ws --from org/recipe`
  creates `./my-ws/.niwa/workspace.toml` and registers `my-ws` in the
  global registry with `Root = <cwd>/my-ws`.
- [ ] AC-2: From an empty directory, `niwa init my-ws` (no `--from`,
  name not in registry) creates `./my-ws/.niwa/workspace.toml` with a
  scaffolded config.
- [ ] AC-3: From an empty directory, `niwa init` (no args) creates
  `./.niwa/workspace.toml` in the current directory; behavior matches
  today.
- [ ] AC-4: From an empty directory, `niwa init --from org/recipe` (no
  positional name) clones the config into `./.niwa/`; behavior matches
  today.

### Name override

- [ ] AC-5: After `niwa init my-name --from org/upstream` where the
  upstream config declares `[workspace] name = "upstream"`, `niwa status`
  shows `my-name`, not `upstream`.
- [ ] AC-6: After AC-5, `niwa apply` references `my-name` in its output,
  not `upstream`.
- [ ] AC-7: After AC-5, the on-disk file `<cwd>/my-name/.niwa/workspace.toml`
  retains its upstream `[workspace] name = "upstream"` (the cloned file
  is not modified).
- [ ] AC-8: When AC-5's command runs, niwa emits a stderr note
  identifying that `my-name` overrides `upstream`.

### Conflict handling

- [ ] AC-9: When `<cwd>/my-ws` is a regular file, `niwa init my-ws`
  exits non-zero with an `InitConflictError`. The error message
  includes the absolute path and the qualifier "file". No directory is
  created at `<cwd>/my-ws`.
- [ ] AC-10: When `<cwd>/my-ws` is an unrelated directory (no `.niwa/`
  inside), `niwa init my-ws` exits non-zero with an `InitConflictError`.
  The error message includes the absolute path and the qualifier
  "directory".
- [ ] AC-11: When `<cwd>/my-ws` is a symlink (to anywhere),
  `niwa init my-ws` exits non-zero. The error qualifier is "symlink".
- [ ] AC-12: When `<cwd>/my-ws/.niwa/workspace.toml` exists,
  `niwa init my-ws` exits non-zero with the existing `ErrWorkspaceExists`
  message ("Use `niwa apply` to update the existing workspace").
- [ ] AC-13: When `<cwd>/my-ws/.niwa/` exists without `workspace.toml`,
  `niwa init my-ws` exits non-zero with the existing
  `ErrNiwaDirectoryExists` message ("Remove the `.niwa` directory and
  retry").

### Name validation

- [ ] AC-14: `niwa init "foo bar"` (any value containing spaces) exits
  non-zero before any filesystem write, with an error quoting the
  offending input and citing the allowed character set.
- [ ] AC-15: `niwa init "foo/bar"` exits non-zero before any filesystem
  write. (Same regex rejection.)
- [ ] AC-16: `niwa init ..` exits non-zero before any filesystem write,
  even though `..` matches the regex. The error mentions `..` and
  reserves explanation.
- [ ] AC-17: `niwa init .` exits non-zero before any filesystem write
  (same as AC-16).
- [ ] AC-18: `niwa init ""` exits non-zero before any filesystem write.

### Registry rebind

- [ ] AC-19: Given a global registry entry for `my-team` with
  `Root = /path/A`, running `niwa init my-team` from `/path/B` (where
  `/path/B/my-team` does not exist) succeeds, creates
  `/path/B/my-team/.niwa/`, and updates the registry's `Root` to
  `/path/B/my-team`.
- [ ] AC-20: AC-19 also emits a stderr warning indicating the rebind
  (mentions both the previous `Root` and the new `Root`).

### Go integration

- [ ] AC-21: After `niwa init my-ws --from org/recipe` from `/some/dir`,
  running `niwa go my-ws` from any directory lands the user at
  `/some/dir/my-ws`.

### Documentation

- [ ] AC-22: `niwa init --help` output explicitly states that a positional
  `<name>` causes niwa to create `<cwd>/<name>/`. The output also states
  that the positional name overrides the cloned `[workspace] name`.
- [ ] AC-23: README quickstart no longer contains a `mkdir + cd` step
  before `niwa init`.
- [ ] AC-24: README "shared workspace configs" section, registry-replay
  example, and commands table row for `niwa init [name]` reflect the new
  behavior.
- [ ] AC-25: At least one `@critical`-tagged Gherkin scenario exercises
  the new `niwa init <name>` flow end-to-end.

### Success messaging

- [ ] AC-26: On a successful init in any mode, the success message
  includes the absolute path of the workspace root.

## Out of Scope

- **Extending the same convention to `niwa create`.** `niwa create`
  shares the "must mkdir first" papercut but is a separate concern;
  filed as a follow-up issue.
- **An `--in-place` / `--here` escape hatch.** Pre-1.0 status; clean
  breaking change. Users who relied on the old `cd <name> && niwa init <name>`
  pattern see the success message's absolute path (R9) and adjust.
- **Tightening the name regex itself** (length cap, excluding leading
  dots, restricting to ASCII-strict). The PRD reuses the existing regex;
  rule changes are a separate concern.
- **Case-sensitivity behavior on case-insensitive filesystems.** This is
  a pre-existing condition (the registry is case-sensitive but macOS
  default APFS is not); the PRD does not introduce or fix it.
- **Migrating the cloned `.niwa/workspace.toml`'s `[workspace] name`
  field** to match the override. The cloned file stays clean against
  upstream; the override lives in instance state.
- **Re-syncing or refreshing the cloned `.niwa/` from the source repo
  after init.** Not changed by this PRD.
- **A formal CHANGELOG, RELEASES.md, or MIGRATION.md file.** Niwa
  doesn't have these; the PR description and the new error/help text are
  the migration aids. Not required by this PRD.
- **Heuristic warning for `filepath.Base(cwd) == name`.** Considered as
  a way to catch users typing the old `mkdir foo + cd foo + init foo`
  pattern. Rejected: scope creep, false-positive risk, and the
  absolute-path success message (R9 / AC-26) gives the user the same
  signal without a new heuristic.

## Decisions and Trade-offs

### Name override mechanism: instance state, not toml rewrite

When the explicit `<name>` overrides the cloned `[workspace] name`, the
override is persisted in `.niwa/state.json` (`InstanceState`) and
consulted by downstream readers. Two alternatives were rejected during
exploration:

- **Rewrite the cloned `.niwa/workspace.toml` in place.** Rejected because
  it dirties the workspace against its source-repo HEAD and complicates
  any future re-sync from upstream.
- **Override only the global registry entry (status quo extended).**
  Rejected because it leaves the user-visible inconsistency the PRD aims
  to fix: status and apply continue to read from the toml.

The chosen approach extends the existing `InstanceState` pattern for
init-time overrides (`SkipGlobal`, `NoOverlay`, `OverlayURL`,
`OverlayCommit`) to a name override, keeping the cloned config clean.

### Pre-flight conflict shape: caller-side existence check

When the target `<cwd>/<name>` already exists, the existence check fires
in the CLI caller (before niwa-state validation), not inside
`workspace.CheckInitConflicts`. This separates filesystem pre-gates from
niwa-state validation and avoids passing two inputs (`parent`, `name`)
into `CheckInitConflicts` for what amounts to a single derived path.
Considered alternative: adding `name` as a second parameter to
`CheckInitConflicts`. Rejected as splitting one concern into two
parameters and complicating the no-name path.

### Error sub-case routing

When the existing path at `<cwd>/<name>` is itself a niwa workspace or
an orphaned `.niwa/`, the preflight surfaces the existing
`ErrWorkspaceExists` / `ErrNiwaDirectoryExists` errors rather than a
generic "target exists." This preserves the existing remediation hints
("Use `niwa apply` to update" / "Remove `.niwa` and retry") which are
the right next step for those specific cases. The new
`ErrTargetDirExists` is reserved for the truly generic case (file,
unrelated directory, symlink, other).

### Name validation: reuse existing regex, apply upfront

`<name>` is validated against the existing `^[a-zA-Z0-9._-]+$` regex
that governs `[workspace] name` and other named identifiers in
`workspace.toml`, with explicit rejection of `.` / `..` literals and the
empty string. The regex itself is not changed; only the application
point is. Today the regex runs only post-flight (during config load),
which means `modeClone` accepts names that the regex would reject, and
errors point at the wrong layer. Pulling validation upfront produces a
clear error pointing at the user's input. Considered alternative: a
looser sanity check (no whitespace, no path separators). Rejected
because it would create a third name-validation regime alongside the
toml regex and the registry sanitizer; reusing the toml regex keeps the
project to one rule.

### Registry rebind: warn, don't error

When the explicit `<name>` matches an existing registry entry whose
`Root` points elsewhere, init succeeds and emits a stderr warning rather
than erroring. This preserves the README-documented
`cd ~/other-dir && niwa init my-team` pattern as a single command.
Considered alternatives: silent rebind (rejected, surprising) or
hard-error requiring an explicit `--rebind` flag (rejected, breaks
documented usage). The warning is the niwa-idiomatic choice for
non-fatal-but-surprising state changes.

### Backward compatibility: clean break, no flag

No `--in-place` or `--here` flag is added for users on the old flow.
Niwa is pre-1.0 and adding a flag for an old pattern entrenches it.
The combination of (a) clear error when the target exists, (b) absolute-
path success message that exposes unintended nesting, and (c) updated
README + help text is judged sufficient for migration. The user-
visible failure mode for the old pattern when names *don't* collide
(e.g., `mkdir my-workspace && cd my-workspace && niwa init my-project`)
is a successfully nested workspace at `my-workspace/my-project/`,
visible in the success message; the user can re-init from the parent
once they recognize the nesting. A muscle-memory heuristic
(`filepath.Base(cwd) == name`) was considered and rejected as scope
creep with false-positive risk.

### `niwa create` extension: deferred

`niwa create` shares the same "must pre-create the directory" papercut
but is a separate command with separate semantics (creates instances
inside an existing workspace, not the workspace itself). Bundling it
into this PRD risks scope creep. Filed as a follow-up issue.

## Known Limitations

- **Old-flow muscle memory produces nested workspaces silently when
  names don't collide.** A user running
  `mkdir my-workspace && cd my-workspace && niwa init my-project` finds
  their workspace at `my-workspace/my-project/.niwa/`. The success
  message's absolute path (R9) makes this visible, but the command does
  not error.
- **Case-insensitive filesystem behavior is unchanged.** A user on macOS
  (default APFS) running `niwa init Foo` then `niwa init foo` from the
  same directory will see the second invocation either succeed (if the
  registry doesn't already have `foo`) or hit the existing-directory
  error; the registry can register `Foo` and `foo` as separate entries
  but the disk has one directory. This pre-existing condition is not
  addressed.
- **The workspace name in the on-disk toml diverges from the effective
  name when an override is in play.** A reader inspecting
  `.niwa/workspace.toml` directly will see the upstream name, not the
  user-given override. This is the deliberate consequence of preserving
  the cloned file unchanged; downstream commands consult instance state
  for the effective name.

## Downstream Artifacts

To be filled when implementation begins. Expected:
- Design doc capturing the implementation shape (instance state schema
  changes, preflight check structure, error sentinel additions).
- Implementation PR.
- Follow-up issue for `niwa create` directory-creation convention.
