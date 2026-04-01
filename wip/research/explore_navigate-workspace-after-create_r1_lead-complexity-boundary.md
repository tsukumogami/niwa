# Lead: Niwa-only vs. tsuku-generalized -- complexity boundary

## Findings

### What niwa create currently does

`niwa create` (in `internal/cli/create.go`) calls `applier.Create()` which clones repos, installs CLAUDE.md files, and writes state. The final output is a single line to stdout:

```go
fmt.Fprintf(cmd.OutOrStdout(), "Created instance: %s\n", instancePath)
```

The instance path is the directory the user wants to `cd` into. Today, the user must manually copy-paste or retype this path. There's no machine-readable output mode (no `--quiet` flag that prints just the path).

### Niwa's existing shell integration surface

Niwa already has an install script (`install.sh`) that:
1. Creates `~/.niwa/env` with a PATH export
2. Sources that env file from `~/.bashrc`, `~/.zshenv`, etc.

This is a thin integration -- it only sets PATH. There's no prompt hook, no eval mechanism, and no `niwa hook` subcommand.

### Tsuku's existing shell integration surface

Tsuku has a much richer shell integration layer:

1. **`tsuku shellenv`** -- prints `export PATH=...` for one-time setup (`eval $(tsuku shellenv)`)
2. **`tsuku shell`** -- prints shell exports to activate project tools (`eval $(tsuku shell)`)
3. **`tsuku hook-env <shell>`** -- hidden command called by prompt hooks to compute per-directory activation
4. **`tsuku hook install [--activate] [--shell=X]`** -- registers hooks in rc files
5. **Embedded hook scripts** (`internal/hooks/tsuku-activate.{bash,zsh,fish}`) -- prompt hooks that call `tsuku hook-env` on every prompt
6. **`tsuku hook uninstall`** and **`tsuku hook status`** -- lifecycle management

The hook infrastructure in `internal/hook/install.go` handles:
- Marker-based idempotent rc file modification (separate markers for command-not-found vs activate)
- Atomic writes to prevent rc file corruption
- Per-shell hook file templates written to `$TSUKU_HOME/share/hooks/`
- Fish conf.d integration

### The three niwa-only approaches and their complexity

**Approach A: stdout path capture -- `cd $(niwa create --quiet ...)`**

Minimal change: add a `--quiet` flag that prints only the path to stdout (messages go to stderr). This is ~10 lines of Go.

Complexity: very low. But the UX is clunky -- users must remember the subshell syntax every time. No repo-within-workspace navigation.

**Approach B: eval-based -- `eval "$(niwa create --shell-hook)"`**

Add a `--shell-hook` flag that makes `niwa create` output shell commands instead of human text. Output would be something like:

```bash
cd /path/to/workspace/instance
```

Complexity: low (~30 lines). But still requires the user to remember `eval "$(...)"` every time. Could be extended to set env vars or print a welcome message.

**Approach C: shell function wrapper in niwa's env file**

Add a `niwa()` shell function to `~/.niwa/env` that wraps the binary:

```bash
niwa() {
    local output
    output=$("$(command -v niwa)" "$@")
    local rc=$?
    if [ $rc -eq 0 ] && [ "$1" = "create" ]; then
        local dir
        dir=$(echo "$output" | grep '^Created instance:' | sed 's/^Created instance: //')
        if [ -n "$dir" ]; then
            cd "$dir" || true
        fi
    fi
    echo "$output"
    return $rc
}
```

Complexity: moderate (~40 lines of shell in the env file, plus Go changes to ensure stable output format). Works transparently -- users just type `niwa create` and land in the directory. Supports future extension for `niwa apply --go <repo>` style navigation.

Downside: shell function wrappers are fragile. Output parsing is error-prone. Need to handle both bash and zsh (and potentially fish). The function shadows the binary, which can confuse debugging.

**Approach C variant: structured output protocol**

Instead of parsing human text, niwa outputs a structured directive:

```go
// On create success, print to stderr for humans, print directive to fd 3 or a temp file
fmt.Fprintf(cmd.OutOrStdout(), "Created instance: %s\n", instancePath)
// Machine-readable directive:
fmt.Fprintf(os.NewFile(3, "directives"), "__NIWA_CD=%s\n", instancePath)
```

The shell function reads fd 3 or a well-known temp file. This avoids output parsing but adds protocol complexity.

### The tsuku-generalized approach

Tsuku already has all the building blocks for shell integration. A generalized approach would:

1. **Add a `post_install_shell` action type** to tsuku recipes, or a `shell_functions` field
2. **Extend `tsuku hook install`** to also source tool-specific shell functions
3. **Create a `~/.tsuku/share/shell-functions/` directory** where installed tools can drop shell function files
4. **Tsuku's env file or activation hook** sources all files in that directory

Niwa's recipe would include a shell function file, and `tsuku install niwa` would put it in the right place.

Complexity: high. This requires:
- New recipe field or action type in tsuku
- New directory convention in tsuku's home
- Changes to tsuku's env file or hook scripts to source the directory
- Niwa to ship a shell function file
- Coordination between two repos

### What about bash completions as a second use case?

Tsuku already handles completions via `tsuku completion <shell>` (using cobra's built-in generators). Niwa could do the same with `niwa completion <shell>`. This is independent of the shell function wrapper question.

However, if tsuku had a general `share/shell-functions/` mechanism, completions could be dropped there too. Currently tsuku does NOT auto-install completions -- it requires `source <(tsuku completion bash)` in the user's rc file. This is a manual step, same pattern as the niwa navigation problem.

So completions don't strongly validate the generalized approach -- they're already handled per-tool via cobra's completion commands.

### Where the complexity boundary actually sits

The boundary is between "niwa needs to change its own output" and "tsuku needs to change its infrastructure."

| Approach | Niwa changes | Tsuku changes | User friction | Maintenance |
|----------|-------------|---------------|---------------|-------------|
| `--quiet` flag | ~10 LOC | None | High (remember syntax) | Trivial |
| `--shell-hook` + eval | ~30 LOC | None | Medium (remember eval) | Low |
| Shell function in env | ~50 LOC (Go + shell) | None | None (transparent) | Medium |
| Tsuku generalized | ~20 LOC (ship function file) | ~200+ LOC (new infrastructure) | None (transparent) | High (cross-repo) |

The shell function in niwa's own env file hits the sweet spot: zero user friction, contained within niwa, and no cross-repo coordination.

### Would other tools duplicate the pattern?

Koto (workflow orchestration engine) is unlikely to need post-command shell state changes. It's a build/run tool, not a navigation tool. Tsuku itself already has its own shell integration.

The only clear consumer of "tool that changes shell directory" is niwa. Directory navigation after workspace creation is a workspace-manager-specific concern. General tools don't need this -- they operate on files or processes, not the user's working directory.

The pattern nvm/rvm use (shell function wrapper) is specific to version managers that modify PATH, which is similar to what tsuku already does with its activation hooks. niwa's need (cd after create) is different enough that generalizing it in tsuku would be premature abstraction.

## Implications

1. **Niwa should own its shell function wrapper.** The `~/.niwa/env` file already exists and is sourced in every shell session. Adding a `niwa()` function there is the natural extension point.

2. **The install script already modifies shell configs.** No new integration pathway is needed. The env file is the delivery mechanism.

3. **Tsuku's generalized approach is premature.** There's no second consumer for a "post-install shell function" mechanism. The complexity cost (~200+ LOC across two repos plus ongoing coordination) isn't justified by one use case.

4. **A `--quiet` mode is still valuable** even with a shell function, for scripting and CI use cases. It's additive and cheap.

5. **The shell function should use a structured protocol**, not output parsing. The cleanest approach: `niwa create` writes a directive file (e.g., `~/.niwa/.last-cd`), and the shell function reads it after the binary exits. This avoids parsing stdout and works regardless of output format changes.

## Surprises

1. **Tsuku's shell integration is much more mature than expected.** It has prompt hooks, activation hooks, hook install/uninstall/status lifecycle, atomic rc file writes, and per-shell template embedding. This is a production-grade shell integration framework. Despite this, it has NO general mechanism for tools to register their own shell functions -- it's all tsuku-specific.

2. **Niwa already has the env file infrastructure.** The install script creates `~/.niwa/env` and sources it from shell configs. This means the delivery mechanism for a shell function wrapper is already in place -- no new installer changes needed.

3. **Completions don't validate generalization.** Both tsuku and niwa use cobra, which has built-in `completion` subcommands. These are self-service -- no framework support needed. This removes what seemed like a compelling second use case for tsuku-level generalization.

## Open Questions

1. **Should the shell function handle `niwa apply --go <repo>` too?** Issue #31 mentions jumping to a specific repo. This would mean the shell function needs to intercept more than just `create`.

2. **Fish support:** The env file approach works for bash/zsh but fish uses a different function syntax. Should the first implementation support fish, or defer it?

3. **What happens when niwa is installed via tsuku vs. standalone?** If tsuku installs niwa, does it also set up the env file? Or does niwa's own post-install setup handle it? This is an integration seam that needs clarity regardless of which approach is chosen.

4. **Directive file vs. exit-code protocol:** Should the shell function detect "cd needed" via a temp file (`~/.niwa/.last-cd`), a special exit code, or by parsing a known output line? Each has trade-offs around race conditions (multiple shells) and reliability.

## Summary

The niwa-only shell function wrapper in `~/.niwa/env` is the right approach: it costs ~50 lines of code across Go and shell, requires no tsuku changes, and delivers transparent UX where `niwa create` lands the user in the new workspace directory. Generalizing this in tsuku would cost 200+ lines across two repos with no second consumer -- bash completions are already handled per-tool by cobra's built-in commands, removing the validating use case. The main open question is the communication protocol between the binary and the shell function (output parsing vs. directive file vs. exit code), which determines fragility and race-condition behavior.
