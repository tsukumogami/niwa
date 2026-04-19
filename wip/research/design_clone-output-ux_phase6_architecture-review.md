---
type: architecture-review
design: DESIGN-clone-output-ux.md
reviewer: architect-reviewer
date: 2026-04-19
---

# Architecture Review: Clone/Apply Output UX

## Is the architecture clear enough to implement?

Mostly yes. The Reporter interface, the `runGitWithReporter` helper skeleton, and the
phase decomposition are specific enough to implement without ambiguity. Two gaps exist:

**Gap 1 — DisplayMode propagation path is underspecified.**
The design says "TTY detection runs once in `PersistentPreRunE` and is passed into the
Reporter constructor passed to the Applier," but `PersistentPreRunE` in `root.go` has no
return value beyond `error`. The design doesn't say how `DisplayMode` travels from the
hook into `runApply` / `runCreate`. The existing pattern in this codebase is package-level
vars (e.g., `applyNoPull`, `applyAllowDirty`). The design should either name that pattern
explicitly or show the signature change. Without this, implementors will invent their own
path, potentially duplicating the var or reading `--no-progress` twice.

**Gap 2 — `SyncConfigDir` is a package-level function, not an `Applier` method.**
The component table lists `configsync.go` as accepting `*Reporter`. But `SyncConfigDir`
is called directly from `cli/apply.go` — not through `Applier`. Making it accept
`*Reporter` either requires passing the reporter from `cli/apply.go` (before `Applier`
exists) or moving the call inside `Applier`. The design is silent on which. This is a
real sequencing issue: Phase 4 says "Update `configsync.go` to accept `*Reporter`" but
the caller is in `cli/`, not in the apply pipeline.

## Are there missing components or interfaces?

One missing interface: `setup.go`'s `RunSetupScripts` runs arbitrary user scripts with
`cmd.Stdout = os.Stderr; cmd.Stderr = os.Stderr` — the same pattern being replaced for
git subprocesses. The design lists `setup.go` in Phase 4 but the function runs non-git
commands. `isGitErrorLine` will misclassify arbitrary script output. The design needs to
specify whether setup scripts are routed through `runGitWithReporter` (incorrect name,
wrong classifier) or get a separate `runCmdWithReporter` helper. This is a structural gap,
not a polish issue — the helper's classifier is git-specific.

## Are the implementation phases correctly sequenced?

Phases 1–4 are correctly ordered: Reporter first, TTY wiring second, status call sites
third, subprocess capture last. Phase 3 (status call sites) before Phase 4 (subprocess
capture) is safe because Phase 3 only adds `reporter.Status(...)` calls that already
clear on the next `reporter.Log(...)`.

One sequencing note: Phase 2 wires `DisplayMode` into `NewReporter`, but Phase 1 builds
`NewReporter` without TTY parameters. That's fine — `NewReporterWithTTY` is designed for
exactly this. No sequencing defect.

## Are there simpler alternatives overlooked?

The `SyncConfigDir` integration issue (Gap 2) has a simpler resolution the design missed:
move the `SyncConfigDir` call inside `Applier.Apply`, where the reporter already exists.
Currently the CLI calls it before constructing the applier. Pulling it into the applier
eliminates the need to thread a reporter into a package-level function and keeps the
"reporter as applier-internal concern" invariant clean.

## Summary

Blocking gaps:
1. `DisplayMode` propagation from `PersistentPreRunE` to `runApply`/`runCreate` is
   unspecified — implementors will diverge.
2. `SyncConfigDir` is a CLI-level call, not applier-internal — Phase 4 as written
   can't wire it without a call-site decision.
3. `setup.go` runs non-git subprocesses — `isGitErrorLine` is the wrong classifier;
   a separate helper or a renamed general-purpose helper is needed.

All three are solvable without changing the overall design direction. The Reporter struct,
`runGitWithReporter` pattern, and phase ordering are sound.
