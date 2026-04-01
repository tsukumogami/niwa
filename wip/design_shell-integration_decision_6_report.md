<!-- decision:start id="shell-integration-optionality" status="confirmed" -->
### Decision: Shell integration must be optional with explicit install/uninstall

**Context**

The design treats shell integration as an enhancement — niwa works without it. But
this principle wasn't reflected in the install flow or command surface. install.sh
always writes the env file with shell-init delegation, and there's no way for users
to add or remove shell integration after initial install. Users who don't want shell
functions shadowing their binary (CI environments, minimal setups, debugging) need
an explicit opt-out. And users who installed via tsuku need an explicit opt-in.

The user identified this as a key requirement: install.sh should accept a flag to
skip shell integration, and niwa should have symmetrical commands to install and
uninstall it.

**Assumptions**

- The existing `--no-modify-path` flag on install.sh establishes the pattern for
  opt-out flags. If wrong: a different mechanism would be needed, but flag-based
  opt-out is standard for installers.

**Chosen: --no-shell-init flag + niwa shell-init install/uninstall subcommands**

Three changes:

1. **install.sh gains a `--no-shell-init` flag.** When passed, the env file contains
   only the PATH export — no delegation to `niwa shell-init auto`. This parallels
   the existing `--no-modify-path` flag.

2. **`niwa shell-init install`** adds shell integration to the user's environment.
   It writes the delegation block to `~/.niwa/env` (or creates the file if it
   doesn't exist when installed via tsuku). For tsuku users without an env file,
   it creates `~/.niwa/env` with the delegation block and adds the source line
   to shell rc files (same logic install.sh uses). This is the explicit opt-in
   for non-install.sh users.

3. **`niwa shell-init uninstall`** removes shell integration. It rewrites
   `~/.niwa/env` to contain only the PATH export (removing the delegation block).
   The source line in rc files stays — it still sets PATH, which is still needed.

The subcommand structure becomes:
- `niwa shell-init bash` — print bash shell code to stdout (existing)
- `niwa shell-init zsh` — print zsh shell code to stdout (existing)
- `niwa shell-init auto` — detect shell and print code (existing)
- `niwa shell-init install` — write shell integration to env file and rc files
- `niwa shell-init uninstall` — remove shell integration from env file
- `niwa shell-init status` — report whether shell integration is active

`status` checks two things: whether `_NIWA_SHELL_INIT` is set in the current
shell (wrapper is loaded), and whether `~/.niwa/env` contains the delegation
block (will load on next shell). Reports both.

**Rationale**

Symmetrical install/uninstall commands give users full control over shell
integration lifecycle. The flag on install.sh prevents it from being set up in
the first place (useful for CI, containers, automated deployments). The
subcommands handle post-install changes regardless of how niwa was installed.

This also resolves the tsuku installation gap more cleanly: instead of just
documenting that users should add an eval line, `niwa shell-init install`
does it for them. The runtime hint (Decision 4) can suggest running
`niwa shell-init install` instead of telling users to manually edit rc files.

**Alternatives Considered**

- **Flag only, no subcommands**: install.sh gets `--no-shell-init` but there's
  no way to change it after install. Rejected: users need to be able to
  enable/disable shell integration without re-running the installer.

- **Subcommands only, no flag**: install.sh always installs shell integration;
  users run `niwa shell-init uninstall` to remove it. Rejected: CI and
  containerized environments shouldn't need a post-install cleanup step.

**Consequences**

- install.sh gains `--no-shell-init` flag (mirrors `--no-modify-path`)
- `niwa shell-init` gains install/uninstall/status subcommands
- The env file becomes a managed artifact with two states: PATH-only or
  PATH + shell-init delegation
- The runtime hint (Decision 4) changes from "add this to your shell config"
  to "run `niwa shell-init install`"
- `niwa shell-init install` absorbs the rc-file modification logic currently
  in install.sh, creating a single code path for shell config management
- Implementation Approach Phase 1 grows slightly (install/uninstall/status
  subcommands) but the logic is straightforward
<!-- decision:end -->
