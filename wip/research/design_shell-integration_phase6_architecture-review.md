# Architecture Review: Shell Integration Design

## Review Scope

Reviewed DESIGN-shell-integration.md against the niwa codebase at commit 71f2a99 (main branch). Verified claims about existing infrastructure, checked for naming conflicts, assessed implementation feasibility, and evaluated phase sequencing.

## 1. Clarity for Implementation

The design is clear enough to implement. The stdout protocol, shell function template, data flow diagrams, and phase breakdown all map directly to code changes. The bash/zsh wrapper code is concrete and testable. The component diagram shows the full chain from rc file through to cd.

One gap: the design says `niwa init <shell|auto>` goes in `internal/cli/init.go`, but that file already exists and implements `niwa init [name]` (workspace initialization). This is a **naming collision** -- the most significant issue found.

## 2. Missing Components and Interfaces

### Critical: `init` subcommand name conflict

The existing `niwa init` command (`internal/cli/init.go`) initializes workspaces:

```
niwa init              -- scaffold workspace.toml
niwa init <name>       -- scaffold with name
niwa init --from URL   -- clone config repo
```

The design proposes `niwa init bash`, `niwa init zsh`, `niwa init auto` for shell integration. These would collide because cobra would interpret `bash`/`zsh`/`auto` as the `[name]` positional argument to the existing init command.

**Options to resolve:**

1. **Rename the shell-init subcommand.** Use `niwa shell-init <shell>` or `niwa completion-init <shell>`. Avoids all ambiguity. Precedent: `rustup completions`, `gh completion`.
2. **Nest under a parent command.** `niwa shell init <shell>`. Allows future `niwa shell` subcommands (e.g., `niwa shell env`).
3. **Rename workspace init.** Not recommended -- `init` is the conventional name for workspace bootstrapping.

Option 1 (`niwa shell-init`) is simplest. Option 2 (`niwa shell init`) is cleaner if more shell-related subcommands are likely.

### Minor: RegistryEntry.Root field

The design says `go` will use `resolveRegistryScope`, `LoadGlobalConfig`, and `LookupWorkspace` from apply.go. This checks out -- `LookupWorkspace` returns a `RegistryEntry` with both `Source` (config path) and `Root` (workspace root). The `go` command can derive the instance path from `Root`. This works.

However, the design doesn't specify how `go <name>` distinguishes between workspaces with multiple instances. If a workspace has instances `tsuku`, `tsuku-2`, `tsuku-3`, what does `niwa go tsuku` navigate to? The `Root` field on `RegistryEntry` points to the workspace root (parent of all instances), not any specific instance. The design should clarify: does `go <name>` navigate to the workspace root, the first instance, or error when multiple instances exist?

### Minor: `go` without arguments

The design says `niwa go` (no args) resolves the current workspace root from cwd. The existing `DiscoverInstance` and `config.Discover` functions walk up from cwd, which would work. But the design doesn't specify whether this means the workspace root (parent of instances) or the instance root. These are different directories. `DiscoverInstance` returns the instance root, while `config.Discover` + `filepath.Dir` gives the workspace root. The `go` command description says "workspace root" but the use case (getting back to your workspace from a nested repo) likely means instance root. This should be clarified.

### Missing: Shell detection for `auto` mode

The design specifies checking `$ZSH_VERSION` and `$BASH_VERSION`. This works when the init output is evaled during shell startup. However, the design should note that `$BASH_VERSION` is only set in bash (not in sh-compatible shells running bash scripts). Since the env file uses `/bin/sh` semantics (it's sourced, not executed with a shebang), the detection relies on the parent shell setting these variables -- which it does, since `.bashrc` is sourced by bash and `.zshenv` by zsh. This is correct but worth a brief note in the design for implementers.

## 3. Phase Sequencing

The three phases are correctly sequenced:

- **Phase 1** (init subcommand + stdout protocol) is self-contained. The init command can be built and tested without env file changes. The `create.go` stdout change is a prerequisite for Phase 2 to be useful.
- **Phase 2** (env file delegation) depends on Phase 1 -- it calls `niwa init auto`. The `command -v` guard correctly handles the case where the binary predates the init subcommand.
- **Phase 3** (go command) is independent of Phase 2 but depends on Phase 1 for the wrapper function. It could be swapped with Phase 2 in principle, but the current order makes sense: Phase 2 enables automatic loading, and Phase 3 adds the second use case.

One suggestion: the design says Phase 3 deliverables include "Update init output to intercept `go` alongside `create`". This means the shell function changes between Phase 1 and Phase 3. Users who eval the Phase 1 output won't pick up the `go` case until their shell restarts (the eval happens once at shell startup). This isn't a bug -- it's inherent to the eval-init pattern -- but worth noting. The Phase 1 shell function could include `go` in the case statement from the start (it would just fail with "unknown command" until Phase 3 ships).

## 4. Simpler Alternatives

The design already evaluated and rejected the main alternatives (temp files, fd 3, env-only). The chosen approach matches zoxide's proven pattern. No simpler alternative was overlooked.

One minor simplification worth considering: **skip cobra completion bundling in the init output.** Cobra already generates `niwa completion bash` and `niwa completion zsh` commands. Bundling completions into `niwa init` output adds complexity (the init command needs to call cobra's generation functions and concatenate output). Users who want completions can run `niwa completion bash` separately. The incremental convenience of bundling is small, and it couples the init command to cobra internals.

If completions are bundled, the design should specify how. Cobra's `GenBashCompletionV2` writes to an `io.Writer`. The init command would need to call this and append the result to its output. This is straightforward but untested -- the design should confirm that cobra's completion output is safe to concatenate with an arbitrary shell function.

## 5. Codebase Verification Summary

| Claim | Verified | Notes |
|-------|----------|-------|
| create.go outputs "Created instance:" to stdout | Yes | Line 104: `fmt.Fprintf(cmd.OutOrStdout(), "Created instance: %s\n", instancePath)` |
| No known scripts parse create output | Cannot verify externally, but the claim is plausible given the project's early stage |
| Env file exists at ~/.niwa/env | Yes | install.sh creates it at line 105-108 |
| Env file is overwritten on each install | Yes | `cat > "$ENV_FILE"` (truncate+write) at line 105 |
| install.sh sources env from .bashrc/.zshenv | Yes | Lines 132-155 |
| Cobra completions work | Yes | Cobra provides built-in `completion` subcommand by default (rootCmd inherits it) |
| apply.go has resolveRegistryScope, LoadGlobalConfig, LookupWorkspace | Yes | apply.go line 117, config/registry.go lines 40, 75 |
| `niwa init` is available for the shell init subcommand | **No** | `niwa init` already exists as workspace initialization (init.go, line 21-39) |

## 6. Recommendations

1. **Rename the shell-init subcommand** to avoid the `init` collision. `niwa shell-init` is direct and unambiguous.
2. **Clarify `go` semantics for multi-instance workspaces.** Specify whether `niwa go <name>` navigates to the workspace root, picks the first instance, or requires disambiguation.
3. **Clarify `go` no-args target.** Specify whether it means workspace root or instance root.
4. **Consider including `go` in the Phase 1 case statement** even before the `go` command exists, to avoid a shell function change between phases.
5. **Consider deferring completion bundling** to reduce Phase 1 scope. Completions via `niwa completion bash` already work.
