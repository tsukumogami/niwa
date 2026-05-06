# Issue 99 Summary

## What Was Implemented

`niwa shell-init install` now wires `~/.niwa/env` into a shell rc file on
default macOS zsh setups instead of silently no-op'ing. The fix targets the
two failure modes identified in the issue: `~/.zshrc` was never considered as
a candidate, and an empty rc-file list was treated as success.

## Changes Made

- `internal/cli/shell_init.go`: collapsed the rc-file scan into a single
  iteration that now also stats `~/.zshrc`. Added `chooseDefaultRCFile` and
  wired it into `runShellInitInstall` so that an empty candidate list
  triggers creating a default rc file (`.zshrc` for `$SHELL=zsh`, `.bashrc`
  otherwise) instead of silently succeeding.
- `internal/cli/shell_init_test.go`: three new tests
  (`AddsSourceLineToZshrc`, `CreatesZshrcWhenNoRcFiles_ZshShell`,
  `CreatesBashrcWhenNoRcFiles_BashShell`).
- `test/functional/features/install-integration.feature`: `@critical`
  scenario "shell-init install on default macOS zsh layout updates .zshrc".
- `test/functional/steps_test.go` + `suite_test.go`: new step
  `the file "<name>" in HOME contains "<text>"` for asserting rc-file
  contents inside the sandbox HOME.

## Key Decisions

- **Add `.zshrc`, keep `.zshenv`**: backwards compatible. Multiple rc files
  sourcing `~/.niwa/env` is harmless because `command -v niwa` and
  `_NIWA_SHELL_INIT=1` guards make repeat evaluation a no-op for the wrapper
  side. Users who set up `.zshenv` previously continue to work; macOS users
  who only have `.zshrc` now also work.
- **Create rather than fail when no rc file exists**: the spec accepted
  either, but creating an empty `.zshrc`/`.bashrc` and appending the source
  line keeps the install command's user-facing contract intact ("open a new
  terminal to activate"). An explicit "Created ..." stderr line surfaces the
  side effect.
- **Use `$SHELL` for the create-fallback choice**: matches what the user
  already configured. No platform sniffing required.

## Trade-offs Accepted

- We do not make the zsh completion script self-bootstrap `compinit`. The
  cobra-generated output uses `compdef`, which assumes `compinit` has already
  run. By preferring `.zshrc` (interactive, post-`compinit`) over `.zshenv`,
  the ordering issue is sidestepped without diverging from cobra. If someone
  later wants `.zshenv` to work, they'd need a separate change (and a strong
  reason).

## Test Coverage

- New unit tests: 3 (all pass).
- New functional `@critical` scenario: 1 (passes).
- Coverage tracking is not enabled in this repo.

## Known Limitations

- The fallback only chooses between `.zshrc` and `.bashrc`. Users on fish,
  csh, or other shells still see no rc-file mutation. Treating those would
  expand scope beyond the bug — out of scope.
- A user who has `.zshenv` set up *and* runs an interactive zsh that doesn't
  source `.zshrc` (uncommon) would still hit the compdef-before-compinit
  ordering issue. Documented in the design doc; not addressed here.

## Requirements Mapping

| AC | Status | Evidence |
|----|--------|----------|
| Default macOS zsh + tsuku install: `niwa <TAB>` completes after `niwa shell-init install` | Implemented | `shellRCFiles` now stats `.zshrc`; `TestShellInitInstall_AddsSourceLineToZshrc` + `@critical` Gherkin scenario |
| When no rc file can be updated, install must not silently no-op | Implemented | `runShellInitInstall` empty-rc-list branch creates a file via `chooseDefaultRCFile`; tests `CreatesZshrcWhenNoRcFiles_ZshShell`, `CreatesBashrcWhenNoRcFiles_BashShell` |
| Existing Ubuntu/bash flow continues to work | Implemented | `TestShellInitInstall_AddsSourceLine` (unchanged, still passes) |
| `@critical` Gherkin scenario for macOS/zsh | Implemented | `install-integration.feature` "shell-init install on default macOS zsh layout updates .zshrc" |
