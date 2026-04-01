<!-- decision:start id="env-file-transition" status="assumed" -->
### Decision: What happens to ~/.niwa/env and install.sh when niwa init is introduced?

**Context**

Niwa's install.sh creates `~/.niwa/env` containing `export PATH="~/.niwa/bin:$PATH"` and appends `. "$HOME/.niwa/env"` to shell rc files (.bashrc, .zshenv). The env file is overwritten on each install run; the source line is appended idempotently.

With the introduction of `niwa init <shell>` (an eval-init pattern producing shell functions, completions, and PATH setup), the relationship between the env file, install.sh, and the new init command needs to be defined. The key constraint is the chicken-and-egg problem: running `niwa init bash` requires niwa to be on PATH, but `niwa init` is what sets up PATH.

**Assumptions**

- The number of existing niwa users is very small (early-stage project). If wrong: the migration path matters more and a longer deprecation period is needed.
- install.sh remains the primary installation method. If wrong: other installers would need their own shell config logic.
- `niwa init <shell>` will include PATH setup in its output, making the env file's PATH export redundant once init runs.

**Chosen: Env file delegates to niwa init (Alternative 3)**

The env file becomes a stable entrypoint that bootstraps PATH and delegates to `niwa init`. install.sh updates `~/.niwa/env` on each install to contain:

```sh
# niwa shell configuration
export PATH="$HOME/.niwa/bin:$PATH"
if command -v niwa >/dev/null 2>&1; then
  eval "$(niwa init auto 2>/dev/null)"
fi
```

Existing users keep their `. "$HOME/.niwa/env"` line in rc files unchanged. The env file is already overwritten on each install, so the delegation happens automatically when they upgrade. `niwa init auto` detects the running shell (via $ZSH_VERSION, $BASH_VERSION, etc.) so the env file doesn't need shell-specific logic.

For fresh installs, install.sh behaves the same as today: creates the env file, appends the source line. The only change is the env file content.

The PATH export in the env file serves as a safety net: even if `niwa init` fails or isn't available yet (binary exists but doesn't have the init subcommand), PATH is still set.

**Rationale**

This is the only alternative where existing users' rc files require zero changes. Their `. "$HOME/.niwa/env"` line continues to work, and the env file's overwrite-on-install behavior (currently a limitation) becomes the upgrade mechanism. The approach preserves a single shell integration entrypoint, avoids the two-lines-in-rc-file awkwardness of Alternative 1, and doesn't require the risky find-and-replace behavior of Alternative 4.

The `command -v` guard and stderr suppression make the delegation graceful: if niwa doesn't support init yet (older binary), or if the binary is missing, the env file still sets PATH correctly. This makes the upgrade path safe for any binary version.

**Alternatives Considered**

- **Env file as bootstrap, eval line added alongside**: install.sh would append a second line (`eval "$(niwa init bash)"`) to rc files alongside the existing source line. Rejected because two shell integration lines is confusing, splits the source of truth between the env file (PATH) and the eval line (functions/completions), and requires install.sh to manage shell-specific eval lines in addition to the generic source line.

- **Eval line with absolute path, env file removed**: Drop the env file; install.sh writes `eval "$("$HOME/.niwa/bin/niwa" init bash)"` directly to rc files. This is the cleanest end state and would be preferred for a greenfield project. Rejected because it leaves existing users with a dead source line (pointing to a missing env file) and requires either tolerating that dead line or implementing find-and-replace logic in install.sh.

- **Replace source line with eval line on upgrade**: install.sh would find and replace the old source line in rc files with the new eval line. Rejected because modifying lines in user rc files is fragile (the line may have been edited, commented out, or moved) and sed-based replacement in shell config files is a common source of installer bugs.

**Consequences**

- install.sh changes are minimal: only the `cat > "$ENV_FILE"` heredoc content changes
- The env file gains a dependency on `niwa init auto` existing as a subcommand; the `command -v` guard makes this safe to deploy before the init subcommand ships
- `niwa init auto` must detect the shell from environment variables, not from an explicit argument, since the env file can't know which shell will source it
- Future install.sh changes can add more setup to the env file without touching rc files
- The env file's PATH export is technically redundant once `niwa init` runs (init also sets PATH), but the duplication is harmless and provides the safety net
<!-- decision:end -->
