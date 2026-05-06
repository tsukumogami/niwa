# Issue 99 Implementation Plan

## Summary

Make `niwa shell-init install` work on default macOS by (a) including `~/.zshrc`
in the rc-file scan and (b) creating a sensible default rc file when none of the
candidates exist. Cover both with unit tests and a `@critical` Gherkin scenario.

## Approach

Two changes to `runShellInitInstall` and `shellRCFiles` in
`internal/cli/shell_init.go`:

1. **Add `~/.zshrc` to `shellRCFiles()`**. Plain macOS zsh users have only
   `.zshrc`. Sourcing `~/.niwa/env` from `.zshrc` ensures the env-file delegation
   runs in an interactive shell *after* `compinit` is initialized, which is what
   the cobra-generated zsh completion (`compdef`) needs. We keep `.zshenv` and
   `.bashrc` for backwards compatibility — multiple rc files all sourcing the
   same env file is harmless because the wrapper guards against re-evaluation.
2. **When no candidate rc file exists, create one.** Pick `.zshrc` if `$SHELL`
   ends in `zsh`, otherwise `.bashrc`. The acceptance criteria forbid silent
   success — creating an empty rc file and appending the source line to it is
   strictly better than the current "exits 0 with no effect" behavior. Print a
   stderr line that the file was created so the user can inspect it.

The `compdef` ordering risk noted in the issue (sourcing the env file from
`.zshenv` runs before `compinit`) is mitigated by ensuring we always also source
from `.zshrc` when zsh is involved. We do not modify the zsh-completion output
itself — that would diverge from cobra's generator and complicate maintenance.

### Alternatives Considered

- **Detect `$SHELL` and only target one rc file.** Cleaner end state, but breaks
  for users with a `.zshenv` already wired up (they'd lose that integration on
  re-install). Rejected: keep additive behavior.
- **Make the zsh completion output autoload `compinit` itself.** Self-bootstrap
  would let `.zshenv` sourcing work, but injects shell side-effects (running
  `compinit -u` from a tool's init script) that users may already be doing
  themselves with their own flags. Rejected: too invasive, and avoidable simply
  by sourcing from `.zshrc`.
- **Exit non-zero when no rc file exists.** Spec allows it but the bar is "must
  not silently no-op" — creating a usable rc file is friendlier and matches
  what `niwa shell-init install` already does for `~/.niwa/env`. Rejected.
- **Stat `.zprofile` / `.bash_profile` too.** Out of scope. Existing macOS
  Terminal sources `.zshrc` for interactive shells. Adding more candidates
  expands the test matrix without solving a reported case.

## Files to Modify

- `internal/cli/shell_init.go` — add `.zshrc` to `shellRCFiles`; add a
  fallback in `runShellInitInstall` that picks (and creates) a default rc file
  when none of the candidates exist; write a new helper for the fallback so the
  control flow stays readable.
- `internal/cli/shell_init_test.go` — three new tests:
  1. `TestShellInitInstall_AddsSourceLineToZshrc` — only `.zshrc` exists.
  2. `TestShellInitInstall_CreatesZshrcWhenNoRcFiles_ZshShell` — no rc files,
     `$SHELL=/bin/zsh`, `.zshrc` is created and contains source line.
  3. `TestShellInitInstall_CreatesBashrcWhenNoRcFiles_BashShell` — no rc files,
     `$SHELL=/bin/bash`, `.bashrc` is created.
- `test/functional/features/install-integration.feature` — add a `@critical`
  scenario for the macOS layout.
- `test/functional/steps_test.go` and `test/functional/suite_test.go` — new
  step(s) needed:
  - `^the file "([^"]*)" in HOME exists$`
  - `^the file "([^"]*)" in HOME contains "([^"]*)"$`
  - `^no niwa rc files exist in HOME$` (cleanup helper for explicitness)
  - `^I create an empty file "([^"]*)" in HOME$`

## Files to Create

None — extending existing files only.

## Implementation Steps

- [ ] Add `~/.zshrc` to `shellRCFiles()`.
- [ ] Refactor `runShellInitInstall` to invoke a new fallback helper when
      `len(rcFiles) == 0`. The helper returns the chosen path; main flow then
      treats it identically to a stat-detected file.
- [ ] Implement the fallback chooser: prefer `.zshrc` when `$SHELL` ends in
      `zsh`, else `.bashrc`. Create the file empty if missing. Emit a stderr
      "Created ..." line.
- [ ] Add three unit tests in `shell_init_test.go`. Each test sets a fresh
      `HOME`, optionally pre-creates `.zshrc`, sets `$SHELL`, runs install,
      asserts the expected file got the source line.
- [ ] Add Gherkin `@critical` scenario in `install-integration.feature` that
      runs `niwa shell-init install` with a macOS-style HOME and asserts the
      source line lands in `.zshrc`.
- [ ] Add the small set of step definitions needed to support the scenario.
- [ ] Run `go vet ./... && go test ./internal/cli/... -run "ShellInit|Completion"`
      and `make test-functional-critical`. Both must pass.

## Testing Strategy

- **Unit tests** cover the three rc-file layout permutations and the
  shell-detection fallback.
- **Functional `@critical`** asserts the user-observable contract: after
  `niwa shell-init install` on a default-macOS sandbox, `.zshrc` contains the
  source line.
- **Existing tests** stay green: `TestShellInitInstall_AddsSourceLine` still
  uses `.bashrc` and exercises the unchanged path; the
  `install-integration.feature` chain scenario uses an explicit env-file write
  and does not depend on rc-file detection.
- **Manual verification** (documented in the PR body): on a real macOS shell,
  open a fresh `zsh -i` after install and run `niwa <TAB>` — expect subcommand
  completion.

## Risks and Mitigations

- **Risk: creating a new `.zshrc` surprises the user.**
  Mitigation: only create when none of `.bashrc`, `.zshenv`, `.zshrc` exists.
  If the user has any of those, we append rather than create. Print explicit
  stderr line on creation.
- **Risk: appending to multiple rc files double-sources `~/.niwa/env`.**
  Mitigation: the env file's `command -v niwa` guard and the wrapper's
  `_NIWA_SHELL_INIT=1` guard make double-sourcing safe (PATH dedup is
  separate; the duplicate entry is benign because PATH lookups are
  short-circuit). No behavior change for users with only one rc file.
- **Risk: `$SHELL` is unset or non-standard in CI.**
  Mitigation: fallback to `.bashrc` matches the existing assumption.
  The zsh-shell test sets `$SHELL` explicitly.
- **Risk: missing trailing newline in pre-existing rc file produces a malformed
  source line.** The current `addSourceLine` writes `\n%s\n`, leading newline
  prevents glomming onto the prior content. No change needed.

## Success Criteria

- [ ] `niwa shell-init install` on `HOME=$tmp` with only `~/.zshrc` present
      writes the source line to `.zshrc`.
- [ ] `niwa shell-init install` on empty `HOME=$tmp` with `SHELL=/bin/zsh`
      creates `.zshrc` and writes the source line.
- [ ] `niwa shell-init install` on empty `HOME=$tmp` with `SHELL=/bin/bash`
      creates `.bashrc` and writes the source line.
- [ ] Existing `internal/cli` shell-init unit tests stay green (no regression).
- [ ] `make test-functional-critical` passes including the new scenario.
- [ ] `go vet ./...` clean.

## Open Questions

None — the acceptance criteria allow the create-rather-than-fail choice; the
chosen rc-file precedence matches platform defaults.
