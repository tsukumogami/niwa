# Lead: What patterns exist for distributing shell completions?

## Findings

### Pattern 1: Generate-to-stdout (the dominant pattern)

Nearly all major CLI tools follow a "generate and print" model. The tool has a subcommand that writes completion scripts to stdout, and the user redirects that output to the right file.

**Examples:**
- `gh completion -s bash` -- prints bash completions to stdout
- `kubectl completion bash` -- prints bash completions to stdout
- `rustup completions bash` -- prints bash completions to stdout
- Cobra auto-generates `niwa completion bash` for free (confirmed working)

The tool never writes files itself. The user decides where to put them.

### Pattern 2: Cobra's built-in completion command

Cobra (which niwa uses) automatically adds a `completion` subcommand with bash/zsh/fish/powershell support. No code needed -- niwa already has this working:

```
$ niwa completion bash    # generates bash completion script
$ niwa completion zsh     # generates zsh completion script
```

The generated help text even includes platform-specific installation instructions. Cobra provides these methods on any `*cobra.Command`:
- `GenBashCompletionV2(w io.Writer, includeDesc bool)` -- modern bash completions
- `GenZshCompletion(w io.Writer)` -- zsh completions
- `GenFishCompletion(w io.Writer, includeDesc bool)` -- fish completions

Tsuku overrides this with a custom completion command at `/public/tsuku/cmd/tsuku/completion.go` that manually calls these same methods, giving it control over the interface (`completion bash` instead of `completion bash`).

### Pattern 3: Installation locations vary by shell and platform

**Bash completions (three levels):**
1. System-wide: `/etc/bash_completion.d/` or `/usr/share/bash-completion/completions/`
2. User-level (lazy-loaded): `~/.local/share/bash-completion/completions/<tool>`
3. Sourced in rc file: `source <(tool completion bash)` in `~/.bashrc`

The user-level directory (`~/.local/share/bash-completion/completions/`) is the cleanest option. If bash-completion package is installed, files placed here are lazily loaded when the command name matches the filename. No rc file modifications needed.

**Zsh completions (two levels):**
1. System-wide: `/usr/share/zsh/vendor-completions/` or `/usr/local/share/zsh/site-functions/`
2. Custom fpath directory: user adds a directory to `$fpath` and puts `_tool` files there

Zsh completion files must be named `_<toolname>` (e.g., `_niwa`). The user must have `autoload -U compinit; compinit` in their `.zshrc`.

**Confirmed on this system:**
- `/etc/bash_completion.d/` exists with kubectl, gcloud, bazel completions
- `/usr/share/bash-completion/completions/` exists with hundreds of completions
- `/usr/share/zsh/vendor-completions/` exists with system completions

### Pattern 4: Package managers handle installation

When tools are installed via package managers (apt, brew, rpm), the package includes pre-generated completion files placed in system directories:

- gh's Debian package installs `/usr/share/bash-completion/completions/gh` and `/usr/share/fish/vendor_completions.d/gh.fish` (confirmed via `dpkg -L gh`)
- This means the user never runs `gh completion` at all if installed via apt

This is the "zero configuration" path -- but only available for package-manager-distributed tools.

### Pattern 5: Source-in-rc-file (eval approach)

Some tools recommend adding a line to `~/.bashrc` or `~/.zshrc`:
```bash
eval "$(gh completion -s bash)"
eval "$(tool completion bash)"
source <(kubectl completion bash)
```

This is the simplest for users but has drawbacks:
- Adds shell startup latency (tool must execute on every shell open)
- Requires the tool binary to be on PATH when shell starts
- Multiple tools doing this accumulate startup cost

### Pattern 6: Installer writes completions automatically

Rustup is notable: its installer (`~/.cargo/env`) modifies PATH but does NOT auto-install completions. Completions are a separate manual step. This is typical -- even tools that modify rc files for PATH avoid auto-installing completions.

No widely-used CLI tool was found that auto-installs completions during `tool install` without explicit user action.

### Generation vs Installation (key distinction)

| Concern | Who owns it | Example |
|---------|------------|---------|
| **Generating** completion scripts | The CLI tool | `niwa completion bash` |
| **Installing** to a system/user directory | Package manager or user | `niwa completion bash > ~/.local/share/bash-completion/completions/niwa` |
| **Loading** completions into the shell | Shell config (bashrc/zshrc) or lazy-load mechanism | `source <(niwa completion bash)` or bash-completion lazy-load |

Most tools only handle generation. Installation is either the user's job or the package manager's job.

### tsuku's existing approach

Tsuku already has a custom completion command (`/public/tsuku/cmd/tsuku/completion.go`) that follows Pattern 1: generate-to-stdout with explicit shell argument. It calls cobra's `GenBashCompletionV2`, `GenZshCompletion`, and `GenFishCompletion` methods directly.

## Implications

1. **Niwa already has completion generation for free.** Cobra auto-adds `niwa completion {bash,zsh,fish,powershell}`. No code changes needed for generation.

2. **The gap is installation, not generation.** If tsuku wants to provide a smooth experience, it needs a mechanism to place completion files in the right location. This is the same class of problem as the `cd` use case: something needs to modify the user's shell environment (either files in completion directories or lines in rc files).

3. **A shell integration mechanism could serve both use cases.** The "source a file on shell startup" pattern is already standard for completions (`eval "$(tool completion bash)"` in bashrc). A shell wrapper function (the approach being explored for `cd` after create) could also set up completions. Both use cases involve: (a) detecting the user's shell, (b) writing/sourcing something in the rc file or a managed directory.

4. **The cleanest completion installation path is file-based, not eval-based.** Writing to `~/.local/share/bash-completion/completions/niwa` (bash) or a managed fpath directory (zsh) avoids shell startup cost and doesn't require rc file modification for bash. For zsh, a single fpath addition in zshrc is needed.

5. **tsuku could own the "install completions" step.** Since tsuku manages tool installation, it could have a post-install action that generates and places completion files. This would be a concrete second use case for a shell integration mechanism: `tsuku install niwa` could run `niwa completion bash > ~/.local/share/bash-completion/completions/niwa` automatically.

## Surprises

1. **Cobra gives niwa completions for free with zero code.** The `niwa completion` subcommand already exists and works. This was confirmed by building niwa and running `niwa completion bash --help`. Many cobra-based tools add custom completion commands unnecessarily.

2. **No major CLI tool auto-installs completions.** Even rustup, which modifies `~/.bashrc` equivalent for PATH, leaves completions as a manual step. This suggests the ecosystem considers auto-installation of completions somewhat risky or opinionated.

3. **Bash has a zero-config lazy-load path.** If bash-completion is installed, placing a file in `~/.local/share/bash-completion/completions/<name>` just works -- no rc file changes needed. This is cleaner than the eval approach most tools recommend.

4. **Zsh always requires some rc file setup.** Unlike bash's lazy-load, zsh needs `fpath` configuration and `compinit` in `.zshrc`. There's no truly zero-config path for zsh.

## Open Questions

1. **Should tsuku provide a `tsuku completions install` action?** This would place generated completion files in the right directories. It's a natural extension of tsuku's "manage your tools" mission.

2. **Is the shell wrapper function (for `cd`) the right vehicle for completions too?** Or should completions use the file-based approach (which doesn't require rc file sourcing for bash) while `cd` uses the wrapper approach?

3. **How should the mechanism detect which shells the user actually uses?** The system might have both bash and zsh installed, but the user might only use one.

4. **What happens on tool upgrade?** Completions may change between versions. Does the installation mechanism need to regenerate completions on upgrade?

5. **Should this be opt-in or automatic?** No major tool auto-installs completions. But tsuku's value proposition is reducing manual setup. Where's the right line?

## Summary

All major CLI tools (gh, kubectl, rustup, cobra-based CLIs) follow the same pattern: a `tool completion <shell>` subcommand generates scripts to stdout, and installation is left to the user or package manager -- niwa already gets this for free from cobra. The gap between generation and installation is exactly the kind of shell integration problem that a tsuku post-install mechanism could solve, giving it two concrete use cases (workspace `cd` and completion installation). The biggest open question is whether completions should use file-based installation (writing to `~/.local/share/bash-completion/completions/`) which is zero-config for bash, or whether both use cases should share a single rc-file-sourcing mechanism.
