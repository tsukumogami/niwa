# Shell Integration: User Stories and Acceptance Criteria

Extracted from DESIGN-shell-integration.md (Decisions 1-6) and supporting
decision reports (Decisions 4, 5, 6).

---

## Journey 1: First-Time Setup

### 1.1 Install via install.sh (default path)

**User story**: As a new user running install.sh, I get shell integration
automatically so that `niwa create` lands me in the workspace without extra
configuration.

**Acceptance criteria**:
- install.sh writes `~/.niwa/env` containing PATH export AND `eval "$(niwa shell-init auto 2>/dev/null)"` guarded by `command -v niwa`.
- The source line `. "$HOME/.niwa/env"` is added to the user's shell rc file (.bashrc, .zshrc) if not already present.
- After opening a new shell, `type niwa` reports it as a function (not just a binary).
- `_NIWA_SHELL_INIT=1` is set in the shell environment.
- Tab completions for niwa subcommands work.

**Edge cases**:
- niwa binary not yet on PATH when env file is sourced (pre-install or broken PATH): the `command -v` guard skips the eval, PATH export still applies, no error output.
- Shell rc file already contains the source line: install.sh does not add a duplicate.
- User's shell is fish or another unsupported shell: `niwa shell-init auto` produces empty output, no error. PATH still works.

### 1.2 Install via install.sh with --no-shell-init

**User story**: As a CI operator or minimalist user, I can install niwa without
the shell function wrapper so my environment stays predictable.

**Acceptance criteria**:
- `install.sh --no-shell-init` writes `~/.niwa/env` containing ONLY the PATH export (no eval delegation).
- The source line is still added to rc files (PATH is still needed).
- After opening a new shell, `type niwa` reports it as an external command (not a function).
- `_NIWA_SHELL_INIT` is NOT set.
- `niwa create` still works but does not auto-cd (user stays in original directory).

**Edge cases**:
- Combining `--no-shell-init` with `--no-modify-path`: env file has PATH export but rc files are untouched. User must source manually.

### 1.3 Install via tsuku / go install / manual binary

**User story**: As a user who installed niwa through tsuku or another method, I
can enable shell integration with a single command.

**Acceptance criteria**:
- `niwa shell-init install` creates `~/.niwa/env` if it doesn't exist, writes PATH export + delegation block.
- If the source line is missing from rc files, `niwa shell-init install` adds it.
- After opening a new shell, shell integration is active (`_NIWA_SHELL_INIT=1`, niwa is a function).

**Edge cases**:
- `~/.niwa/env` already exists with PATH-only content: install adds the delegation block without duplicating the PATH export.
- `~/.niwa/env` already has delegation block: command is idempotent, no duplicate.
- User has no .bashrc or .zshrc: command should create the file or report an actionable error.

### 1.4 Upgrade from pre-shell-integration version

**User story**: As an existing niwa user, I get shell integration automatically
when I upgrade, without changing my rc files.

**Acceptance criteria**:
- install.sh overwrites `~/.niwa/env` on each install with the new content (PATH + delegation).
- Existing `. "$HOME/.niwa/env"` line in rc files picks up the new env file content on next shell.
- No manual intervention required.

**Edge cases**:
- User had customized `~/.niwa/env` with their own additions: overwrite destroys customizations. (Documented consequence, not mitigated.)

---

## Journey 2: Creating a Workspace

### 2.1 Create and land at instance root

**User story**: As a developer, when I run `niwa create --from <template>`, my
shell automatically changes to the new workspace directory.

**Acceptance criteria**:
- With shell integration active: after `niwa create --from example`, the shell's cwd is `~/.niwa/instances/example` (or the actual instance path).
- Human-readable output ("Cloning repo-a...", "Created instance: ...") appears on stderr, visible to the user.
- Stdout contains only the bare absolute directory path.
- Exit code is 0 on success.

**Edge cases**:
- Create fails (invalid template, network error): exit code is non-zero, stdout is empty, shell does NOT cd. Error message on stderr.
- Create succeeds but the instance directory somehow doesn't exist (race, permissions): shell function's `-d` check prevents cd. User sees the binary's exit code.
- Shell integration not active: binary prints path to stdout (no function to intercept). Output looks like a bare path to the terminal. Hint fires on stderr.

### 2.2 Create and land in a specific repo

**User story**: As a developer, I can use `--cd <repo>` to land directly in a
repo within the new workspace after creation.

**Acceptance criteria**:
- `niwa create --from example --cd api-service` changes cwd to `~/.niwa/instances/example/<group>/api-service`.
- The binary resolves the repo name against the classified repo list, determining the group automatically.
- Stdout contains the resolved repo directory path.

**Edge cases**:
- `--cd nonexistent`: binary errors with a clear message on stderr, non-zero exit, empty stdout. No cd occurs.
- Repo name is ambiguous (same name in multiple groups): binary errors with a message suggesting the qualified form (e.g., "did you mean public/api-service or private/api-service?").
- `--cd` without `--from`: should this be an error? (Needs clarification -- create requires a source.)

### 2.3 Create without shell integration

**User story**: As a user without shell integration, I can still use
`niwa create` and get a useful hint about enabling auto-cd.

**Acceptance criteria**:
- `niwa create --from example` prints the instance path to stdout and human messages to stderr.
- A hint appears on stderr: "hint: shell integration not detected. For auto-cd and completions, run: niwa shell-init install"
- The hint fires ONLY on cd-eligible commands (create, go), not on every niwa invocation.
- The hint fires ONLY when `_NIWA_SHELL_INIT` is unset.

**Edge cases**:
- User pipes stdout to another command (`niwa create --from x | xargs ls`): hint on stderr doesn't interfere with piped output.
- User sets `_NIWA_SHELL_INIT=1` manually without the wrapper: hint is suppressed but auto-cd doesn't happen. (Acceptable -- user explicitly opted out of the hint.)

---

## Journey 3: Navigating Workspaces

### 3.1 Go to workspace root (no arguments)

**User story**: As a developer working inside a workspace, I can run `niwa go`
to return to the workspace root.

**Acceptance criteria**:
- `niwa go` (no args) resolves the current workspace from cwd and prints the instance root to stdout.
- Shell function cds to that path.
- If not inside any workspace: error on stderr, non-zero exit, empty stdout.

### 3.2 Go to a named workspace

**User story**: As a developer, I can run `niwa go <name>` to jump to any
workspace by name.

**Acceptance criteria**:
- `niwa go example` resolves the workspace via the global registry and prints the instance path to stdout.
- If multiple instances exist for the same workspace (e.g., `example`, `example-2`): prints the most recently used instance. If no usage data, prints the first (original) instance.
- If workspace name not found: error on stderr, non-zero exit, empty stdout.

**Edge cases**:
- Workspace directory was deleted but registry entry remains: the `-d` check in the shell function prevents cd. Binary should also validate and report.

### 3.3 Go to a repo within a workspace

**User story**: As a developer, I can run `niwa go <workspace> <repo>` to jump
directly to a repo directory.

**Acceptance criteria**:
- `niwa go example api-service` resolves the repo within the workspace instance and prints the full repo path to stdout.
- Shell function cds there.
- If repo not found within workspace: error on stderr, non-zero exit.

**Edge cases**:
- Path traversal attempt (`niwa go example ../../etc`): binary validates that the resolved path falls within the instance root using `filepath.Rel` (logical-path, pre-symlink). Returns error for paths outside the instance.
- Repo name is ambiguous across groups: error with suggestion of qualified form.
- Symlinked repos: allowed, as long as the logical path is within the instance root.

---

## Journey 4: Managing Shell Integration

### 4.1 Check status

**User story**: As a user, I can check whether shell integration is active and
configured.

**Acceptance criteria**:
- `niwa shell-init status` reports two pieces of information:
  1. Whether `_NIWA_SHELL_INIT` is set in the current shell (wrapper loaded NOW).
  2. Whether `~/.niwa/env` contains the delegation block (will load on NEXT shell).
- Output is human-readable and unambiguous.

**Edge cases**:
- Wrapper loaded but env file missing (user sourced manually without env file): status reports "active in current shell, not persisted."
- Env file has delegation but wrapper not loaded (new env file, old shell): status reports "configured but not active in this shell. Open a new terminal."
- Neither: "Shell integration is not installed."

### 4.2 Install shell integration post-hoc

**User story**: As a user who initially skipped shell integration, I can enable
it later without re-running install.sh.

**Acceptance criteria**:
- `niwa shell-init install` writes delegation block to `~/.niwa/env`.
- Adds source line to rc files if missing.
- Reports success with instruction to open a new shell.

### 4.3 Uninstall shell integration

**User story**: As a user who wants to remove shell integration, I can disable
it cleanly.

**Acceptance criteria**:
- `niwa shell-init uninstall` rewrites `~/.niwa/env` to contain only the PATH export.
- The source line in rc files is NOT removed (still needed for PATH).
- After opening a new shell, `type niwa` reports it as an external command.
- `_NIWA_SHELL_INIT` is not set.

**Edge cases**:
- `~/.niwa/env` doesn't exist: command reports "nothing to uninstall" or creates a PATH-only env file.
- User has the eval line directly in their rc file (not via env file): uninstall doesn't catch this. Should status warn about it?

### 4.4 Generate shell code for manual setup

**User story**: As an advanced user, I can run `niwa shell-init bash` or
`niwa shell-init zsh` to see the exact shell code being evaluated.

**Acceptance criteria**:
- `niwa shell-init bash` prints valid bash code to stdout (wrapper function + completions).
- `niwa shell-init zsh` prints valid zsh code to stdout.
- `niwa shell-init auto` detects shell from `$ZSH_VERSION` / `$BASH_VERSION` and emits the right variant.
- Output includes `export _NIWA_SHELL_INIT=1`.

**Edge cases**:
- `niwa shell-init auto` in an unrecognized shell (no ZSH_VERSION, no BASH_VERSION): produces empty output, exits successfully. No error.
- Running `niwa shell-init bash` from zsh (or vice versa): works fine, user gets the requested shell's code regardless of current shell.

---

## Journey 5: Completions

### 5.1 Tab completions work out of the box

**User story**: As a user with shell integration, I get tab completions for niwa
subcommands and flags without additional configuration.

**Acceptance criteria**:
- `niwa shell-init` output includes cobra-generated completion registration.
- A single `eval` line in the env file provides both the wrapper function AND completions.
- Completions work for subcommands (`niwa cr<TAB>` -> `create`), flags (`niwa create --f<TAB>` -> `--from`), and the `go` command's workspace names.

**Edge cases**:
- Completions for `niwa go <TAB>`: should complete workspace names from the global registry. (Requires cobra custom completion function.)
- Completions for `niwa go <workspace> <TAB>`: should complete repo names within that workspace.
- Completions for `niwa create --cd <TAB>`: cannot complete repo names because they don't exist until after creation. Should complete nothing or indicate this.

---

## Journey 6: Error Cases and Hints

### 6.1 Shell startup hang

**User story**: As a user, if the niwa binary hangs during shell startup, I need
a way to recover.

**Acceptance criteria**:
- Recovery documented: `bash --norc` or `zsh -f` opens a shell without rc files.
- The `2>/dev/null` on the eval line suppresses error output but does NOT prevent hangs.
- No timeout wrapper is added (matches ecosystem practice).

**Edge cases**:
- Binary segfaults during shell-init: `2>/dev/null` suppresses stderr, eval gets empty string, shell starts normally. PATH still set.

### 6.2 Stdout protocol violations

**User story**: As a user, if a bug causes the binary to print unexpected
content to stdout, the shell function should not cd to a garbage path.

**Acceptance criteria**:
- Shell function checks: exit code 0 AND stdout non-empty AND path is a directory (`-d` test).
- Multi-line stdout: only first line would be used for cd (or entire captured string fails `-d`).
- Path with spaces or special characters: double-quoting in `builtin cd "$__niwa_dir"` prevents word splitting and glob expansion.

### 6.3 Binary without shell-init support (version mismatch)

**User story**: As a user with a new env file but old binary (doesn't have
shell-init subcommand), shell startup should not break.

**Acceptance criteria**:
- `command -v niwa` succeeds (binary exists), but `niwa shell-init auto` fails.
- `2>/dev/null` suppresses the error. Eval gets empty output. Shell starts normally.
- PATH is set correctly from the env file's export line.
- No shell function is loaded (niwa works as a bare binary).

### 6.4 Concurrent shells

**User story**: As a user with multiple terminal sessions, shell integration
should work correctly in all of them without interference.

**Acceptance criteria**:
- The stdout protocol is per-process (stdout capture in shell function is local to that invocation). No shared state.
- No temp files, no lock files, no cross-shell communication.
- Two terminals can run `niwa create` simultaneously without conflict.

---

## Summary of Key Testable Properties

| Property | Verification method |
|---|---|
| Shell function intercepts only create and go | `type niwa` shows function; other commands pass to binary |
| Stdout protocol: path only on success | Capture stdout of `command niwa create`, verify single absolute path |
| Stderr protocol: human messages | Capture stderr, verify progress/status messages |
| Exit code propagation | Failed create returns non-zero through wrapper |
| `-d` guard prevents bad cd | Print nonexistent path to stdout, verify no cd |
| `--cd` resolves repo within instance | Create with --cd, verify cwd is repo dir not instance root |
| `--cd` with bad repo errors | Create with --cd nonexistent, verify non-zero exit |
| Path traversal blocked in go | `niwa go ws ../../etc` returns error |
| install/uninstall are symmetrical | Install then uninstall, verify env file returns to PATH-only |
| status reports both dimensions | Check loaded vs configured states independently |
| Hint fires only on cd-eligible commands | Run `niwa list` without wrapper, no hint. Run `niwa create`, hint appears |
| auto detects shell correctly | Run in bash, get bash output. Run in zsh, get zsh output |
| Completions included in init output | `niwa shell-init bash` output contains completion registration |
| `--no-shell-init` produces PATH-only env | Inspect env file after install with flag |
| Env file overwrite on upgrade | Run install.sh twice, env file has latest content |
| `command -v` guard safe with old binary | Remove shell-init from binary, source env file, no error |
