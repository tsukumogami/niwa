# Lead: What shell integration does niwa already have?

## Findings

### Current Shell Integration: PATH-only via env file

Niwa's only shell integration today is PATH configuration. The installer (`install.sh`) creates `~/.niwa/env` containing a single PATH export:

```sh
# niwa shell configuration
export PATH="${INSTALL_DIR}:$PATH"
```

This env file is sourced from the user's shell rc file. The installer appends a `. "$ENV_FILE"` line to `~/.bashrc` / `~/.bash_profile` / `~/.profile` (bash) or `~/.zshenv` (zsh). The `add_to_config()` helper is idempotent -- it checks for the marker before appending. A `--no-modify-path` flag skips shell config modification entirely.

**Key files:**
- `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-4/public/niwa/install.sh` (lines 104-164)

### What `niwa create` Currently Outputs

The create command prints the instance path to stdout on success:

```go
fmt.Fprintf(cmd.OutOrStdout(), "Created instance: %s\n", instancePath)
```

This is a human-readable message, not machine-parseable. There's no `--quiet` flag or structured output mode. The path is embedded in the string "Created instance: /path/to/instance" rather than printed bare.

**Key file:** `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-4/public/niwa/internal/cli/create.go` (line 104)

### No Shell Completion Support

Niwa has zero shell completion support. There's no `completion` subcommand, no `GenBashCompletion` calls, no completion scripts. The cobra root command is bare.

**Key file:** `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-4/public/niwa/internal/cli/root.go`

### No Shell Function Wrappers or Hooks

Beyond the PATH env file, niwa installs no shell functions, no prompt hooks, no command-not-found handlers, and no eval-based integrations. The entire shell surface area is a single `export PATH=...` line.

### Tsuku's Shell Integration (for comparison)

Tsuku has a mature shell integration system that niwa lacks entirely:

1. **Shell completions** (`tsuku completion [bash|zsh|fish]`): Uses cobra's built-in generation. File: `cmd/tsuku/completion.go`.

2. **Command-not-found hooks** (`tsuku hook install`): Writes hook scripts to `$TSUKU_HOME/share/hooks/` and appends source blocks with markers to shell rc files. Supports bash, zsh, fish. File: `cmd/tsuku/cmd_hook.go`, `internal/hook/install.go`.

3. **Activation hooks** (`tsuku hook install --activate`): Prompt-based hooks that run `tsuku hook-env` on directory change to manage per-project PATH. File: `cmd/tsuku/hook_env.go`.

4. **Shell shims** (`tsuku shim install`): Scripts in `$TSUKU_HOME/bin/` that delegate to `tsuku run` for CI/script contexts. File: `cmd/tsuku/cmd_shim.go`.

5. **Explicit activation** (`eval $(tsuku shell)`): Prints export statements for manual eval. 

Tsuku's hook install/uninstall uses marker-delimited blocks in rc files (not just appending), which allows clean removal. This is more sophisticated than niwa's append-only approach.

### Install Flow End-to-End

1. User runs `curl -fsSL ... | sh`
2. Installer detects OS/arch, downloads binary, verifies checksum
3. Binary placed at `~/.niwa/bin/niwa`
4. `~/.niwa/env` created with PATH export
5. Source line appended to appropriate shell rc files (bash: `.bashrc` + `.bash_profile`/`.profile`; zsh: `.zshenv`)
6. User told to run `. ~/.niwa/env` to activate immediately

That's it. No shell functions, no completions, no hooks.

## Implications

1. **The env file is the only extension point.** Today `~/.niwa/env` only sets PATH. A shell function wrapper for `cd`-after-create could be added to this file, but the installer currently generates it with `cat > "$ENV_FILE"` (overwrite), which means upgrades would clobber additions. This needs restructuring if the env file becomes the distribution mechanism for shell functions.

2. **The create command's output format needs work.** The current "Created instance: /path" format isn't suitable for `cd $(niwa create)` usage. Either a `--quiet`/`--porcelain` flag printing just the path, or a `--shell-hook` mode printing eval-able shell code, would be needed.

3. **Tsuku already has the pattern niwa needs.** Tsuku's `hook install` command with marker-delimited blocks, shell detection, and idempotent install/uninstall is exactly the infrastructure needed. The question is whether niwa duplicates this or tsuku generalizes it.

4. **Completions are low-hanging fruit.** Cobra provides `GenBashCompletionV2`, `GenZshCompletion`, and `GenFishCompletion` out of the box. Niwa could add a `completion` subcommand with minimal effort. But distributing completions (auto-installing them to the right location) is the harder problem and overlaps with the shell function distribution question.

5. **The append-only env file approach is fragile.** Niwa's installer writes a fresh env file on every run (`cat >`) but appends the source line idempotently. If the env file grows to include shell functions, the overwrite-on-install behavior means those functions come from the binary's installer, not from user customization. This is actually fine for managed integrations but needs to be intentional.

## Surprises

1. **Niwa's env file is overwritten on each install.** The `cat > "$ENV_FILE"` in install.sh means the env file is regenerated from scratch on every install. This is currently fine (it only has one line) but would need to change if the env file becomes a distribution mechanism for shell functions or completions. Alternatively, the env file could source additional files, keeping it as a stable entrypoint.

2. **Tsuku's hook system is more capable than expected.** It supports install, uninstall, status checking, multiple hook types (command-not-found and activation), marker-delimited blocks for clean removal, and shell detection. This is a full shell integration framework, not just a one-off hack.

3. **The issue (#31) already identifies the key tension.** It notes that `niwa create` already prints the path (true, but not in machine-parseable form) and lists all the major solution approaches. The real question isn't "what approach" but "where does the shell integration infrastructure live."

## Open Questions

1. **Should niwa own its own shell integration, or should tsuku provide a general mechanism?** Tsuku already has `hook install/uninstall/status` with shell detection and rc file management. If niwa's shell function were distributed via tsuku's install mechanism (as a post-install step in the niwa recipe), both tools benefit. But niwa also needs to work independently of tsuku.

2. **What happens to `~/.niwa/env` during upgrades?** If the env file grows beyond PATH to include shell functions, the current overwrite behavior means the installer binary controls the content. Is that the right model? Or should the env file source a separate file that the `niwa` binary manages (`niwa shell init` or similar)?

3. **Is eval-based output (`eval "$(niwa create --shell-hook)"`) acceptable UX?** This is the simplest solution that doesn't require any shell integration setup -- the binary prints `cd /path && echo "Created instance: /path"` and the user wraps it in eval. But eval patterns are unfamiliar to many users and look scary.

4. **Should niwa add cobra completions as part of this work or separately?** Completions are easy to generate but face the same distribution problem. If the shell integration mechanism is being designed, completions should be a validating use case.

## Summary

Niwa's shell integration today is minimal: a single env file (`~/.niwa/env`) that sets PATH, sourced from shell rc files by the installer, with no shell functions, no completions, and no hooks. This means any post-create navigation solution requires building new shell integration infrastructure from scratch, either within niwa or by generalizing tsuku's existing hook install/uninstall framework. The biggest open question is whether niwa should duplicate tsuku's shell integration machinery or whether tsuku should provide a general post-install shell integration capability that niwa (and other tools) can use.
