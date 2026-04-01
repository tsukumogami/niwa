# Current-State Analysis: Predecessor UX Contract

## What newtsuku did

newtsuku created a new workspace and navigated the user into it. It was a shell
function (not a compiled binary), so it could call `cd` directly in the parent
shell. By default it landed at the workspace root. A `-c <repo>` flag landed in
a specific repo within the workspace. The workflow was single-command: create and
navigate in one step.

## What resettsuku did

resettsuku reset (deleted and recreated) an existing workspace, then navigated
the user back to the repo they were in before the reset. It detected the current
repo from cwd before destruction, ran the reset, then `cd`'d back to the
equivalent repo path in the fresh workspace. This was necessary because the
delete-and-recreate cycle invalidated the user's cwd.

## Behaviors preserved in niwa

| Predecessor behavior | Niwa equivalent | Notes |
|---------------------|-----------------|-------|
| Create + auto-navigate to workspace root | `niwa create` with shell wrapper | Shell function intercepts create, captures stdout path, runs `builtin cd` |
| Create + navigate to specific repo (`-c <repo>`) | `niwa create --cd <repo>` | Flag name changed from `-c` to `--cd`; resolves repo against classified list |
| Single-command workflow | Same | User types one command, lands in the right directory |

The core UX contract -- "create a workspace and land in it" -- carries forward
unchanged. The mechanism shifts from a pure shell function to the eval-init
pattern (compiled binary + thin shell wrapper), but the user-facing behavior
is identical.

## Behaviors changed

| Predecessor behavior | Niwa behavior | Rationale |
|---------------------|---------------|-----------|
| resettsuku navigates back to current repo after reset | `niwa apply` has no navigation | apply is non-destructive (updates in place), so the user's cwd stays valid throughout. No directory is deleted, no navigation needed. |
| Shell functions owned all logic | Binary owns logic, shell function is glue | Eval-init pattern: binary resolves paths and prints to stdout, shell function only captures and cds. Under 15 lines of shell code. |
| Direct shell function (sourced script) | Eval-init pattern (`eval "$(niwa shell-init auto)"`) | Follows modern convention (zoxide, direnv, mise). Binary versions its shell output. |

The biggest behavioral change is that apply (niwa's analog to resettsuku) does
not participate in shell integration at all. This isn't a regression -- it's a
consequence of a fundamental design difference. resettsuku destroyed the
workspace and recreated it, invalidating the user's cwd. niwa apply updates
in place without deletion, so the cwd remains valid. Post-apply navigation is
unnecessary.

## Behaviors dropped

| Predecessor behavior | Status | Rationale |
|---------------------|--------|-----------|
| resettsuku's "detect current repo, restore after reset" | Dropped | apply is non-destructive; cwd stays valid. The detect-and-restore pattern was a workaround for destructive reset, not a feature in its own right. |
| Shell-function-only implementation | Dropped | Compiled binary enables proper argument parsing, error handling, and cross-platform support. Shell glue is minimal. |

## Behaviors that are new (no predecessor equivalent)

| New behavior | Description |
|-------------|-------------|
| `niwa go [workspace] [repo]` | Navigate to existing workspaces and repos. No arguments = workspace root from cwd. With workspace name = resolve via global registry. With both = repo directory within instance. |
| `niwa shell-init install/uninstall/status` | Explicit lifecycle management for shell integration. Install writes delegation to env file and adds source line. Uninstall reverts to PATH-only. Status reports current state. |
| `niwa shell-init bash\|zsh\|auto` | Generates shell wrapper + cobra completions. auto detects shell from environment variables. |
| Shell completions | Cobra-generated completions bundled into shell-init output. Predecessor had none. |
| `--no-shell-init` on install.sh | Installer flag to skip shell integration entirely. For CI and containerized environments. |
| Runtime hint when shell integration is missing | cd-eligible commands check `_NIWA_SHELL_INIT` and print a hint to stderr if the wrapper isn't loaded. |
| Env file delegation with command-v guard | `~/.niwa/env` delegates to `niwa shell-init auto` when the binary is available. Existing users get shell integration on upgrade without changing rc files. Safe fallback if binary predates shell-init. |

## Migration path

For users coming from newtsuku/resettsuku:

1. `newtsuku` -> `niwa create`: Direct replacement. Auto-navigates on create.
   The `-c <repo>` flag becomes `--cd <repo>`.
2. `resettsuku` -> `niwa apply`: Behavioral change. apply updates in place
   without navigation. If the user needs to navigate after apply, use `niwa go`.
3. Shell setup: Was `source ~/.nvm/nvm.sh`-style sourced function. Now
   `. "$HOME/.niwa/env"` which delegates to eval-init. Existing env file users
   get shell integration automatically on upgrade.
4. New capability: `niwa go` provides workspace/repo navigation that had no
   predecessor equivalent. This partially compensates for dropping resettsuku's
   auto-navigate behavior by giving users an explicit navigation command.

## Key insight

The predecessor tools were shell functions that happened to manage workspaces.
Niwa is a compiled binary that happens to need shell integration. This inversion
means the shell layer is thinner and more maintainable, but it also means shell
integration is an explicit, optional feature rather than an inherent property of
the tool. The design leans into this by making shell integration fully opt-in
with lifecycle commands.
