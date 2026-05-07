# Lead: Shell wrapper coverage

## Findings

### The wrapper is a `case` whitelist, not "every invocation"

The shell wrapper template lives in `internal/cli/shell_init.go:37-72`. It defines a single shell function `niwa()` that explicitly whitelists which subcommands trigger the response-file dance. From `shell_init.go:52-71`:

```sh
niwa() {
    case "$1" in
        create|go|init)
            __niwa_cd_wrap "$@"
            ;;
        session)
            case "$2" in
                create)
                    __niwa_cd_wrap "$@"
                    ;;
                *)
                    command niwa "$@"
                    ;;
            esac
            ;;
        *)
            command niwa "$@"
            ;;
    esac
}
```

So today, `__niwa_cd_wrap` (the function that creates the temp file, exports `NIWA_RESPONSE_FILE`, runs niwa, reads back the path, and `cd`s) only runs for:

- `niwa create`
- `niwa go`
- `niwa init`
- `niwa session create`

For every other subcommand — including `niwa destroy` — the wrapper calls `command niwa "$@"` directly. `NIWA_RESPONSE_FILE` is never set, the CLI's `writeLandingPath` becomes a no-op (`landing.go:43-51`), and no `cd` happens after the binary exits. **`niwa destroy` will not auto-cd today even if the destroy code calls `writeLandingPath`.**

### The `__niwa_cd_wrap` helper itself (shell_init.go:39-50)

```sh
__niwa_cd_wrap() {
    local __niwa_tmp __niwa_dir __niwa_rc
    __niwa_tmp=$(mktemp) || { command niwa "$@"; return $?; }
    NIWA_RESPONSE_FILE="$__niwa_tmp" command niwa "$@"
    __niwa_rc=$?
    __niwa_dir=$(cat "$__niwa_tmp" 2>/dev/null)
    rm -f "$__niwa_tmp"
    if [ $__niwa_rc -eq 0 ] && [ -n "$__niwa_dir" ] && [ -d "$__niwa_dir" ]; then
        builtin cd "$__niwa_dir" || return
    fi
    return $__niwa_rc
}
```

Three guards on the `cd`: exit code zero, non-empty file content, and the directory must currently exist. So if niwa exits non-zero, or doesn't write to the response file, no `cd` happens — verified in `landing_test.go:69-75` (`TestWriteLandingPath_ResponseFileAbsent_IsNoOp`).

### CLI side of the protocol

- `internal/cli/landing.go:13` defines the env var name `NIWA_RESPONSE_FILE`.
- `internal/cli/landing.go:24-27` (`captureNiwaResponseFile`) reads it into a package-level cache and unsets the env var so subprocesses don't inherit it. Called from the root command's `PersistentPreRunE` (`root.go:32` references this).
- `internal/cli/landing.go:43-51` (`writeLandingPath`) writes `path + "\n"` to the cached response file path, after running it through `validateResponseFilePath` (must be absolute, must live under `$TMPDIR` or `/tmp`).
- Current callers: `create.go:186`, `go.go:101`, `go.go:276`, `init.go:357`, `session_lifecycle_cmd.go:81`.
- `destroy.go` does **not** call `writeLandingPath` today. `runDestroy` only prints `"Destroyed instance: %s\n"` to stdout (`destroy.go:78`).

### `hint.go`

`internal/cli/hint.go:12-18`: `hintShellInit` prints a one-time hint when `_NIWA_SHELL_INIT` is unset (the wrapper exports `_NIWA_SHELL_INIT=1` at `shell_init.go:37`). Called from create, go, session create, init. The hint message is "shell integration not detected. For auto-cd and completions, run: niwa shell-init install". Tested in `hint_test.go`.

### Empty-response-file behavior

The wrapper treats an empty file as "no cd": `__niwa_dir=$(cat ...)` returns the empty string, the `[ -n "$__niwa_dir" ]` guard fails, the wrapper skips `cd` and just returns the niwa exit code. So if destroy chooses *not* to write a landing path (e.g. when destroying a non-enclosing instance from a still-existing cwd), the wrapper does the right thing automatically. Tested implicitly by `landing_test.go:69-75`.

### Deleted-cwd handling

The wrapper's `cd` runs synchronously inside the same shell function call before `return $__niwa_rc`. So before control returns to the user prompt, `builtin cd "$__niwa_dir"` has already moved the shell to the landing dir. There is **no race** with the user's next command — by the time the prompt redraws, cwd is already a valid directory.

One subtlety: `mktemp` (called at the start of `__niwa_cd_wrap`) and `cat`/`rm -f` all run after niwa exits, but those tools don't depend on cwd being a valid directory — they take absolute paths (mktemp creates in `$TMPDIR` or `/tmp`, `cat` and `rm` get the absolute temp file path). So even if niwa just deleted cwd, the cleanup still works.

The single concern: the `[ -d "$__niwa_dir" ]` guard runs before `cd`. If niwa wrote a path to the response file but the path doesn't exist (defensive bug, or a race), the wrapper silently skips `cd` and the user is stranded in a deleted directory. For destroy, the landing path will always be the workspace root or workspace parent (an existing directory), so this guard should always pass in normal operation. Worth verifying that destroy writes the path *after* a successful directory removal but to a parent that still exists.

### Doc references for "cd-eligible subcommands"

There is a list, but it predates destroy:

- `DESIGN-shell-integration.md:101`: "cd-eligible subcommands (initially `create` and `go`) print the target directory…"
- `DESIGN-shell-integration.md:106`: "A `niwa()` wrapper function that intercepts cd-eligible subcommands…"
- `DESIGN-shell-navigation-protocol.md:32`: "`niwa create` and `niwa go` are 'cd-eligible' commands…"
- `DESIGN-shell-navigation-protocol.md:176, 224`: same restriction.
- `PRD-shell-integration.md:109, 113, 187`: enumerates `create` and `go` only.

The shell-integration design did not consider destroy. The current wrapper has been extended past those docs (init, session create) without doc updates, which is a precedent: the wrapper is the source of truth, the design docs are stale.

### What's tested today

- `shell_init_test.go:20-33, 44-58`: golden-string assertions against the wrapper template — they pin the literal string `"create|go|init)"` plus `mktemp`, `NIWA_RESPONSE_FILE="$__niwa_tmp"`, `builtin cd`, `rm -f`.
- `shell_init_test.go:64-113`: structural assertions on the protocol (mktemp fallback, exit-code capture order, default branch must not contain `mktemp`/`NIWA_RESPONSE_FILE`).
- `landing_test.go`: covers `writeLandingPath` end-to-end including TMPDIR validation, traversal rejection, env unset.
- `go_test.go:81-98`: integration pattern — `runGoTest` wires a temp response file and reads back the landing path. Reusable shape for destroy tests.
- `init_rewire_test.go:471-548`: similar pattern with `t.Setenv("NIWA_RESPONSE_FILE", ...)`.

## Implications

**Destroy needs a wrapper change.** It is not "free" — adding a `writeLandingPath` call to `runDestroy` alone will not produce auto-cd, because the wrapper's `case "$1"` falls through to `command niwa "$@"` and never sets `NIWA_RESPONSE_FILE` for destroy.

The change is small and well-scoped:

1. **`internal/cli/shell_init.go:54`** — extend the case label from `create|go|init)` to `create|go|init|destroy)`. One line. The `__niwa_cd_wrap` helper itself doesn't change.

2. **`internal/cli/shell_init_test.go:25` and `:50`** — update the golden `"create|go|init)"` strings to `"create|go|init|destroy)"` (or split the assertion into a per-subcommand check).

3. **`internal/cli/destroy.go`** — add `writeLandingPath(landingPath)` and `hintShellInit(cmd)` calls in the success path of `runDestroy`. Choose the landing path per the three cases described in the lead:
   - destroy from inside an instance → workspace root
   - destroy whole workspace from root with `--force` → workspace parent
   - destroy empty workspace (no `--force`) → workspace parent
   - destroy a non-enclosing instance by name (the existing case) → no landing path written; empty file means no cd, which is correct.

4. **New tests** following the `go_test.go` shape:
   - destroy from inside instance writes workspace-root landing path
   - destroy a named non-enclosing instance writes nothing (empty response file)
   - destroy whole workspace writes parent-dir landing path
   - destroy empty workspace writes parent-dir landing path
   - shell wrapper test: confirm `destroy` is in the cd-eligible case label

5. **Hint coverage**: destroy should call `hintShellInit` so users without the wrapper get the "run niwa shell-init install" nudge, matching create/go/init/session-create behavior.

6. **No change needed to `landing.go`, `hint.go`, or root command initialization.** The protocol primitives already do the right thing.

### Doc updates (low-priority)

`DESIGN-shell-navigation-protocol.md`, `DESIGN-shell-integration.md`, and `PRD-shell-integration.md` all enumerate "create and go" as the cd-eligible set. They are already stale (init, session create were added without updating them). A short note in the destroy design or a status-update commit on these docs would help, but it isn't blocking.

## Surprises

- **The wrapper has already been extended twice past what the docs say** (init at `shell_init.go:54`, session create at `shell_init.go:58-66`) without corresponding updates to `DESIGN-shell-navigation-protocol.md` or `PRD-shell-integration.md`. So adding destroy is a well-trodden path — not a novel design move.
- **The `session create` branch uses a nested `case "$2" in`** rather than something like `session\ create)`. If destroy ever grows subcommands (e.g. `niwa destroy --picker` interactivity that doesn't always cd), the same nested pattern is available.
- **`writeLandingPath` is a strict no-op when the env var is absent** (`landing.go:44, 50`) — there is no fallback to stdout despite what `DESIGN-shell-navigation-protocol.md:122-124` and `:286` describe. The design doc's "When absent, write landing path to stdout (backward compat for scripts)" was not implemented in the final code. So scripts that try `dir=$(niwa destroy ...)` to capture the landing path will not work; only the wrapper path works. Same constraint applies to create/go today.
- **The `_NIWA_SHELL_INIT=1` export at `shell_init.go:37` is set unconditionally** when the env file is sourced, regardless of whether the wrapper case actually fires. So `_NIWA_SHELL_INIT` only proves the wrapper is loaded — it doesn't tell the CLI which subcommands are wrapped. This is fine today but means we can't gate destroy behavior on "the wrapper supports me" without a version bump. The natural mitigation: have destroy emit the path-info on stderr too (already non-protocol; user can see it), and let the cd-on-success layer be best-effort.

## Open Questions

1. **Deleted-cwd safety in shell startup hooks**: the wrapper calls `builtin cd` synchronously before returning, so the prompt never sees a deleted cwd. But shell startup hooks (`PROMPT_COMMAND`, `chpwd`) and prompt frameworks (starship, powerlevel10k) often probe cwd before the wrapper's `cd` would run if they execute *between* niwa exiting and the wrapper's `cd`. In practice, `__niwa_cd_wrap` is a single function call so nothing else interleaves. This should be safe but is worth a manual smoke test on at least bash + zsh.

2. **What if the chosen landing path no longer exists?** The wrapper's `[ -d "$__niwa_dir" ]` guard skips `cd` silently. For destroy this would be a bug (we just told the user we destroyed something but they're stranded). Need to verify destroy always picks an existing parent. The workspace-self-destroy case is the riskiest — we need to write the path *before* the final `rm -rf` of the workspace dir, which means writing the parent and then deleting. Any path order bug shows up here.

3. **Scope of "destroy a non-enclosing instance"**: when the user runs `niwa destroy other-instance` from outside any instance (or from a different instance), should we still write a landing path? The lead says no — only "destroy enclosing dir" cases emit a landing path. The wrapper handles empty-file correctly, so destroy can simply skip `writeLandingPath` in the non-enclosing case.

4. **Should we update the design doc enumeration of cd-eligible commands?** init and session-create were added without doc updates, so there's precedent for not updating. Probably best to update them once with destroy to bring them current.

## Summary

`niwa destroy` does not work for free — the shell wrapper at `internal/cli/shell_init.go:52-71` whitelists subcommands via `case "$1"` and currently covers only `create|go|init|session create`, so destroy's `writeLandingPath` would never receive a `NIWA_RESPONSE_FILE` to write into. The fix is a one-line wrapper change plus golden-string test updates and matches the precedent already set by `init` and `session create` (both added past the design docs). Deleted-cwd handling is safe because the wrapper's `builtin cd` runs synchronously before returning to the prompt and doesn't depend on cwd being valid; the biggest open question is making sure destroy chooses a still-existing parent path and writes it *before* deleting the directory the user is currently in.
