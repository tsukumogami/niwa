# Lead: How do other CLI tools solve parent-shell navigation?

## Findings

### The Fundamental Constraint

A child process cannot modify its parent process's environment. This means a
compiled binary (or any subprocess) cannot `cd` the user's shell. Every tool
that needs to affect the parent shell must arrange for shell-native code to
run _in_ the parent shell. The mechanisms fall into a few distinct patterns.

### Pattern 1: Sourced Shell Function Wrapper

**Tools:** nvm, rvm, pyenv (via pyenv-virtualenv)

**Mechanism:** The tool ships a shell script that the user sources in their
shell rc file (`.bashrc`, `.zshrc`). This script defines a shell function
with the same name as the tool. When the user types e.g. `nvm use 18`, they
are calling the shell function, not a binary. The function can run arbitrary
shell code including `cd`, `export`, and `eval`.

**nvm:** Entirely implemented as a sourced shell function. There is no `nvm`
binary. The user adds `source ~/.nvm/nvm.sh` to their rc file. The `nvm`
function handles `cd`, `PATH` manipulation, version switching, and
completions all within the shell process.

**rvm:** Similar to nvm. The user sources `~/.rvm/scripts/rvm` which defines
an `rvm()` function. This function can modify `PATH`, `GEM_HOME`, and `cd`
into directories as needed.

**pyenv:** Uses a hybrid. `pyenv` is a real binary (shell script collection),
but `pyenv init` outputs shell code that the user evals. This sets up a
`pyenv()` shell function that intercepts certain subcommands. Most commands
delegate to the binary, but shell-affecting operations run in the function.

**Characteristics:**
- Maximum flexibility: can do anything in the parent shell
- Setup cost: user must add a `source` line to rc files
- Maintenance burden: must maintain shell code for bash, zsh, and optionally fish
- Startup cost: sourcing adds to shell initialization time

### Pattern 2: Eval-Based Init Hook

**Tools:** direnv, pyenv, rbenv, starship, zoxide, mise (formerly rtx)

**Mechanism:** The tool is a compiled binary. During setup, the user adds
`eval "$(tool init bash)"` or `eval "$(tool init zsh)"` to their rc file.
The binary outputs shell code on stdout; `eval` executes it in the parent
shell. The outputted code typically defines shell functions, hooks, or prompt
modifications.

**direnv:** The user adds `eval "$(direnv hook bash)"` to `.bashrc`. The
binary outputs a shell function that hooks into the prompt command
(`PROMPT_COMMAND` in bash, `precmd` hook in zsh). On each prompt, the hook
checks `.envrc` files and modifies the environment. direnv itself is a Go
binary -- it doesn't try to `cd`, but it does need to modify `PATH` and
environment variables, which is the same class of problem.

**zoxide:** The user adds `eval "$(zoxide init bash)"` to `.bashrc`. This
defines a `z` shell function (and optionally `zi` for interactive). When the
user types `z foo`, the shell function calls the `zoxide` binary to resolve
the path, then runs `cd` in the parent shell. This is the closest analog to
niwa's use case -- a binary that needs to trigger `cd` in the parent shell.

**rbenv:** `eval "$(rbenv init -)"` outputs a shell function that wraps
certain rbenv commands and sets up PATH shims.

**mise (formerly rtx):** `eval "$(mise activate bash)"` outputs hook code
similar to direnv's approach.

**Characteristics:**
- Binary does the heavy lifting; shell code is minimal (generated, not hand-maintained)
- The binary can version its shell output as the tool evolves
- Shell-specific output is handled by a single `init` subcommand with a shell argument
- Startup cost is typically small (one subprocess spawn)
- Clean separation: binary logic vs. shell glue

### Pattern 3: Stdout Path Capture (Subshell Composition)

**Tools:** Many tools support this as a secondary pattern

**Mechanism:** The binary prints a path to stdout. The user composes it with
`cd`: `cd $(tool resolve foo)`. No persistent shell setup needed.

**Examples:**
- `cd $(brew --prefix)` -- navigate to Homebrew's prefix
- `cd $(git rev-parse --show-toplevel)` -- navigate to repo root
- `cd $(mktemp -d)` -- navigate to a temp directory

**Characteristics:**
- Zero setup cost
- Requires the user to know the composition pattern
- Cannot trigger automatically
- Awkward UX for a "create and navigate" workflow (output path conflicts with other stdout)
- Works universally across shells without shell-specific code

### Pattern 4: Shell Alias Installation

**Tools:** rustup (for `cargo`, `rustc`)

**Mechanism:** rustup modifies `PATH` by appending a line to the user's
shell rc file during installation. It doesn't define shell functions but
ensures its bin directory is on PATH. For the specific case of `rustup
completions`, it generates completion scripts that the user must manually
install.

**Characteristics:**
- One-time setup during install, not per-session
- Modifying rc files is invasive but widely accepted for installers
- Limited to PATH changes; can't do per-command shell manipulation

### Pattern 5: Directory Protocol Output

**Tools:** Some file managers (ranger, yazi, nnn)

**Mechanism:** The tool writes a destination path to a temporary file on
exit. A shell wrapper reads the file and `cd`s. For example, ranger can be
wrapped with a function that checks `~/.rangerdir` after exit:

```bash
ranger() {
    command ranger --choosedir=$HOME/.rangerdir "$@"
    cd "$(cat $HOME/.rangerdir)"
}
```

yazi uses a similar pattern with `--cwd-file`.

**Characteristics:**
- Binary communicates via file rather than stdout (avoids stdout conflicts)
- Still requires a shell function wrapper
- Clean separation of concerns

### Comparison Matrix

| Pattern | Shell Setup | Binary Needed | Can cd | Maintenance | Examples |
|---------|-----------|--------------|--------|-------------|---------|
| Sourced function | `source script` in rc | No (pure shell) | Yes | High (per-shell) | nvm, rvm |
| Eval init hook | `eval "$(bin init)"` in rc | Yes | Yes | Low (binary generates) | zoxide, direnv, pyenv |
| Stdout capture | None | Yes | Yes (manual) | None | git, brew |
| RC file modification | One-time install | Yes | No (PATH only) | None | rustup |
| Directory protocol | Wrapper function | Yes | Yes | Medium | ranger, yazi |

### Which Pattern Is Most Common for Modern Tools?

The eval-based init hook (Pattern 2) has become the dominant approach for
modern CLI tools written in compiled languages. zoxide, direnv, starship,
mise, and atuin all use it. The reasons:

1. **Minimal shell code.** The binary generates just enough shell glue. The
   tool's authors maintain Go/Rust/etc., not bash.
2. **Shell detection is trivial.** `tool init bash` vs `tool init zsh` --
   the binary can emit different code per shell.
3. **Versioning is natural.** When the binary updates, the init output
   updates automatically. No separate shell script to distribute.
4. **Composable.** Users understand `eval "$(...)"`  from direnv, zoxide,
   etc. It is a known pattern.
5. **One-line setup.** Adding a single line to `.bashrc`/`.zshrc` is the
   lowest viable setup cost for persistent shell integration.

### Zoxide as the Closest Analog

Zoxide's architecture maps almost directly to niwa's need:

- `zoxide` is a compiled binary (Rust) that maintains a database of
  directories
- `eval "$(zoxide init bash)"` defines a `z()` shell function
- `z foo` calls `zoxide query foo` to resolve a path, then the shell
  function runs `builtin cd` to that path
- The binary never touches the shell's working directory; the shell function
  does

For niwa, the equivalent would be:
- `niwa` remains a compiled Go binary
- `eval "$(niwa init bash)"` defines a `niwa()` shell function
- The function intercepts `niwa create` (and possibly `niwa go`), calls the
  binary, captures the output path, and runs `cd`
- All other subcommands pass through to the binary unchanged

### Completions: Same Pattern, Different Content

Shell completions face the same constraint: the binary knows the completions,
but they must be registered in the parent shell. The same `init` subcommand
can output both the wrapper function and completion registration:

```bash
# What `niwa init bash` could output:
niwa() {
    case "$1" in
        create|go)
            local output
            output=$(command niwa "$@")
            local rc=$?
            if [ $rc -eq 0 ] && [ -d "$output" ]; then
                cd "$output" || return
            else
                printf '%s\n' "$output"
                return $rc
            fi
            ;;
        *)
            command niwa "$@"
            ;;
    esac
}

# completions
eval "$(command niwa completions bash)"
```

cobra (niwa's CLI framework) has built-in completion generation, which can
be wired into this same init flow.

## Implications

1. **The eval-init pattern is the right fit for niwa.** It is the most common
   modern approach, requires minimal shell code, and maps directly to the
   problem. Zoxide proves the pattern works for "binary resolves path, shell
   function does cd."

2. **A `niwa init <shell>` subcommand is the right interface.** It outputs
   shell code that defines a wrapper function and optionally registers
   completions. Users add `eval "$(niwa init bash)"` to their rc file.

3. **This does NOT necessarily require tsuku to gain a general post-install
   shell integration mechanism.** The init subcommand is self-contained in
   niwa. tsuku's role is just to ensure `niwa` is on PATH (which it already
   does). The user adds the eval line themselves, or niwa's recipe could
   include a post-install message suggesting the line to add.

4. **However, if tsuku wants to automate the "add eval line to rc file"
   step**, that is a separate, larger feature. Most tools (zoxide, direnv,
   starship) leave this to the user. rustup is the exception, modifying rc
   files during install. The convention is: tell the user what to add, don't
   modify their rc files automatically.

5. **Completions are a freebie.** Once `niwa init` exists, adding completion
   output to it is trivial since cobra already generates completion scripts.
   This collapses two problems (navigation + completions) into one mechanism.

## Surprises

1. **No modern compiled-language tool uses the "pure sourced function"
   approach (Pattern 1).** nvm and rvm, which are entirely shell scripts, are
   the only prominent users. Every tool written in Go, Rust, or similar uses
   the eval-init pattern instead. This suggests the pattern has clear
   advantages for compiled tools.

2. **The stdout-capture approach (`cd $(tool create)`) is viable but never
   the primary recommended UX for any tool.** It always exists as a power-user
   escape hatch. No tool relies on users knowing to compose commands this way.

3. **tsuku's existing PATH setup (modifying rc files during install) is
   already more invasive than what most tools do for shell integration.**
   Adding an eval line is actually a lighter touch than what rustup/tsuku
   already do with PATH.

## Open Questions

1. **Should `niwa init` be added to rc files by `tsuku install niwa` or by
   the user manually?** The prior art overwhelmingly says "tell the user."
   But tsuku already modifies rc files for PATH. Is consistency with tsuku's
   approach more important than consistency with the broader ecosystem?

2. **What subcommands should the wrapper intercept?** `niwa create` is
   obvious. Should there be a `niwa go <workspace>` or `niwa cd <workspace>`
   command for navigating to existing workspaces?

3. **Should the wrapper function be named `niwa` (shadowing the binary) or
   something else?** zoxide uses `z` (a different name). direnv uses `direnv`
   (same name, but most interaction is through hooks, not direct calls). The
   zoxide approach avoids confusion but adds a name to learn.

4. **Does the shell function need to handle `niwa create` failing
   gracefully?** If create fails, the function should not `cd`. It needs to
   distinguish success-with-path from error output on stdout.

## Summary

Every modern compiled CLI tool that needs to affect the parent shell uses the
eval-init pattern: a subcommand outputs minimal shell code that the user evals
in their rc file, defining a function that bridges binary logic to shell
operations. This pattern (proven by zoxide, direnv, and others) maps directly
to niwa's need and also handles completions, meaning a `niwa init <shell>`
subcommand solves both navigation and completion installation without
requiring tsuku to gain new post-install capabilities. The main open question
is whether tsuku should automate adding the eval line to rc files (matching
its existing PATH behavior) or follow the broader ecosystem convention of
telling users to add it themselves.
