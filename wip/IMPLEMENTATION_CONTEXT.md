---
issue: 99
title: "fix(shell-init): install silently no-ops on default macOS zsh setup"
tier: simple
design_doc: docs/designs/current/DESIGN-shell-integration.md
related: ["#42"]
summary: |
  Two bugs prevent niwa shell completions from working on default macOS zsh.
  (1) shellRCFiles() at internal/cli/shell_init.go:137-149 only stats .bashrc
  and .zshenv. macOS users have only .zshrc, so `niwa shell-init install`
  silently no-ops the rc-file step but still prints "Shell integration installed".
  (2) GenZshCompletion emits compdef calls; compdef is undefined unless compinit
  has run. Sourcing from .zshenv would run before compinit. .zshrc is the right
  target because zsh sources it interactively after compinit is typically run.

constraints:
  - Public repo: keep messaging professional, no internal references.
  - No emojis in code or docs.
  - No AI attribution in commits/PRs.
  - Design Decision 4 (DESIGN-shell-integration.md): "non-install.sh installs"
    are expected to run `niwa shell-init install`. The user did exactly this,
    so install must work for the default macOS layout.
  - shell wrapper template uses temp-file protocol (NIWA_RESPONSE_FILE) — do
    not regress.
  - Tests must remain green (`go test ./...`); functional `@critical` scenario
    desired per acceptance criteria.

key_files:
  - internal/cli/shell_init.go             # shellRCFiles, runShellInitInstall
  - internal/cli/shell_init_test.go        # existing unit coverage
  - test/functional/features/              # Gherkin scenarios
  - docs/guides/functional-testing.md      # functional-test patterns

approach_notes: |
  Minimum fix surface for the user-visible bug:
  - Add ~/.zshrc to shellRCFiles() lookup so the install command actually
    appends the source line on default macOS layouts. Order matters: .zshrc is
    sourced interactively, after compinit, so compdef is defined when niwa's
    zsh completion script runs. .zshenv would source too early.
  - When no rc file is detected, do something useful instead of silent success:
    create ~/.zshrc on macOS / ~/.bashrc on linux fallback and append, OR exit
    non-zero with a message. Creating is friendlier; document the choice in
    Phase 3.
  - Detect macOS (runtime.GOOS == "darwin") only if needed for fallback choice.

risks:
  - Modifying user rc files is sensitive. Keep the addSourceLine append
    semantics; do not modify existing lines.
  - Creating a new ~/.zshrc when none exists could be surprising. Mitigation:
    if the file doesn't exist, create it empty before appending so the user can
    inspect what was added. Print stderr message that we created it.
  - compdef-before-compinit is real but only manifests when sourced from
    .zshenv. Sourcing from .zshrc avoids it. Document but don't try to make the
    completion script robust to wrong sourcing locations.
---

## Goal

`niwa shell-init install` on default macOS (plain zsh, `~/.zshrc` only) should
produce a working installation: a fresh terminal completes `niwa <TAB>` with niwa
subcommands.

## Context

Reproduced on macOS with default zsh — `~/.zshrc` exists, no `~/.bashrc`, no
`~/.zshenv`, no Oh My Zsh or other framework.

Steps:
1. Install niwa (in this case via `tsuku install niwa`, but the install method
   doesn't matter — same result with any install path that doesn't run
   `install.sh`).
2. Run `niwa shell-init install`. Command exits 0 and prints
   `Shell integration installed. Open a new terminal to activate.`
3. Open a fresh Terminal window.
4. Type `niwa <TAB>`. Expected: subcommand completion. Actual: falls back to
   filename completion. No errors.

Root cause: `shellRCFiles()` in
`internal/cli/shell_init.go:137-149` only checks `~/.bashrc` and `~/.zshenv`. On
default macOS zsh neither file exists, so the function returns an empty slice and
the loop in `runShellInitInstall` (line 191) is a no-op. `~/.niwa/env` gets
written but no rc file ever sources it, so `niwa shell-init auto` is never
evaluated and no completion function is registered. The success message at
line 204 fires regardless.

The same flow works on Ubuntu because `~/.bashrc` is present by default.

Likely-related secondary defect (worth checking during the fix): the zsh
completion output emitted by `niwa shell-init zsh` uses cobra's
`GenZshCompletion`, which calls `compdef`. `compdef` is only defined after
`compinit` has run. If we add `~/.zshrc` to `shellRCFiles()` we should also make
sure the sourced env file ends up after `compinit` (or the completion script is
robust to running before it). Sourcing from `.zshenv` would be too early.

Design context: `docs/designs/current/DESIGN-shell-integration.md` Decision 4.
Related: #42.

## Acceptance Criteria

- [ ] `niwa shell-init install` on a default macOS zsh layout
      (only `~/.zshrc` present) results in `niwa <TAB>` completing subcommands
      in a fresh terminal
- [ ] When no rc file can be updated, `niwa shell-init install` either updates
      one anyway (creating it if needed) or exits non-zero with a clear message
      — it must not print "installed" without doing anything
- [ ] Existing Ubuntu/bash and other working flows continue to work (no regression)
- [ ] `@critical` Gherkin scenario added under `test/functional/features/` covering
      the macOS/zsh `shell-init install` path

## Validation

```bash
#!/usr/bin/env bash
set -euo pipefail

home=$(mktemp -d)
: > "$home/.zshrc"

# Run install with only .zshrc present.
HOME="$home" niwa shell-init install >/dev/null

# Some rc file must now source the env file.
grep -lr '\.niwa/env' "$home" >/dev/null \
  || { echo "FAIL: no rc file references ~/.niwa/env"; exit 1; }
echo "OK: rc file references env file"

# Smoke-check that the zsh completion output registers _niwa given a sane compinit.
zsh -d -f -c '
  autoload -Uz compinit && compinit -u
  eval "$(niwa shell-init zsh)"
  type _niwa >/dev/null 2>&1
' && echo "OK: _niwa registered" || { echo "FAIL: _niwa not registered"; exit 1; }
```

## Dependencies

None.
