# Lead: niwa CLI tone audit

## Findings

### Output formatting

**Header case: ALL CAPS, space-padded fixed-width columns.** Every tabular
command in niwa today uses the same pattern: a `fmt.Fprintf` format string
with `%-Ns` directives, ALL-CAPS headers, two leading spaces, no separator
character between columns (just whitespace padding). No tabwriter. No box
characters. No alternating row colors.

Concrete examples:

- `niwa session list` (lifecycle) — `internal/cli/session_lifecycle_cmd.go:150`:
  ```go
  fmt.Fprintf(out, "  %-8s %-12s %-10s %-20s %s\n",
      "ID", "REPO", "STATUS", "CREATED", "PURPOSE")
  ```
  Two leading spaces, `ID` 8w, `REPO` 12w, `STATUS` 10w, `CREATED` 20w, `PURPOSE`
  unbounded final column.

- `niwa mesh list` — `internal/cli/mesh_list.go:68`:
  ```go
  fmt.Fprintf(out, "  %-16s %-8s %-10s %-14s %s\n",
      "ROLE", "PID", "STATUS", "LAST-SEEN", "PENDING")
  ```
  Same shape: two-space leader, fixed widths, last column unbounded. Note
  hyphen-separated multi-word header (`LAST-SEEN`).

- `niwa task list` — `internal/cli/task.go:213`:
  ```go
  header := fmt.Sprintf("  %-10s %-14s %-10s %-8s %-8s %-14s %s",
      "TASK", "TARGET", "STATE", "RESTART", "AGE", "DELEGATOR", "BODY")
  ```
  Same shape, seven columns. `task.go:212` comment is explicit:
  > "columns aligned to the existing `niwa status` convention (two leading
  > spaces, fixed-width columns, body summary in the final variable-width
  > column)."

- `niwa status` summary — `internal/cli/status.go:188`:
  ```go
  fmt.Fprintf(cmd.OutOrStdout(), "  %-12s %d repos   %d %s   applied %s\n",
      status.Name, repoCount, status.DriftCount, driftLabel, appliedAgo)
  ```
  Same two-space leader, but `status` summary view is *not* a header-row
  table. It's instance-name on the left + multi-clause descriptive line.
  This is the lone outlier — no `INSTANCE`/`REPOS`/`DRIFT` header above.

- `niwa status --audit-secrets` — `internal/cli/status_audit.go:213` —
  uses dynamic-width column computation rather than fixed widths:
  ```go
  format := fmt.Sprintf("%%-%ds  %%-%ds  %%-%ds  %%s\n", keyWidth, classWidth, tableWidth)
  fmt.Fprintf(out, format, "KEY", "CLASSIFICATION", "TABLE", "SHADOWED")
  ```
  Headers `KEY / CLASSIFICATION / TABLE / SHADOWED`, all caps, two-space
  separator (note: TWO spaces between columns here, not one). No leading
  indent.

- `niwa status --audit-auth` — `internal/cli/status_audit_auth.go:125` —
  also no leading indent, fixed widths derived from the longest known
  values: `KIND / PROJECT-UUID / SOURCE / FALLBACK`. Empty `Fallback`
  renders as the em-dash `—`.

**Sort order conventions:**

- `niwa mesh list` — sorted by `Role` ascending (mesh_list.go:55).
- `niwa session list --status` — sorted by `SessionID` ascending (session_lifecycle_cmd.go:141).
- `niwa task list` — sorted by `sent_at` newest-first, ties broken by `taskID` (task.go:117).
- `niwa status` repos — sorted by `Name` ascending (status.go:241).
- `niwa status --audit-secrets` — repo names sorted alphabetically (status_audit.go:164).
- `niwa status --audit-auth` — `KIND` ascending, then `PROJECT-UUID` ascending (status_audit_auth.go:91).

So: niwa picks a domain-natural sort key per command (role, session ID,
recency, name) — there is no global rule. The default fallback when
nothing more meaningful is available is alphabetical ascending.

**Empty-state handling:** mesh list prints `no coordinator sessions registered`
(mesh_list.go:65, lowercase, no period); status_check_vault prints
`no vault providers declared; nothing to check.` (lowercase, period because
two-clause). status summary prints `No instances found.` (capitalized,
period). status_audit prints `No *.secrets entries found.` (capitalized,
period). So: mixed. The lowercase form is reserved for listing-style
commands ("no rows"); the capitalized form is for "nothing to do" status
prose.

**Truncation:** session lifecycle list truncates `Purpose` at 40 chars with
`...` suffix (session_lifecycle_cmd.go:160). Task list truncates body summary
at 200 chars (no ellipsis — silent truncate). Task ID is shortened to 8 chars
in the list view (`shortTaskID`, task.go:239) — a presentation-only
convention; full ID is accepted by `niwa task show`. Vault status truncates
opaque tokens to 12 chars + `...` (`shortToken`, status.go:348).

### Status vocabulary

Lifecycle states — always **lowercase**, single English word:

- Sessions: `active`, `ended`, `abandoned` (only `active` and `ended`
  have writers; `abandoned` is a reserved constant).
- Tasks: `queued`, `running`, `completed`, `abandoned`, `cancelled`
  (`task.go:64` flag help, `internal/mcp/types.go`).
- Coordinator alive-check: `alive` / `dead` (mesh_list.go:71-73).
- Workspace summary: `drifted` (status.go:184) — descriptor not state.

`audit-secrets` classifications: `vault-ref`, `plaintext`, `empty`,
`resolved` — kebab-case for multi-word, lowercase always
(status_audit.go:18-23).

`audit-auth` source labels: `vault:personal-overlay`,
`vault:personal-overlay(<name>)`, `none` — colon-separated namespace,
parenthesized qualifier, `none` lowercase
(status_audit_auth.go:33,53).

**Rule:** internal lifecycle/state values are lowercase, single word
where possible. Multi-word values use either kebab-case
(`vault-ref`, `personal-overlay`) or colon-namespace (`vault:`). Never
TitleCase, never UPPER.

Header text (column titles) use UPPER but value cells use lowercase.
This is the dominant pattern — STATUS column in `mesh list` shows
`alive`/`dead`; STATUS column in `session list` shows `active`/`ended`;
STATE column in `task list` shows `queued`/`running`/etc.

### Success messages

Two distinct shapes:

**Style A — terse single-line `<verb>: <noun>` form, written to STDERR**
(used by surfaces tied to the shell-wrapper protocol):

- `niwa session create`: `session: created <id> at <path>` —
  session_lifecycle_cmd.go:76. **Stderr**, not stdout.
- `niwa session destroy`: `session: destroyed <id>` —
  session_lifecycle_cmd.go:113. **Stderr**.
- `niwa go ...`: `go: workspace root` / `go: workspace "name"` /
  `go: repo "name" in instance` / `go: session abc12345 (repo) at /path`
  — go.go:118, 135, 153, 181, 217, 221, 227, 272. **Stderr**.

These commands deliberately keep stdout clean because they're
cd-eligible — the shell wrapper reads `NIWA_RESPONSE_FILE` for a path,
not from stdout, and stderr is the user-facing channel.

**Style B — sentence-style on STDOUT** (used by surfaces that aren't
wrapped or are explicitly user-facing report commands):

- `niwa init` (named): `Workspace "myws" initialized at /abs/path.\n` +
  blank line + `Next steps:\n  1. ...\n  2. ...` — init.go:619. **Stdout**.
- `niwa init` (clone): `Workspace "myws" initialized at /abs/path from remote config.` +
  next steps — init.go:625.
- `niwa destroy <instance>`: `Destroyed instance: /abs/path` — destroy.go:138. **Stdout**.
- `niwa destroy --force` (workspace): `Destroyed workspace: /abs/path` — destroy.go:276,350. **Stdout**.
- `niwa reset`: `Reset instance: /abs/path` — reset.go:130. **Stdout**.
- `niwa config set global`: `Global config registered: <repo>` — config_set.go:84. **Stdout**.
- `niwa config unset global`: `Global config unregistered.` — config_unset.go:62. **Stdout**.

Style A: lowercase verb, no punctuation, one line, stderr.
Style B: capitalized verb, ends with period when it's a complete
sentence, on stdout, sometimes followed by a blank line and bulleted
next steps.

### Error messages

**No standard `niwa: error:` prefix.** Errors go through cobra's
default `Execute` which prints the returned `error` to stderr followed
by `Error:` cobra formatting. Code-side messages do NOT include `niwa:`
or `error:` prefixes — they're plain prose:

- `not inside a niwa workspace or instance` (destroy.go:78)
- `instance name is only valid from the workspace root; run `niwa destroy` (no arguments) to destroy the enclosing instance` (destroy.go:82)
- `instance %q not found, available instances: %s` (destroy.go:220, status.go:151)
- `workspace %q not found in registry` (apply.go:199)
- `not inside a workspace. Pass a workspace name or run from within a workspace directory` (create.go:107)
- `task not found: <id>` (task.go:259) — also writes the same string to stderr first via Fprintf (task.go:258) — defensive double-emit.

**Wrapping convention:** errors wrap the operation context with
`fmt.Errorf("verbing X: %w", err)`. Examples:
- `getting working directory: %w` (destroy.go:62, create.go:103, etc.)
- `loading global config: %w` (multiple)
- `enumerating instances: %w` (multiple)
- `parsing session response: %w` (session_lifecycle_cmd.go:71)

**Actionable errors:** several errors include hints inline:
- `repo %q not found in current instance: %w\nhint: use "niwa status" to list available repos` (go.go:150) — embeds a literal newline + lowercase `hint:` line.
- `not inside a workspace\nhint: use "niwa go <workspace>" to navigate to a registered workspace: %s` (go.go:237).
- `instance has uncommitted changes in %d repo(s); use --force to override` (reset.go:70).
- `instance has unpushed work and stdin is not a terminal; aborting (resolve unpushed work, or use --force to destroy without confirmation)` (destroy.go:117).
- The `apply --force` URL-change error is a multi-line block with explicit
  numbered remediation (apply.go:327-336).

**Stderr vs stdout in errors:** runtime errors flow as Go errors
(returned, printed by cobra to stderr). Non-fatal warnings printed
during a successful operation use `fmt.Fprintf(cmd.ErrOrStderr(),
"warning: ...\n", ...)`. Some commands also write to stderr
*before* returning the error so the user sees the message even when
cobra's error printing is suppressed (task show, line 258).

**Quoting:** identifiers (workspace names, repo names, session IDs)
in error text are wrapped in `%q` for double-quoting. Paths are
unquoted. Examples:
- `workspace %q not found` ✔
- `at %s` (path) ✔ (no quotes)
- `instance %q not found, available instances: <comma-list-unquoted>`.

### Warnings

**Format: `warning: <message>` lowercase prefix, written to stderr.**
The convention is universal:

- `warning: %s\n` (apply.go:105 — for config warnings)
- `warning: --allow-dirty is no longer meaningful ...` (apply.go:126)
- `warning: %v\n` (apply.go:179 — for registry update failures)
- `warning: NIWA_CHANNELS=%q is not a recognized value ...` (channels.go:64)
- `warning: could not stop mesh daemon: %v\n` (destroy.go:131)
- `warning: scan reported errors: %v\n` (destroy.go:306)
- `warning:` + free text when emitted via the MCP-result envelope:
  `fmt.Fprintln(cmd.ErrOrStderr(), "warning:", resp.DaemonWarn)`
  (session_lifecycle_cmd.go:74) — note the space-then-text join, not
  `: `.
- `warning: %s\n` (init.go:117)
- `warning: skipping %s: directory exists but is not a valid niwa instance` (create.go:75) — multi-clause with colon.

**The `branch_warning` MCP precedent:** `niwa session destroy` reads a
`branch_warning` field out of the JSON envelope returned by the MCP
direct-call path and writes it as a **second-class side-channel
warning**:
```go
if resp.BranchWarn != "" {
    fmt.Fprintln(cmd.ErrOrStderr(), "warning:", resp.BranchWarn)
}
fmt.Fprintf(cmd.ErrOrStderr(), "session: destroyed %s\n", sessionID)
```
The success line still prints. Same pattern for `daemon_warning` from
session create. So: warnings do NOT preempt success — both fire, with
the warning first.

**Capitalized `WARNING:` is reserved for security-sensitive notices.**
The rebind path in init prints `WARNING: registry entry %q rebound from %s to %s` (init.go:324) — explicit comment says: "Prominent on stderr per Security Considerations §6 — `--rebind` opens a registry-write path, and an automated agent passing it programmatically still leaves an audit trail."

**`note:` prefix** is used for purely informational stderr lines:
- `note: workspace name %q overrides %q from cloned config.` (init.go:332)
- `note: this workspace declares a vault (kind: %s). Bootstrap with:` (init.go:473)

**`hint:` prefix** for actionable suggestions, used both inline in error
messages and standalone:
- `hint: shell integration not detected. For auto-cd and completions, run:` (hint.go:16)
- `hint: use "niwa status" to list available repos` (go.go:150)

So niwa has four severity prefixes today:
- `WARNING:` (uppercase) — security-relevant audit event.
- `warning:` (lowercase) — non-fatal problem, command will continue or has succeeded with a caveat.
- `note:` — informational, no action needed.
- `hint:` — actionable suggestion.

### Flag conventions

**Long-form preferred. Short-form rare.** Only three short flags exist
today:
- `-r` / `--repo` (create, go) — go.go:24, create.go:19.
- `-w` / `--workspace` (go) — go.go:23.
- `-v` is NOT used (status uses `--verbose` long-only — status.go:29).

All other flags are long-form only. No `-f` shortcut for `--force`
anywhere. No `-y` for confirmation bypass.

**Naming patterns:**
- Negation: `--no-X` (`--no-pull`, `--no-progress`, `--no-channels`,
  `--no-overlay`). The matching positive form usually exists too
  (`--channels` ↔ `--no-channels`).
- Verbs: `--force`, `--allow-dirty`, `--allow-missing-secrets`,
  `--allow-plaintext-secrets`, `--rebind`, `--check-only`,
  `--skip-global`. Verb-flag pattern: `allow-X` for "let through what
  would normally be blocked", `force` for "bypass protections", `skip-X`
  for "don't do X".
- Filters: noun-form (`--repo`, `--status`, `--state`, `--role`,
  `--delegator`, `--instance`, `--name`, `--since`).
- Operations: imperative (`--audit-secrets`, `--audit-auth`,
  `--check-vault`, `--verbose`).

**`--force` semantics across commands:**
- `niwa destroy --force` — at workspace root with no args, wipe the
  entire workspace. Inside an instance, skip the dirty-repo check.
  destroy.go:18-20: `"skip uncommitted changes check; at the workspace
  root with no instance name, wipe the entire workspace after a
  non-pushed-work scan"`.
- `niwa apply --force` — bypass the URL-change refusal, apply.go:30-31.
- `niwa reset --force` — skip the uncommitted-changes check, reset.go:17.
- `niwa session destroy --force` — delete the session branch even when
  it has unmerged commits, session_lifecycle_cmd.go:46.

`--force` is **never the same thing twice**. Each command has a
specific, documented bypass. The flag isn't normalized to mean "yes,
do the dangerous thing"; it means "I accept the specific risk this
command guards against."

**Mutual exclusion:** `--overlay` and `--no-overlay` reject combination
explicitly: `"--overlay and --no-overlay are mutually exclusive"`
(init.go:133).

**Positional + flag mix-ups:** go.go:62-67 rejects positional + `-w`
or positional + `-r` with explicit messages: `"cannot combine
positional argument with -w flag; use one or the other"`.

### Confirmation prompts

niwa is **mostly non-interactive**. The only confirmation prompt path
in production code is `niwa destroy` for irreversible workspace-wipe
or instance-with-unpushed-work scenarios.

The mechanism (prompt.go):
- `IsStdinTTY()` gates whether to even attempt a prompt.
- `ReadConfirmation(prompt, expected, in, out)` reads a line, trims
  whitespace, returns whether it matches an expected token.
- Token is the **literal name** of the thing being destroyed
  (instance name or workspace name). Not `yes`/`no`. Not `Y/n`.
- Prompt itself is `"> "` (destroy.go:120). Spartan.

When stdin isn't a TTY, the command refuses with: `instance has
unpushed work and stdin is not a terminal; aborting (resolve unpushed
work, or use --force to destroy without confirmation)` (destroy.go:117).

There's also a TUI picker (`tui.Pick`) for the multi-instance destroy
case — when ≥2 instances exist and no name was given, niwa shows an
interactive selector (destroy.go:255). Falls back to a non-TTY error
listing all instances + suggesting either passing a name or `--force`
(destroy.go:243-248).

**`niwa session destroy` does NOT prompt today.** It deletes immediately
using `git branch -d` (safe — fails on unmerged) and emits a warning
when the safe deletion fails. `--force` switches to `git branch -D` for
the unconditional deletion.

So the precedent is: **destructive ops that can lose unpushed work
prompt with literal-name confirmation; destructive ops on bounded
ephemeral resources (worktrees, branches that exist in git history)
do not prompt and rely on `--force` for "I really mean it".**

### Help text

Every command has both `Short` and `Long`. Conventions:

- `Short` is sentence-fragment, no period, capitalized first word.
  Examples: `Manage sessions in the workspace mesh`,
  `Create a new git-worktree session for a repo`,
  `Destroy a session and remove its worktree`,
  `List coordinator sessions registered in this workspace`.

- `Long` opens by repeating the Short verbatim or with a period, then
  adds a blank line, then a paragraph or sub-list. The repeating-Short
  pattern is intentional — see session.go:18-19, mesh_list.go:22-23,
  session_lifecycle_cmd.go:23.

- Sub-command lists in parent `Long` use a bullet pattern:
  ```
  Subcommands:
    create    Create a new git-worktree session for a repo
    destroy   Destroy a session and remove its worktree
    list      List sessions (use --repo or --status to filter ...)
  ```
  Two-space indent, sub-command name padded to 9-10 chars
  (session.go:22-25, mesh.go:14-17, task.go:27-29, config.go:14-16).

- `Examples:` blocks exist on `niwa go` (go.go:50-56) and
  several others. Indented with two spaces. `# comment` after the
  command for what it does:
  ```
  Examples:
    niwa go                       # workspace root from cwd
    niwa go tsuku                 # repo "tsuku" in current instance, ...
  ```
  Comments column-aligned to ~30 chars. Imperative-mood comments,
  lowercase.

- `Use:` strings: `<command> [optional]` for optional positional,
  `<command> <required>` for required, brackets for optional. Examples:
  `init [name]`, `apply [workspace-name]`, `create [workspace-name]`,
  `destroy [instance]`, `session create <repo> <purpose>`,
  `session destroy <session-id>`, `task show <task-id>`,
  `go [target] [session-id]`. Always lowercase, kebab-case for
  multi-word placeholders.

- Flag descriptions use lowercase first letter, no period:
  - `"target a specific instance by name"` (apply.go:20)
  - `"skip uncommitted changes check"` (reset.go:17)
  - `"force workspace resolution via registry"` (go.go:23)

  Exception: the session list flags break this rule:
  `"Filter by repo name"` and `"Filter by status: active, ended,
  abandoned"` (session.go:51-52). Capitalized first letter. This is an
  inconsistency.

### Logging vs user output

niwa has no separate logger for the CLI. Every non-data line goes
through `fmt.Fprintln`/`fmt.Fprintf` to either `cmd.OutOrStdout()` (data)
or `cmd.ErrOrStderr()` (status, warnings, hints, errors).

`internal/workspace.Reporter` is the closest thing to a logger — used
during `apply` and `create` for clone progress. It supports a TTY mode
(in-place status updates) and a non-TTY append-only mode, gated by
`!noProgress && term.IsTerminal(int(os.Stderr.Fd()))` (apply.go:119,
create.go:144). It writes to stderr.

There is no `--quiet` flag, no `--verbose` global (status has its own
`--verbose`), no log-level gate. The CLI emits what it emits.

**Stdout vs stderr:**
- **Stdout**: tabular data (mesh list, session list, task list, audit
  tables), success messages on user-facing report commands (init,
  destroy, reset, status), the `version` block.
- **Stderr**: warnings/notes/hints, `session: created/destroyed` lines
  (because session create is shell-wrapped), `go:` lines (same reason),
  Reporter clone progress, picker/confirmation prompts and their
  responses.

The comment at session_lifecycle_cmd.go around the success line
explicitly chose stderr so stdout stays clean for the wrapper protocol
even though `niwa session create` does still write the landing path
via `NIWA_RESPONSE_FILE`. Same pattern for `niwa go`. **Any new
shell-wrapper-eligible command should follow this stderr pattern.**

### Color and TTY

- `noColor` is read from `NO_COLOR` in `PersistentPreRunE`
  (root.go:41). The variable is package-level and accessible to every
  command, but **no command currently uses it** to gate color output.
  niwa effectively emits no ANSI color in CLI prose.
- `noProgress` is the `--no-progress` persistent flag plus an
  `term.IsTerminal` check; only `apply` and `create` consume it,
  passing the result to `workspace.NewReporterWithTTY` so the
  Reporter's progress-line behavior degrades cleanly when piped or
  in CI (apply.go:119, create.go:144).
- The functional test
  `niwa apply --no-progress produces clean line-by-line output`
  (critical-path.feature:196-217) asserts `the error output does not
  contain an ANSI escape sequence` — so the project actively guards
  against bleeding ANSI when output is captured.
- `IsStdinTTY` (prompt.go:22) gates interactive prompts. Used by
  `destroy` to refuse confirmation/picker paths in non-TTY contexts.
- `tui.IsAvailable` is the TUI-pickability check, also gated in destroy
  (destroy.go:242).
- The Reporter is the only place that emits color/cursor codes. CLI
  prose is plain ASCII.

So: **niwa is plain-text. No prose-level color. NO_COLOR is
reserved/unwired. TTY detection exists but only gates progress-bar UX
and interactive prompts, not text styling.**

## Implications

The existing tone is rigorous and self-consistent on most axes. The new
`niwa session attach`, `niwa session detach --force`, and the
`AVAILABILITY` column on `niwa session list` should mechanically inherit
these conventions. Concrete prescriptions follow.

### `niwa session list` — new `AVAILABILITY` column

- Header text: `AVAILABILITY` (ALL CAPS, single word — fits the
  `ROLE/PID/STATUS/LAST-SEEN/PENDING` and
  `ID/REPO/STATUS/CREATED/PURPOSE` precedents). No hyphen needed.
- Value cells: lowercase, kebab-case for multi-word. Suggested values
  (subject to the state-model lead's findings):
  - `available` — no attach lock present.
  - `attached` — current process holds the lock or another live PID
    holds it (single-UID model).
  - `stale` — lock file exists but lock holder PID is dead. Mirrors
    the `dead` vocabulary already in `niwa mesh list`.
- Column placement: append to the end of the existing fixed-width run,
  before `PURPOSE` (which is the unbounded final column today).
  Suggested format string for the lifecycle list:
  ```go
  fmt.Fprintf(out, "  %-8s %-12s %-10s %-20s %-12s %s\n",
      "ID", "REPO", "STATUS", "CREATED", "AVAILABILITY", "PURPOSE")
  ```
  Width 12 fits `available`/`attached`/`stale` plus padding.
- Sort order: unchanged (by `SessionID`).
- Empty-state line unchanged.

### `niwa session attach <session-id>`

- `Use:` string: `attach <session-id>`. Match `session destroy <session-id>`.
- `Short:` `Attach to a session's worktree`.
- `Long:` opens by repeating the Short with a period, blank line, then
  paragraph(s) explaining lock semantics, single-UID assumption, what
  "attach" actually does on disk.
- Success message (terse, on stderr — this is shell-wrapper-eligible
  per niwa go's pattern, since attach lands the user in the worktree):
  `session: attached <id> at <worktree-path>`.
- The shell wrapper should add `attach` to the cd-eligible list in
  `shell_init.go:54` so `__niwa_cd_wrap` invokes correctly (separate
  PR concern but worth flagging).
- Already-attached error (same UID, our own PID): treat as success/no-op
  with a `note: already attached` line on stderr — matches the
  `session destroy` idempotency pattern.
- Already-attached error (same UID, different live PID):
  `error: session %q is attached by pid %d (started %s); detach there or use --force`.
  The error returns; cobra prints. Wording mirrors the
  destroy URL-change error's actionable shape.
- Stale-lock auto-recovery: `warning: removing stale attach lock from
  pid %d (no longer running)` on stderr, then proceed. Same shape
  as `warning: could not stop mesh daemon: %v` (destroy.go:131).

### `niwa session detach [--force]`

- `Use:` string: `detach [<session-id>]` if implicit-current-worktree
  detach is supported, else `detach <session-id>`.
- `Short:` `Detach from a session's worktree`.
- Long describes when `--force` is needed: only to break a lock you
  don't own (e.g., crashed peer process). Without `--force`, detach
  refuses if you're not the lock holder.
- `--force` flag definition:
  ```go
  detachCmd.Flags().BoolVar(&detachForce, "force", false,
      "release the attach lock even if held by another process")
  ```
  Lowercase first letter, no period — matches every existing flag
  description except the session-list outliers.
- Success: `session: detached <id>` on stderr. Mirrors
  `session: destroyed <id>` byte-for-byte in shape.
- Force-required error:
  `error: session %q is held by pid %d; pass --force to break the lock`.
  Same actionable shape as the reset uncommitted-changes message.
- Detach-from-non-current-session refusal: `error: not attached to
  session %q` (when the user runs detach without the right lock).

### Status vocabulary for attach.state

Use lowercase kebab-case constants for the state file's status field
to mirror existing precedent:
- Holder identity field: `pid`, `started_at` (matches `daemon` sub-object
  schema in PR #115's design — `pid`, `alive`, `started_at`).
- Don't introduce TitleCase or UPPER tokens in JSON.

This keeps the AVAILABILITY column rendering trivial: it's the lowercase
state name, padded to 12 chars, with no transformation.

### Confirmation prompt: NOT for attach/detach

No confirmation prompt is warranted for either command. Attach is
recoverable (just detach), and detach is always recoverable too (the
worktree's still there, just attach again). Both should follow the
"`--force` for the irreversible bit, no prompt" pattern that
`niwa session destroy` already establishes.

### Where each line goes

| Surface | Stream | Rationale |
|---|---|---|
| `session: attached <id> at <path>` | stderr | shell-wrapper-eligible |
| `session: detached <id>` | stderr | parallel to destroyed |
| `note: already attached` (idempotent) | stderr | matches `note:` precedent |
| `warning: removing stale attach lock from pid %d` | stderr | warning convention |
| Errors | returned via RunE → cobra prints stderr | standard |
| `AVAILABILITY` column row data | stdout | tabular data goes to stdout |

### Help-text examples block (recommended)

`session attach`/`session detach` should follow the `niwa go`
precedent and include an `Examples:` block in `Long` — there are
several distinct attach scenarios (your own session, peer crashed,
already-attached) that benefit from concrete invocations.

## Surprises

Inconsistencies that exist today but are out of scope for the attach PRD:

1. **Two flag-help capitalization styles.** Most flags use lowercase-
   first ("target a specific instance by name") but
   `niwa session list --repo`/`--status` uses capitalized
   ("Filter by repo name", "Filter by status: ...") — session.go:51-52.
   Same file, different convention to the rest of niwa. Don't propagate
   the capitalized style to `--force` on detach; use lowercase.

2. **Two-spaces-vs-one-space column separator.** Status audit tables
   (`status_audit.go`, `status_audit_auth.go`) use TWO spaces between
   columns; mesh list / session list / task list use ONE space (within
   the `%-Ns ` format widths). The audit tables also have NO leading
   indent, while the others use a TWO-space leading indent. Both
   patterns coexist; no one has unified them. The session list uses
   the indent+single-space style — keep that for AVAILABILITY.

3. **`session: created/destroyed` writes to stderr, but `Destroyed
   instance: ...` and `Reset instance: ...` go to stdout.** This is the
   shell-wrapper protocol in disguise — session commands and `niwa go`
   keep stdout clean for the wrapper while ordinary commands print
   success on stdout. New session commands (attach/detach) MUST follow
   the stderr pattern, not the stdout one.

4. **`warning:` separator.** `fmt.Fprintln(out, "warning:", text)`
   produces `warning: text` (single space, the `Fprintln` arg-join
   default), while `fmt.Fprintf(out, "warning: %v\n", err)` produces
   the same output. Both forms are used. Cosmetically identical, but a
   future auditor reading the source might wonder. Not blocking.

5. **`niwa status` summary view has no header row.** Every other table
   does. Status's `INSTANCE / REPOS / DRIFT / APPLIED` would be a clean
   addition but it's not the precedent today, so AVAILABILITY's
   addition to `session list` should NOT trigger a follow-up to add
   one to status.

6. **`abandoned` is reserved-but-unused.** `SessionLifecycleState`
   exposes `abandoned` as a filter value (session.go:52, sessions.md:167)
   but no code path writes it. The UI surfaces it as if it works.
   Confirmed by Round 1. AVAILABILITY introduces a similar risk —
   define `stale` only if the implementation actually writes/derives it.

7. **`niwa session list` (no flags) is a deprecated alias** with a
   stderr-printed deprecation warning (session.go:57). The new
   AVAILABILITY column lives on the `--status`/`--repo` lifecycle
   path, NOT the deprecated alias path. Make sure the PRD pins this.

## Open Questions

1. **Should `niwa session attach` write the landing path via the
   shell-wrapper protocol?** If yes, attach must be added to
   `shell_init.go:54`'s case list. If no, attach prints the worktree
   path on stderr and the user `cd`s manually. Recommendation: yes
   (matches `niwa go`'s value prop), but verify with the
   discovery-UX lead.

2. **Does the AVAILABILITY column appear on `niwa mesh list` too?**
   Mesh list is the coordinator-process registry; coordinator sessions
   probably also benefit from an attach concept, but that's not in
   scope per the issue. The PRD should declare AVAILABILITY as
   lifecycle-list-only and explicitly NOT touch mesh list, to avoid
   scope creep.

3. **How does the `STATUS` column interact with `AVAILABILITY`?**
   E.g., a session in `STATUS=ended` cannot have `AVAILABILITY=
   attached`. Should the column show `-` (the precedent for
   session_lifecycle_cmd.go:153 missing-CREATED dashes), or hide
   the value, or stay blank? Recommendation: render `-` for
   `ended`/`abandoned` rows so column alignment never breaks.

4. **What's the wire format for `attach.state`?** TOML, JSON, or
   bare key=value lines? niwa uses JSON for runtime state files
   (`state.json`, `instance.json`, `sessions.json`) and TOML only for
   user-authored config. Recommendation: JSON, named `attach.state`
   with `.json` extension or content-typed JSON without — match
   `daemon.pid` if there's a sibling, otherwise full `attach.json`.
   Out of scope for tone audit; flag for the lock-semantics lead.

5. **Should the success line on attach include the worktree path
   like `session: created` does, or just the session ID like
   `session: destroyed`?** The path is useful because attach is
   shell-wrapper-eligible and the user might miss the cd if the
   wrapper isn't loaded. Recommendation: include the path —
   `session: attached <id> at <path>` — matching `created`.

6. **Should errors carry a hint line?** Inline-hint errors
   ("hint: use ...") improve UX but aren't universal. Recommendation:
   yes for the "attached by another pid" case (it's the most
   confusing); no for trivial invalid-argument errors.

## Summary

niwa's tone is unornamented, plain-text, fixed-width, and consistent:
ALL-CAPS table headers with two-space leading indent and lowercase
kebab-case state values; `<verb>: <noun>` success lines on stderr for
shell-wrapper-eligible commands and capitalized stdout sentences
elsewhere; lowercase `warning:`/`note:`/`hint:` prefixes with
uppercase `WARNING:` reserved for security audit events; long-form
flags by default with `--force` carrying a command-specific bypass
meaning rather than a global "yes" semantic. The attach/detach
prescriptions follow mechanically: `AVAILABILITY` as a fixed-width
12-char ALL-CAPS column with lowercase values (`available`,
`attached`, `stale`); `session: attached <id> at <path>` and
`session: detached <id>` on stderr; `--force` flag on detach with
description `release the attach lock even if held by another process`;
no confirmation prompt; stale-lock auto-recovery via the existing
`warning:` channel. The biggest open question is whether the
shell-wrapper landing-path protocol applies to `attach` (likely yes,
matching `niwa go` and `niwa session create`) — that decision
determines both whether attach needs a `shell_init.go` case-list
entry and whether the success message should include the worktree
path or just the ID.
