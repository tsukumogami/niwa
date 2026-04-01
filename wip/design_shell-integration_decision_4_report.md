<!-- decision:start id="tsuku-install-shell-integration" status="assumed" -->
### Decision: Shell integration activation when niwa is installed via tsuku

**Context**

The DESIGN-shell-integration.md chose env file delegation for shell integration
bootstrapping: install.sh creates `~/.niwa/env` which sets PATH and calls
`eval "$(niwa shell-init auto)"`. But when niwa is installed via tsuku, install.sh
never runs. The `~/.niwa/env` file isn't created, and no source line is added to
shell rc files. The binary works (tsuku puts it on PATH via `~/.tsuku/bin/`), but
the shell function wrapper and completions aren't loaded.

Research confirmed that tsuku has no post-install shell setup capability — no action
installs sourceable shell files, no auto-sourcing mechanism exists, and `set_env`'s
output files are never read by tsuku's shell integration. No niwa recipe exists yet.

The `niwa shell-init` subcommand is already install-method-agnostic: the binary emits
its own shell code regardless of where it's installed. The gap is narrow — who adds
the eval line to the user's shell config when install.sh doesn't run?

**Assumptions**

- Users who install tools via tsuku are comfortable adding eval lines to their shell
  config — they already have `eval $(tsuku shellenv)` in their rc file. If wrong:
  a more automated approach (tsuku shell.d/ mechanism) would be needed, but this is
  a tsuku platform decision, not a niwa concern.
- The shell function wrapper is an enhancement, not a requirement. Niwa works without
  it — users just don't get auto-cd after create or the go command. If wrong: the
  design would need to guarantee shell integration regardless of install method.

**Chosen: Document the eval line + runtime hint**

For non-install.sh installation methods (tsuku, `go install`, manual binary
placement), niwa's documentation and tsuku's post-install message tell users to add
`eval "$(niwa shell-init auto)"` to their shell config. This is the same requirement
that direnv, mise, and zoxide have.

As a quality-of-life enhancement, niwa prints a one-time hint when it detects the
shell function isn't active. The shell function sets `_NIWA_SHELL_INIT=1` as part of
its init output. When niwa runs a cd-eligible command (`create`, `go`) and this
variable is unset, it prints to stderr:

```
hint: shell integration not detected. For auto-cd and completions, add to your shell config:
  eval "$(niwa shell-init auto)"
```

This fires only on cd-eligible commands (not every invocation) and only when the
wrapper is missing, so it's targeted and not noisy.

The design doc's Decision 2 (env file delegation) should be reframed: it's the
install.sh-specific bootstrapping path, not the universal mechanism. The universal
mechanism is the shell-init subcommand itself, which works with any installation
method.

**Rationale**

This matches how the entire eval-init ecosystem works. No tool (direnv, mise, zoxide,
starship) auto-installs its eval line — they all require the user to add it. The env
file delegation in install.sh is a convenience for that specific install path, not
a design requirement.

The runtime hint closes the UX gap without cross-repo changes: users who forget the
eval line get a prompt at the moment they'd benefit from it (running `create` or
`go`). Users who don't need shell integration (running other niwa commands) never
see the hint.

Tsuku's shell.d/ mechanism (Alternative 3) is the right long-term answer if multiple
tools need shell integration, but it's a tsuku platform decision with no second
consumer today. Niwa shouldn't wait for it.

**Alternatives Considered**

- **Piggyback on tsuku's shellenv (shell.d/ mechanism)**: Extend tsuku's shellenv
  output to source files from installed tools. Rejected: requires tsuku changes with
  no second consumer, and was already considered and deferred during the exploration
  phase. This remains a valid future tsuku feature but isn't niwa's problem to solve.

- **Recipe runs install.sh via run_command**: Have the tsuku recipe execute install.sh
  or create ~/.niwa/env directly. Rejected: the env file is useless without the
  source line in rc files, which only install.sh adds. run_command can't modify
  user rc files (no variable for home directory, and doing so would be invasive).

- **Runtime hint only (no documentation)**: Rely entirely on the runtime hint.
  Rejected: documentation is the primary discovery mechanism. Users read READMEs and
  install guides before they encounter the hint. Both are needed.

**Consequences**

- The design doc needs a section covering installation method variations (install.sh
  vs. tsuku vs. manual)
- `niwa shell-init` output should set `_NIWA_SHELL_INIT=1` so the binary can detect
  whether the wrapper is active
- cd-eligible commands need a stderr hint when `_NIWA_SHELL_INIT` is unset
- Tsuku's niwa recipe (when created) should include a post-install message suggesting
  the eval line
- The env file delegation approach is preserved but correctly scoped to install.sh
<!-- decision:end -->
