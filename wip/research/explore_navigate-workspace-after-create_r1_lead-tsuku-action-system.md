# Lead: Does tsuku's action system support post-install shell setup?

## Findings

### Current Action System Inventory

Tsuku's action system is defined in `/public/tsuku/internal/actions/action.go`. Actions implement the `Action` interface with `Name()`, `Execute()`, `IsDeterministic()`, and `Dependencies()` methods. They're registered in a global registry via `init()`.

The full set of registered actions (from `action.go` lines 140-206):

**Core actions:** `download`, `download_file`, `extract`, `chmod`, `install_binaries`, `set_env`, `run_command`, `set_rpath`, `install_libraries`, `link_dependencies`, `require_system`

**System package actions:** `apt_install`, `apt_repo`, `apt_ppa`, `brew_install`, `brew_cask`, `dnf_install`, `dnf_repo`, `pacman_install`, `apk_install`, `zypper_install`

**Package manager composites:** `npm_install`, `npm_exec`, `pipx_install`, `pip_install`, `cargo_install`, `cargo_build`, `gem_install`, `cpan_install`, `go_install`, `nix_install`

**Build ecosystem:** `go_build`, `nix_realize`, `configure_make`, `cmake_build`, `meson_build`, `pip_exec`, `setup_build_env`

**Other:** `homebrew`, `homebrew_relocate`, `download_archive`, `github_archive`, `github_file`, `fossil_archive`, `app_bundle`, `group_add`, `service_enable`, `service_start`, `require_command`, `manual`

### Actions Closest to Shell Setup

**`set_env` action** (`internal/actions/set_env.go`): Creates an `env.sh` file in the tool's install directory containing `export VAR=VALUE` lines. This is the closest existing action to shell file installation. However, it only writes environment variables to a file within the tool's install directory -- it does NOT install anything into the user's shell configuration (bashrc/zshrc) or into a shared sourcing location.

**`run_command` action** (`internal/actions/run_command.go`): Executes arbitrary shell commands during installation. Could theoretically write shell functions to files, but operates within the tool's work directory context and is designed for build-time tasks. It has variable expansion for `{install_dir}`, `{work_dir}`, `{version}`, and `{libs_dir}`. No variable for the user's shell config directory or tsuku's shell integration directory.

**`install_binaries` action** (`internal/actions/install_binaries.go`): Copies files from work directory to install directory. Supports `bin/` destinations that become executable. Could install a shell script to `bin/`, but not a shell function file that needs to be sourced.

### How `env.sh` Is (Not) Sourced

The `set_env` action writes `env.sh` to the tool's install dir (`$TSUKU_HOME/tools/{name}-{version}/env.sh`), but there is no mechanism in tsuku's shell integration that automatically sources these files. The `shellenv` command (`cmd/tsuku/shellenv.go`) only prints PATH exports. The `shell` command (`cmd/tsuku/shell.go`) and `hook-env` command (`cmd/tsuku/hook_env.go`) use `shellenv.ComputeActivation()` which only modifies PATH based on `.tsuku.toml` tools -- it does not source env.sh files.

This means `set_env` is effectively inert for shell integration. It creates the file but nothing reads it in the shell context.

### Tsuku's Own Shell Integration Pattern

Tsuku uses `eval $(tsuku shellenv)` for PATH setup and `eval $(tsuku hook-env <shell>)` for prompt-based activation. These are `eval`-based patterns where the binary prints shell code and the user's shell evaluates it. This is the standard pattern for compiled binaries that need shell integration (used by mise, direnv, starship, etc.).

The existing design doc `DESIGN-shell-integration-building-blocks.md` outlines a six-block architecture for shell integration. Block 2 (command-not-found handler) specifically describes shell functions installed for bash/zsh/fish. But this design is about tsuku's own shell hooks, not a general mechanism for installed tools to register shell functions.

### No Niwa Recipe Exists

There is no niwa recipe in `/public/tsuku/recipes/`. Niwa has not yet been packaged for tsuku.

### What's Missing for Shell Function Installation

To support a tool like niwa installing a shell wrapper function (e.g., `niwa()` that calls the binary and then `cd`s), tsuku would need:

1. **A "shell files" destination**: A location like `$TSUKU_HOME/shell.d/` or `$TSUKU_HOME/completions/` where tools can install sourceable shell scripts, organized by shell type.

2. **A sourcing mechanism**: Shell integration that sources files from that directory. Something like: `for f in $TSUKU_HOME/shell.d/bash/*.sh; do source "$f"; done` added to `eval $(tsuku shellenv)`.

3. **A new action or extended `install_binaries`**: Either a new `install_shell_files` action that places files in the shell.d directory, or an extension to `install_binaries` that supports non-bin destinations with a "source" semantic.

4. **Shell-specific file variants**: The shell function for bash, zsh, and fish would differ. The action would need to handle per-shell variants.

### Alternative: Tool-Side `eval` Pattern

Instead of tsuku providing a general mechanism, niwa could follow the same pattern tsuku itself uses: `eval $(niwa shell-init bash)`. The niwa binary would have a subcommand that prints the shell function definition. Users would add this to their shell config independently of tsuku.

This is simpler and doesn't require tsuku changes, but it means each tool that needs shell integration has to document and implement its own `eval` setup. Tsuku's install flow can't automate it.

### Existing Precedent: Completion Scripts

Tsuku has a `completion` command (`cmd/tsuku/completion.go`) that generates bash/zsh/fish completion scripts. But this is tsuku's own completions -- there's no mechanism for installed tools to register their completions through tsuku. Each tool handles completions independently.

## Implications

1. **Tsuku cannot currently install shell functions for tools it manages.** The action system has no concept of "files that need to be sourced in the user's shell." The closest action (`set_env`) creates an env.sh file that nothing reads.

2. **The gap is two-part: destination + sourcing.** Even if a new action could place files in the right location, tsuku's shell integration would need to source them. Both pieces are missing.

3. **The "eval" pattern is the path of least resistance for niwa.** Niwa can implement `niwa shell-init <shell>` that prints a shell function definition. Users add `eval $(niwa shell-init bash)` to their shell config. This requires zero tsuku changes and follows established convention (direnv, mise, starship, etc.).

4. **A general tsuku mechanism would serve completions too.** If tsuku added a `$TSUKU_HOME/shell.d/` directory with automatic sourcing, it could handle both shell wrapper functions and completion scripts for all managed tools. This is a bigger project but has broader value.

5. **The DESIGN-shell-integration-building-blocks.md design is relevant but orthogonal.** That design focuses on tsuku's own shell hooks (command-not-found, environment activation). A "shell files for installed tools" mechanism is a different concern that could complement those building blocks.

## Surprises

1. **`set_env` creates files nothing sources.** The `env.sh` files written by `set_env` appear to be dead output -- no part of tsuku's shell integration reads or sources them. This is either a bug, an incomplete feature, or intended for manual sourcing by users. Either way, it demonstrates that tsuku has a "write shell files" capability but no "source shell files" capability.

2. **The `run_command` action has no access to user shell config paths.** Its variable expansion includes `{install_dir}`, `{work_dir}`, `{version}`, and `{libs_dir}`, but nothing pointing to the user's home directory, shell config, or tsuku's shell integration directory. A recipe cannot use `run_command` to modify shell config without hardcoding paths.

3. **Tsuku's completion scripts are generated but not auto-installed.** The `completion` command prints scripts to stdout for users to manually install. This confirms a consistent pattern: tsuku avoids modifying user shell configuration.

## Open Questions

1. **Is the `eval $(niwa shell-init)` pattern sufficient, or does niwa need tsuku-managed shell integration?** If niwa is always installed via tsuku, tsuku could handle the shell setup. If niwa might be installed independently (e.g., via go install), the `eval` pattern is more portable.

2. **What is the intended behavior of `set_env`'s env.sh files?** Are they meant to be manually sourced? Is there a planned mechanism to auto-source them? Understanding this clarifies whether env.sh is a dormant foundation for shell file support or truly dead code.

3. **Would a `$TSUKU_HOME/shell.d/` mechanism benefit enough tools to justify the complexity?** If niwa is the only tool needing a shell wrapper, the `eval` pattern is better. If completions for many tools would also use it, the general mechanism has broader value.

4. **How should version switching interact with shell functions?** When `tsuku activate niwa 2.0`, the shell function might need updating if it references the binary path. The `eval` pattern handles this naturally (niwa binary is on PATH, function calls it by name), while a tsuku-managed file would need updating on version switch.

## Summary

Tsuku's action system has no support for post-install shell setup -- there is no action that installs sourceable shell files and no mechanism to auto-source them in the user's shell. The most practical approach for niwa's `cd`-after-create requirement is to implement `niwa shell-init <shell>` in niwa itself, following the same `eval $(...)` pattern that tsuku, direnv, and mise use, which requires zero tsuku changes. If a general tsuku mechanism for shell file installation becomes desirable (with completions as a second use case), it would require both a new action type and an extension to tsuku's shell integration to source files from a `$TSUKU_HOME/shell.d/` directory.
