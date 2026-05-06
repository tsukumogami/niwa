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

### Preflight Order

When `niwa init <name>` is invoked with a positional name, niwa MUST run
preflight checks in this fixed order before any filesystem writes:

1. **Name validation (R7)** — reject malformed names, `.`, `..`, empty.
2. **Target-exists pre-flight (R5)** — reject if `<cwd>/<name>` exists at
   any path type, EXCEPT when sub-case routing (R6) applies.
3. **Sub-case routing for niwa-aware paths (R6)** — when `<cwd>/<name>`
   contains `.niwa/` or `.niwa/workspace.toml`, surface the more specific
   error.
4. **Niwa-state validation** — existing `CheckInitConflicts` checks
   (orphan `.niwa/` in cwd, nested-instance walk-up) on the target dir;
   most cases will not fire because the target dir does not exist yet.
5. **Registry rebind detection (R8)** — if the name is already
   registered to a different `Root`, queue the rebind warning to emit
   after success.

When two checks could fire on the same input, the earlier check in this
list wins. Notably: R5 (target exists) takes precedence over R8
(registry rebind); when `<cwd>/<name>` exists and `<name>` is also
already registered, the user gets the target-exists error and no rebind
happens. R6 takes precedence over R5; when `<cwd>/<name>` is itself a
niwa workspace, the `ErrWorkspaceExists` error wins over the generic
`ErrTargetDirExists`.

### Functional Requirements

**R1**: When `niwa init <name>` is invoked with an explicit positional
`<name>`, niwa MUST create `<cwd>/<name>/` and initialize the workspace
inside it. This applies whether or not `--from` is also given.

**R2**: When `niwa init` is invoked without a positional `<name>` (zero
args, with or without `--from`), niwa MUST initialize the workspace in
the current working directory. Behavior is unchanged from today.

**R3**: When `niwa init <name> --from <src>` is invoked, the explicit
`<name>` MUST be the effective workspace name on every niwa-interpreted
surface: `niwa status` workspace identity field, `niwa apply` workspace-
name banner and summary line, the global registry entry's key, the init
success message, and any future read-back command that prints an
interpreted workspace name. The cloned `[workspace] name` field in
`.niwa/workspace.toml` MUST NOT be modified on disk; the override MUST
be persisted in `InstanceState` (`.niwa/state.json`) and consulted by
downstream readers. Commands that print the raw `.niwa/workspace.toml`
content verbatim (e.g., a debug `cat`-equivalent) are exempt and continue
to show the on-disk value.

**R4**: When the cloned `--from` config's `[workspace] name` differs from
the explicit positional `<name>`, niwa MUST emit a per-invocation stderr
note as part of init success indicating the override. Note format:
`"note: workspace name <name> overrides <config-name> from cloned
config."` "Per-invocation" means: emitted by the init invocation that
performs the override; NOT emitted by subsequent `niwa status`,
`niwa apply`, or any other command. The note is not persisted to any
state file and does not use the niwa one-time-notices machinery.

**R5**: When `niwa init <name>` is invoked and `<cwd>/<name>` already
exists at any path type (regular file, directory, symlink), niwa MUST
reject the operation with an `InitConflictError` wrapping a new sentinel
`ErrTargetDirExists`, BEFORE any filesystem writes. The error's `Detail`
MUST be `"<absolute-path> already exists (<qualifier>)"` where
`<qualifier>` is one of `file`, `directory`, or `symlink` (determined via
`os.Lstat`, so symlinks are not followed). The error's `Suggestion` MUST
be `"Pick a different name or remove the path and retry."` No file or
directory may be created at or below `<cwd>/<name>` when this error
fires. Path types other than file/directory/symlink (FIFO, socket,
device, etc.) are out of scope and have undefined-but-must-still-error
behavior.

**R6**: When `<cwd>/<name>` already exists AND contains a niwa workspace
(`<cwd>/<name>/.niwa/workspace.toml` exists), niwa MUST surface the
existing `ErrWorkspaceExists` error with its existing
`"Use niwa apply to update the existing workspace"` suggestion, INSTEAD
OF the generic `ErrTargetDirExists`. When
`<cwd>/<name>/.niwa/` exists at any path type AND
`<cwd>/<name>/.niwa/workspace.toml` does not exist (regardless of any
other contents inside `.niwa/`), niwa MUST surface the existing
`ErrNiwaDirectoryExists` error with its
`"Remove the <abs path>/.niwa directory and retry"` suggestion. This
sub-case routing takes precedence over R5; the existence of
`workspace.toml` is the sole discriminator between R6's two branches.

**R7**: The positional `<name>` MUST be validated against the existing
`^[a-zA-Z0-9._-]+$` regex (the same regex that governs `[workspace] name`
in `internal/config/config.go`). The literals `.` and `..` MUST be
rejected explicitly (they pass the regex but are path-traversal
sentinels). The empty string MUST be rejected. Validation MUST run
before any filesystem write. All name-validation errors MUST quote the
offending input verbatim and MUST include a human-readable description
of the allowed character set (e.g., "alphanumerics, dots, underscores,
hyphens"); the regex itself is not required in the message.

**R8**: When the positional `<name>` matches an existing global registry
entry whose `Root` differs from the new target path, init MUST succeed
and MUST emit a stderr warning naming both the previous `Root` and the
new `Root`. The previous directory at the old `Root` MUST be left intact
(this PRD does not delete or modify the old workspace). This preserves
the documented `cd ~/other-dir && niwa init my-team` pattern.

**R9**: On init success in every mode (no-args scaffold, named scaffold,
clone), niwa MUST print to stdout a success message that includes the
absolute path on disk where this invocation wrote
`.niwa/workspace.toml`. The path is the resolved absolute path
(symlinks in the cwd ancestry followed via `filepath.EvalSymlinks` or
equivalent). Format: `"Workspace <name> initialized at <abs-path>."` for
named modes, `"Workspace initialized at <abs-path>."` for the no-args
scaffold mode.

**R10**: `niwa go <name>` MUST land in the workspace directory created
by `niwa init <name>` (i.e., `<cwd>/<name>/`), regardless of which
directory `niwa go` is invoked from. This is regression coverage for
the registry-write side of init, not a new `niwa go` feature.

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
  upstream config declares `[workspace] name = "upstream"`, the
  `Workspace:` line of `niwa status` output reads `my-name`. No line of
  status output prints `upstream` as the workspace name.
- [ ] AC-6: After AC-5, the workspace-name banner and summary line of
  `niwa apply` stdout output read `my-name`. No line of apply output
  prints `upstream` as the workspace name.
- [ ] AC-7: After AC-5, the on-disk file
  `<cwd>/my-name/.niwa/workspace.toml` retains its upstream
  `[workspace] name = "upstream"` (the cloned file is not modified).
- [ ] AC-8: When AC-5's command runs, niwa emits a stderr note
  containing both literal tokens `my-name` and `upstream`, formatted per
  R4.
- [ ] AC-8b: When `niwa init my-name --from org/upstream` runs and the
  upstream config also declares `[workspace] name = "my-name"` (names
  match), niwa does NOT emit the override note. Stderr is silent on the
  override topic.
- [ ] AC-8c: After AC-5, subsequent invocations of `niwa status`,
  `niwa apply`, and `niwa go` from inside the workspace do NOT re-emit
  the override note on stderr.
- [ ] AC-8d: After AC-5, the global registry's entry key for the
  workspace is `my-name`. (`niwa list` or registry-file inspection
  shows `my-name`, not `upstream`.)

### Conflict handling

- [ ] AC-9: When `<cwd>/my-ws` is a regular file, `niwa init my-ws`
  exits non-zero, the error wraps the new `ErrTargetDirExists` sentinel,
  the message includes the absolute path and the qualifier `file`, and
  no file or directory is created at or below `<cwd>/my-ws`.
- [ ] AC-10: When `<cwd>/my-ws` is an unrelated directory (no `.niwa/`
  inside), `niwa init my-ws` exits non-zero, the error wraps
  `ErrTargetDirExists`, the message includes the absolute path and the
  qualifier `directory`, and `<cwd>/my-ws/.niwa/` does NOT exist after
  the failed init.
- [ ] AC-11: When `<cwd>/my-ws` is a symlink (regardless of whether the
  target exists, resolves to a file, or resolves to a directory),
  `niwa init my-ws` exits non-zero, the error wraps `ErrTargetDirExists`,
  the qualifier is `symlink`, and `<cwd>/my-ws/.niwa/` does NOT exist
  after the failed init.
- [ ] AC-12: When `<cwd>/my-ws/.niwa/workspace.toml` exists,
  `niwa init my-ws` exits non-zero with the existing `ErrWorkspaceExists`
  error (suggestion contains `"Use niwa apply"`). The error MUST NOT be
  `ErrTargetDirExists`.
- [ ] AC-13: When `<cwd>/my-ws/.niwa/` exists without `workspace.toml`,
  `niwa init my-ws` exits non-zero with the existing
  `ErrNiwaDirectoryExists` error (suggestion contains `"Remove"` and the
  `.niwa` path). The error MUST NOT be `ErrTargetDirExists`.

### Name validation

All name-validation ACs assert the same invariant: niwa exits non-zero,
no file or directory is created at `<cwd>/<the-input>`, and the error
message quotes the offending input verbatim and includes a human-
readable description of the allowed character set.

- [ ] AC-14: `niwa init "foo bar"` (whitespace) fails per the invariant.
- [ ] AC-15: `niwa init "foo/bar"` (path separator) fails per the
  invariant.
- [ ] AC-16: `niwa init ..` fails per the invariant. The error mentions
  the literal `..` and explains it is rejected as a path-traversal
  sentinel (not allowed even though it matches the regex).
- [ ] AC-17: `niwa init .` fails per the invariant. The error mentions
  the literal `.` and explains it is rejected as a path-traversal
  sentinel.
- [ ] AC-18: `niwa init ""` (empty string) fails per the invariant; the
  error explicitly states the name cannot be empty.

### Registry rebind

- [ ] AC-19: Given a global registry entry for `my-team` with
  `Root = /path/A`, running `niwa init my-team` from `/path/B` (where
  `/path/B/my-team` does not exist) succeeds, creates
  `/path/B/my-team/.niwa/`, and updates the registry's `Root` to
  `/path/B/my-team`. The previous directory at `/path/A` is left intact;
  no files at or below `/path/A` are removed or modified.
- [ ] AC-20: AC-19 also emits a stderr warning containing both the
  literal previous `Root` (`/path/A`) and the new `Root`
  (`/path/B/my-team`).
- [ ] AC-20b: Given the same registry entry for `my-team` with
  `Root = /path/A`, running `niwa init my-team` from `/path/B` when
  `/path/B/my-team` ALREADY exists (any path type) fails with the
  target-exists error per AC-9/AC-10/AC-11 (R5 takes precedence over
  R8). No rebind happens; the registry's `Root` remains `/path/A`.

### Go integration

- [ ] AC-21: After `niwa init my-ws --from org/recipe` from `/some/dir`,
  running `niwa go my-ws` from any directory lands the user at
  `/some/dir/my-ws`.

### Documentation

- [ ] AC-22: `niwa init --help` output contains text describing the
  directory-creation behavior (a literal phrase containing "creates" and
  `<name>`) AND text describing the name-override behavior (a literal
  phrase containing "overrides" or equivalent semantic, plus reference
  to the cloned config).
- [ ] AC-23: A grep of `README.md` for the literal sequence
  `mkdir` followed by any niwa-init command on the next line returns
  zero matches. (No `mkdir + cd + niwa init` example survives.)
- [ ] AC-24: The README sections enumerated in R12 (quickstart, "shared
  workspace configs," registry-replay example, commands table row for
  `niwa init [name]`) each show a `niwa init <name>` invocation without
  a preceding `mkdir`/`cd` step.
- [ ] AC-25: At least one `@critical`-tagged Gherkin scenario in
  `test/functional/features/` exercises the new `niwa init <name>`
  flow end-to-end: invokes `niwa init <name> --from <fixture>`,
  asserts `<name>/.niwa/workspace.toml` exists, asserts the registry
  has `<name>` with the new Root, and asserts `niwa go <name>` lands
  in the new directory.

### Success messaging

- [ ] AC-26: On a successful init in every mode (named scaffold, named
  clone, no-args scaffold, no-args clone), niwa prints a success message
  to stdout matching R9's format. The path printed is the resolved
  absolute path of the workspace root (symlinks in cwd ancestry are
  followed; on macOS, `/var/...` resolves to `/private/var/...`).

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
path success message (R9) that exposes unintended nesting, and (c)
updated README + help text is judged sufficient for migration. The user-
visible failure mode for the old pattern when names *don't* collide
(e.g., `mkdir my-workspace && cd my-workspace && niwa init my-project`)
is a successfully nested workspace at `my-workspace/my-project/`,
visible in the success message; the user can re-init from the parent
once they recognize the nesting.

A muscle-memory heuristic (warning when `filepath.Base(cwd) == name`)
was considered to catch the silent-nesting case and rejected. Phase 2
research argued the false-positive rate is near-zero because nobody
intentionally names a directory the same as the workspace they're about
to put inside it. That argument is conceded; the rejection rests on a
different point: an informational note about pre-existing user behavior
runs counter to the clean-break stance this PRD takes elsewhere. The
absolute-path success message gives users the same diagnostic signal
without baking old-flow patterns into the new error vocabulary.

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
