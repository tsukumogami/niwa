# Lead: Niwa UX patterns

## Findings

### Command inventory

Inventory of cobra commands declared under
`/home/dgazineu/dev/niwaw/tsuku/tsukumogami-8/public/niwa/internal/cli/`. Sources
located via `grep -n "Use:|Short:" internal/cli/*.go`.

| Command | Short | File | Args | Interaction |
|--------|-------|------|------|-------------|
| `niwa` | Declarative workspace manager for AI-assisted development | `root.go` | — | none (root) |
| `niwa apply [workspace-name]` | Apply workspace configuration | `apply.go` | `MaximumNArgs(1)` | output-only (Reporter, stderr warnings) |
| `niwa create [workspace-name]` | Create a new workspace instance | `create.go` | `MaximumNArgs(1)` | output-only (Reporter); writes landing path |
| `niwa init [name]` | Initialize a new workspace | `init.go` | `MaximumNArgs(1)` | output-only; writes landing path |
| `niwa destroy [instance]` | Destroy a workspace instance | `destroy.go` | `MaximumNArgs(1)` | output-only (no prompt today) |
| `niwa reset [instance]` | Reset a workspace instance | `reset.go` | `MaximumNArgs(1)` | output-only (no prompt today) |
| `niwa go [target] [session-id]` | Navigate to a workspace, repo, or session worktree | `go.go` | `MaximumNArgs(2)` | output-only; writes landing path |
| `niwa status [instance]` | Show workspace instance status | `status.go` | `MaximumNArgs(1)` | output-only (tables) |
| `niwa version` | Print version information | `version.go` | — | output-only |
| `niwa shell-init` (+ `bash`/`zsh`/`auto`/`install`/`uninstall`/`status`) | Generate shell integration | `shell_init.go` | varies | output-only |
| `niwa config set global <repo>` | Register a global config repo | `config_set.go` | `ExactArgs(1)` | output-only (clones via Reporter) |
| `niwa config unset global` | Unregister the global config repo | `config_unset.go` | `NoArgs` | destructive: `os.RemoveAll(globalConfigDir)` with NO prompt and NO `--force` |
| `niwa session list` | List sessions | `session.go` | — | output-only |
| `niwa session register` | Register this session with the mesh | `session_register.go` | — | output-only |
| `niwa session create <repo> <purpose>` | Create a worktree session | `session_lifecycle_cmd.go` | `ExactArgs(2)` | output-only; writes landing path |
| `niwa session destroy <session-id>` | Destroy a session and worktree | `session_lifecycle_cmd.go` | `ExactArgs(1)` | destructive; `--force` only for unmerged-branch override; no prompt |
| `niwa task list` / `niwa task show <id>` | Inspect tasks | `task.go` | varies | output-only |
| `niwa mesh watch` | Run the mesh watch daemon | `mesh_watch.go` | — | long-running daemon |
| `niwa mesh list` | List coordinator sessions | `mesh_list.go` | — | output-only |
| `niwa mesh report-progress` | Advance the stall watchdog deadline | `mesh_report_progress.go` | — | output-only |
| `niwa mcp-serve` | Start the niwa stdio MCP server | `mcp_serve.go` | — | server (stdio) |

Key observation: **no command in niwa today uses an interactive prompt or
picker**. Every command takes a positional, a flag, or works from cwd.

### Prompt/confirmation patterns

`grep -rn "survey\.|bufio.NewReader.*Stdin|bubbletea|tea\.New" internal/`
returned no matches. `go.mod`/`go.sum` contain no `survey`, `bubbletea`, or
`charmbracelet` references.

The only "ask" patterns in the tree are the MCP `task.ask` machinery
(`internal/mcp/handlers_task.go:423`, `internal/mcp/watcher.go:208` —
`buildQuestionEvent`/`registerQuestionWaiter`). Those are coordinator-to-worker
JSON-RPC questions, not terminal prompts.

**Implication for destroy:** there is no in-tree precedent for a typed-input
prompt. Destroy's typed-confirmation prompt will be the first one. The rework
needs to bring the prompt scaffolding (TTY check, line read, normalization,
non-TTY refusal) along with it; there is nothing to reuse.

### Destructive operations elsewhere

| Caller | What it destroys | Guard |
|--------|------------------|-------|
| `niwa destroy` | `os.RemoveAll(instanceDir)` via `workspace.DestroyInstance` (`internal/workspace/destroy.go:128`) | `--force` flag; otherwise refuses on uncommitted changes. **No prompt.** |
| `niwa reset` | Same `DestroyInstance` then re-creates | `--force` flag; otherwise refuses on uncommitted changes. **No prompt.** |
| `niwa config unset global` | `os.RemoveAll(globalConfigDir)` (`config_unset.go:48`) | **No guard at all** — runs immediately. |
| `niwa session destroy <id>` | Removes worktree, deletes session branch | `--force` only for the **branch unmerged** check; the worktree itself is always removed. **No prompt.** |
| `niwa apply --force` | Re-materializes config dir from new source URL | `--force` required; refuses by default with multi-line error showing `was:`/`now:` and a numbered remediation list (`apply.go:327`). |
| `niwa apply --allow-dirty` | Bypasses dirty-config refusal | One-shot flag; emits a deprecation warning (`apply.go:126`). |
| `niwa apply --allow-missing-secrets` / `--allow-plaintext-secrets` | Bypasses secret guardrails | One-shot flags; described as "strictly one-shot — no state persistence". |

**Pattern:** niwa's house style is **flag-based opt-in for risky operations,
not interactive confirmation**. The dirty-repo refusal in `destroy`/`reset` is
the closest existing analog — it lists offenders and tells you to use
`--force`. There is exactly zero precedent for an interactive y/n or typed
"yes" prompt in the codebase.

### Error-message style

All errors use `fmt.Errorf` with lowercase verbs and `%w` for wrapping. Hints
appear via `\n` suffixes or multi-line backticks. Examples:

`destroy.go:37` and `reset.go:42`:

```go
return fmt.Errorf("getting working directory: %w", err)
```

`destroy.go:65` (the canonical "needs --force" pattern):

```go
return fmt.Errorf("instance has uncommitted changes in %d repo(s); use --force to override", len(dirty))
```

`go.go:146`/`go.go:150` (multi-line hints):

```go
return "", fmt.Errorf("-r requires being inside a workspace instance\nhint: use \"niwa go -w <workspace> -r %s\" to target a specific workspace", repoName)
```

`apply.go:327` (multi-line guarded refusal — best template for destroy's
"workspace self-destroy without push"):

```go
return fmt.Errorf(`workspace config source changed
  was:  %s
  now:  %s
  The current %s on disk is from the old source. Replacing it will
  discard any uncommitted edits inside.
To proceed:
  1. cd %s && git status   # check for uncommitted work (legacy working tree)
  2. niwa apply --force     # discard and re-materialize from the new source`,
    onDiskURL, registeredURL, configDir, configDir)
```

`init.go` (`InitConflictError`, `internal/workspace/destroy.go:79,84`) shows the
sentinel-with-suggestion pattern — `workspace.InitConflictError{Err, Detail,
Suggestion}` is a structured error type carrying machine-stable Err plus a
human Suggestion string. Used when the failure has a single canonical fix.

Conventions extracted:

- Lowercase verb-first error: `"checking for uncommitted changes: %w"`,
  `"loading global config: %w"`, `"resolving current instance: %w"`.
- Wrap with `%w` when the inner error is meaningful; format with `%s`/`%v`
  when it isn't.
- Hints: append `\nhint: ...` or use a backtick block with numbered
  remediation steps. Both styles coexist.
- "needs --force": phrase as `"<thing>; use --force to override"` (singular
  semicolon, lowercase).
- Pre-flight refusals list offenders to stderr first, then return a single
  fmt.Errorf summarizing the count.
- No `error:` prefix in returned errors — cobra's
  `Execute() → fmt.Fprintln(os.Stderr, err)` adds nothing; the error string
  must read cleanly on its own.

### Reporter abstraction

`internal/workspace/reporter.go` (lines 1–195). API:

```go
NewReporter(w io.Writer) *Reporter            // auto-detect TTY from *os.File
NewReporterWithTTY(w, isTTY bool) *Reporter   // explicit TTY flag

(r) Status(msg string)                        // transient spinner line; no-op when !isTTY
(r) Log(format string, a ...any)              // permanent line; clears spinner first
(r) Warn(format string, a ...any)             // Log with "warning: " prefix
(r) Defer(format string, a ...any)            // queue for FlushDeferred
(r) DeferWarn(format string, a ...any)        // queue with "warning: " prefix
(r) FlushDeferred()                           // print all deferred and clear
(r) Writer() io.Writer                        // io.Writer adapter routed through Log
```

**Used by:**

- `apply.go:119` and `create.go:144` — wired into `workspace.Applier.Reporter`,
  with explicit TTY flag `!noProgress && term.IsTerminal(int(os.Stderr.Fd()))`.
- `init.go:248,551,570` and `config_set.go:70` — passed to `MaterializeFromSource` /
  `EnsureOverlaySnapshot` for clone progress.

**Not used by:** destroy, reset, status, go, session destroy, config unset.
These commands write directly via `cmd.ErrOrStderr()` / `cmd.OutOrStdout()`
and `fmt.Fprintf`/`fmt.Fprintln`.

The Reporter is uniformly bound to `os.Stderr`. Every consumer passes
`os.Stderr` (never stdout) and either auto-detects TTY or honors `--no-progress`.

**Implication for destroy:** the Reporter abstraction is *for clone-style
progress narration*, not for picker UI or prompts. Destroy's picker output
should go to `cmd.ErrOrStderr()` directly (matching the existing destroy code
on lines 61–63 and 71). A Reporter is overkill for a one-shot list-and-prompt;
adopting it just to look uniform would actively confuse the spinner contract
(picker output is not transient progress and shouldn't be cleared on the next
Log call).

### Output destinations (stdout/stderr)

The convention is well-established and consistent:

**Stdout (`cmd.OutOrStdout()` or `os.Stdout`)** — final, machine-parseable
results only:

- `destroy.go:78` — `Destroyed instance: %s` (final summary)
- `reset.go:130` — `Reset instance: %s` (final summary)
- `status.go:161,165,166,188,206…` — the entire status table
- `config_unset.go:39,62` — final state lines
- `session_lifecycle_cmd.go:145` — session-list table

**Stderr (`cmd.ErrOrStderr()` or `os.Stderr`)** — diagnostics, warnings,
progress, hints:

- `destroy.go:61,63,71` — pre-flight dirty-repo list and daemon-stop warning
- `reset.go:66,68,95` — same pattern
- `apply.go:105,119,126,169,179` — warnings, Reporter, errors
- `create.go:75,116,144,176` — same
- `go.go:118,135,153,181,217,221,227,272` — every "go: …" navigation
  acknowledgement is on stderr; stdout is reserved for the (no-op) write
- `init.go:295,310,323,332` — warnings, rebind warning, override note
- `hint.go:16,17` — shell-init hint
- `session_lifecycle_cmd.go:74,76,110,113` — session create/destroy
  acknowledgements all go to stderr

The cleanest illustration is `niwa go`: the success message
("`go: workspace "tsuku"`") goes to `cmd.ErrOrStderr()` and the actual
landing path is **not written to stdout** — it's written to
`NIWA_RESPONSE_FILE` via `writeLandingPath` (`landing.go:43`). Stdout is
deliberately empty so a non-wrapper invocation produces nothing parseable
and the wrapper's response-file channel is the only data plane.

**Implication for destroy:** the picker's instance list, the typed-confirmation
prompt text, and "Destroying instance: …" progress lines all belong on stderr.
The final "Destroyed instance: %s" success line continues to go to stdout
(matching the existing `destroy.go:78`). Under `--force` (or whatever the
non-interactive path becomes), stdout is the sole machine-readable channel.

### Non-TTY behavior

`term.IsTerminal(int(os.Stderr.Fd()))` is checked in exactly two places:
`apply.go:119` and `create.go:144`. Both call sites use the same pattern:

```go
applier.Reporter = workspace.NewReporterWithTTY(os.Stderr, !noProgress && term.IsTerminal(int(os.Stderr.Fd())))
```

The Reporter then makes `Status` a no-op in non-TTY mode (`reporter.go:62`).
Log/Warn still print with no ANSI sequences. Output is degraded gracefully,
never refused.

`--no-progress` is a root-level persistent flag (`root.go:48`) that forces the
non-TTY path even on a real terminal — the flag exists specifically to give
CI/scripts a way to opt out of spinner output without piping.

`stdin` TTY status is **never** checked anywhere in `internal/`. The codebase
has no precedent for "is the user available to answer".

**Implication for destroy** — this is the most consequential gap and the
clearest place destroy must establish a new pattern:

- The picker (workspace root, no name, instances present) must check
  `term.IsTerminal(int(os.Stdin.Fd()))`. When false, refuse with a
  clear-eyed error: something like
  `"multiple instances exist; pass an instance name or run with --force"` —
  matching the "needs --force to override" wording family.
- The typed-confirmation prompt (workspace-self-destroy + non-pushed work)
  must also TTY-check stdin. When non-TTY, refuse with an analogous error
  that names what's wrong and how to bypass: `"workspace has unpushed work
  on N branches; use --force to destroy without confirmation"`.
- Both refusals should list offenders to stderr first (mirroring
  `destroy.go:61–64`), then return the single fmt.Errorf.
- `--no-progress` should NOT suppress the picker — it's about spinner
  animations, not interactivity. The TTY check on stdin is the right gate.

This is the **non-TTY contract** for destroy:

| Scenario | Stdin is TTY | Stdin is not TTY |
|----------|--------------|------------------|
| Workspace root, no name, 0 instances | error: nothing to destroy | error: nothing to destroy |
| Workspace root, no name, 1 instance | destroy that instance (no picker, no prompt) | destroy that instance |
| Workspace root, no name, ≥2 instances | picker | refuse: pass `<name>` or `--force` (force = wipe all? — open question, see below) |
| Inside instance, no name | destroy enclosing instance (after the existing dirty-repo check) | same |
| Workspace-self-destroy, has unpushed work | typed-confirmation prompt | refuse: `; use --force to override` |
| Workspace-self-destroy, fully pushed | proceed (consistent with today's behavior?) | proceed |

### One-time notices

`docs/guides/one-time-notices.md` describes the `DisclosedNotices` mechanism
in `InstanceState`. The keys are **per-instance**, persisted on
`SaveState`, and intended for *informational facts about the workspace
that don't change between runs*. Existing key: `provider-shadow`.

The guide explicitly says (line 36):

> Do not use one-time notices for warnings that reflect the current state of
> the workspace (e.g. drift, missing secrets). Those should appear on every
> run.

A first-time-picker hint or a "destroy is shell-wrapper-aware" hint **does
not fit this mechanism**:

- The notice is keyed to an instance's state file. A picker fires *before*
  any specific instance is selected, and the workspace-root case has no
  instance at all when no instances exist yet.
- Destroy in particular *removes the instance state file*, so the second-time
  experience is exactly the same as the first by definition. There is no
  "remember I already saw this" surface that survives the operation.
- The guide's "describes a configuration fact that doesn't change between
  runs" criterion isn't satisfied — picker availability is a runtime fact,
  not a config fact.

For the shell-wrapper landing-path drop-out (the protocol that gets the user
out of a directory destroy just deleted), the existing `hintShellInit(cmd)`
helper (`hint.go:12`) is the right model. It's stateless: every `niwa create`
and `niwa go` and `niwa session create` calls it; it suppresses itself when
`_NIWA_SHELL_INIT` is set in the environment. Destroy should do the same when
its post-destroy `writeLandingPath` would be useful but the wrapper isn't
loaded.

**Implication for destroy:** **do not** use `DisclosedNotices` for picker or
self-destroy hints. Reuse `hintShellInit(cmd)` after the destroy completes if
landing-path delivery would otherwise no-op silently.

## Implications

What destroy adopts wholesale:

1. **Stderr for all interactive surfaces.** Picker list, prompt text, "y/N"
   echo, progress notes — all to `cmd.ErrOrStderr()`. Stdout reserved for the
   `Destroyed instance: %s` summary line (existing convention preserved).
2. **`fmt.Errorf` with lowercase verb + `%w` + "; use --force to override".**
   The error wording for the new typed-confirmation refusal should mirror
   `destroy.go:65` exactly: list offenders to stderr first, then return
   `fmt.Errorf("workspace has unpushed work in %d repo(s); use --force to override", len(unpushed))`.
3. **`--force` as the bypass.** Both surfaces (picker and typed-confirmation)
   are bypassable with `--force`. Same flag name, no new flags. From the
   workspace root, `--force` becomes "wipe the entire workspace without
   asking" per the design context.
4. **`hintShellInit(cmd)` after success when landing-path drop-out applies.**
   When destroy removed the cwd, write the parent (or workspace root) via
   `writeLandingPath` and call `hintShellInit` so users without the wrapper
   know why their shell didn't move.
5. **No Reporter.** Direct `fmt.Fprintf(cmd.ErrOrStderr(), ...)` matches
   today's destroy and reset and is the right tool for one-shot output.

What destroy must establish (new pattern, no in-tree precedent):

1. **A stdin TTY check.** `term.IsTerminal(int(os.Stdin.Fd()))` gates both
   the picker and the typed-confirmation prompt. This is the first place
   in niwa where stdin-tty-ness matters; the check should live in a small
   helper (e.g. `internal/cli/prompt.go`) so future commands can reuse it.
2. **Picker UI.** No precedent in niwa. Either (a) write to stderr and read
   a number/name from stdin (minimal, no deps, uses `bufio.NewReader`), or
   (b) bring in a small picker component. Option (a) is consistent with the
   project's "no external linters / minimal deps" ethos.
3. **Typed-confirmation prompt.** Read a line, normalize, compare to the
   expected token (likely the workspace name). On mismatch, abort with a
   one-line error.
4. **Non-TTY contract documented in the destroy command's `Long:`.** The
   table above belongs in user-facing help text so CI users know exactly
   what `--force` means in each branch.

What destroy ignores:

- One-time-notice mechanism — wrong tool, see above.
- Reporter spinner — wrong granularity, will fight with the picker.

## Surprises

1. **Niwa has zero interactive prompts today.** Every "destructive" command
   uses flag-only guards. Destroy's picker + typed-confirmation will be the
   first interactive surfaces in the binary. This is a small but real
   precedent shift; the rework should call it out in the design doc.
2. **`niwa config unset global` has no guard at all** — it `os.RemoveAll`s
   the global config clone immediately. This is a latent UX bug
   (`config_unset.go:48`), unrelated to destroy but worth flagging.
3. **`niwa session destroy` always removes the worktree** — the `--force`
   flag *only* governs branch deletion, not worktree removal
   (`session_lifecycle_cmd.go:33–47`). The destroy rework's `--force` semantic
   is the inverse: `--force` makes destruction *broader* (wipe the whole
   workspace from the root), not narrower. The two `--force` flags will read
   differently across the two commands; that's tolerable but worth noting.
4. **Destroy doesn't write a landing path today.** It deletes the cwd-or-
   named-instance and prints a summary, but if the user was *in* the instance
   it just deleted, the shell wrapper has no signal to cd them out. The
   shell-wrapper-aware drop-out described in the exploration context is a
   genuine gap, not a polish item.

## Open Questions

1. **What does `--force` mean from the workspace root with multiple instances
   present?** The design context says "wipe the entire workspace under
   `--force`". Confirm: does that mean RemoveAll the workspace root? Or
   RemoveAll every instance subdirectory but keep `.niwa/` and the registry
   entry? The semantics need a decision before error wording can lock in.
2. **What does the typed-confirmation prompt ask the user to type?** The
   workspace name is the obvious answer (matches industry convention for
   destructive prompts), but niwa's effective workspace name can differ
   from `cfg.Workspace.Name` after `niwa init <name>` overrides — see
   `effective_name.go` and `resolveEffectiveWorkspaceName`. The prompt
   should use the override-aware name; this affects help-text wording.
3. **Should `--force` skip both gates or each independently?** Today
   `destroy --force` skips the dirty-repo gate. The new prompt is a
   *second* gate. Single-flag-skips-both is simpler; two flags
   (`--force`, `--yes`?) is more granular. Recommend single flag for
   consistency with reset and apply.
4. **Should the picker degrade to the first-instance default in non-TTY
   mode, refuse, or pick by some other rule?** The non-TTY-contract table
   above proposes "refuse and tell the user to pass a name or `--force`",
   but a "default to the only-or-most-recent instance" path is also
   defensible. Needs a design call.

## Summary

Niwa today is flag-driven and non-interactive: every destructive command uses
`--force` plus a pre-flight refusal that lists offenders to stderr, and there
are zero in-tree prompt patterns to copy from (no survey, no bubbletea, no
bufio stdin readers). Destroy will adopt the existing conventions wholesale
— stderr for interactive output, stdout for the final summary, `fmt.Errorf`
with lowercase verbs and `; use --force to override` wording, `hintShellInit`
after success — and establish two genuinely new patterns the codebase doesn't
have yet: a `term.IsTerminal(os.Stdin.Fd())` gate and a small picker/prompt
helper for the workspace-root and self-destroy cases. The non-TTY contract
(picker refuses, typed-confirmation refuses, both bypassable with `--force`)
must be documented in `Long:` help text so CI consumers have a stable surface.
