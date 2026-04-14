# Security Review: contextual-completion

## Dimension Analysis

### External Artifact Handling
**Applies:** No

The completion subprocess downloads nothing and executes nothing from remote
sources. It reads two local inputs — `$XDG_CONFIG_HOME/niwa/config.toml` and
entries under the workspace/instance filesystem tree — and emits candidate
strings to stdout. Nothing is fetched, no binaries are invoked, no hooks are
run. The `niwa __complete` subcommand is auto-registered by cobra and dispatches
only to the closures this design attaches.

### Permission Scope
**Applies:** Yes (low severity — no privilege escalation)

The completion subprocess runs with the user's own UID and needs:

- Read on `$XDG_CONFIG_HOME/niwa/config.toml` (same file the interactive CLI
  already reads).
- Read/stat on workspace roots listed in that config, and on
  `.niwa/instance.json` sentinels inside each instance directory.
- Read/stat on two levels of subdirectories under the discovered instance root
  (group -> repo directory names).
- `os.Getwd()` for cwd-based instance discovery.
- No network, no `exec`, no writes, no elevated capability.

The scope is identical to what the interactive `niwa` commands already do.
Running it on every tab press is a frequency change, not a privilege change.
No mitigation required beyond ensuring the closures never widen the scope
(e.g., never follow registry paths to arbitrary filesystem locations the
config author didn't already authorize — `ListRegisteredWorkspaces` returns
names, not paths, so this holds).

### Supply Chain or Dependency Trust
**Applies:** Yes (low severity — no new dependencies)

The design pulls in zero new modules. It uses:

- `github.com/spf13/cobra` v1.10.2 — already a direct dependency; completion
  infrastructure is part of cobra core. `GenBashCompletionV2` and
  `GenZshCompletion` are maintained by upstream and are the standard path
  used by kubectl, helm, gh, and hundreds of other CLIs.
- `os`, `path/filepath`, `sort` — standard library.

The generated shell scripts are static text produced at `shell-init` time and
committed to the user's shell-integration blob. An attacker who can already
write to `$TSUKU_HOME/share/shell.d/` or the user's rc file has capabilities
far beyond what this feature exposes. No new trust boundary.

### Data Exposure
**Applies:** Yes (very low severity — confined to local stdout)

Completion output is written only to the shell's completion buffer (the
calling TTY). What can leak:

- Registry workspace names from `config.toml`.
- Instance directory names from the filesystem.
- Repo directory names from the two-level group scan.
- Instance numbers appended in descriptions (`repo in 1`).

All of this is already visible to `ls` and `niwa list`. The subprocess does
not transmit to the network, does not log, and does not touch stderr on the
happy path (Implicit Decision C: errors return empty list, not
`ShellCompDirectiveError`, which also avoids accidentally surfacing
pathnames in error messages to the completion menu).

The one residual concern is that completion may read the global config in
contexts the user didn't anticipate (e.g., a screen-shared terminal pressing
TAB surfaces workspace names). This is the same exposure as running `niwa
list` in the same session; completion doesn't widen it.

### Tab-induced side effects
**Applies:** Yes (low severity with current design)

Cobra's `__complete` protocol writes one candidate per line followed by a
`:<directive>` trailer. The shell wrapper parses this as data — candidates
never reach a shell `eval` unless the user explicitly presses Enter after a
completion selects. Even then, the user is executing their own intended
command with the candidate as an argument, which is the normal CLI contract.

The only way a malicious directory name triggers shell interpretation is if
the user's shell has non-default completion hooks that re-evaluate candidates
(e.g., a custom `complete -F` wrapper around niwa's wrapper). That is out of
scope for niwa's threat model.

Panic resistance is the in-scope concern. The closures call:

- `config.LoadGlobalConfig()` — already handles malformed TOML by returning
  an error, which closures swallow (Implicit Decision C). Safe.
- `workspace.EnumerateInstances` / `EnumerateRepos` — `os.ReadDir` returns
  an error for missing/permission-denied paths; closures swallow.
- `workspace.DiscoverInstance(cwd)` — walks up from cwd; bounded by
  filesystem depth.

No code path constructs shell-evaluated strings from filesystem input.
Mitigation already in place.

### Symlink traversal
**Applies:** Yes (low-to-medium severity — worth documenting)

`os.ReadDir` itself does not follow symlinks for the parent directory, but:

- `EnumerateInstances` calls `os.Stat(statePath(dir))` for each entry, which
  *does* follow symlinks. A symlinked subdirectory pointing at an attacker
  path with a `.niwa/instance.json` file inside would be recognised as an
  instance and its name exposed as a completion candidate. Impact is limited
  to leaking the symlink target's existence, because completion returns the
  symlink's own name, not its target.
- `EnumerateRepos` is a two-level scan. A symlink inside the instance root
  pointing at `/` would not cause unbounded descent because the scan is
  depth-bounded (two levels). Worst case is a stat against a large remote
  filesystem entry, which could stall the completion call.
- `DiscoverInstance` walks `filepath.Dir` upward; it does not traverse into
  symlinked children, so a symlink loop in cwd is not a concern.

The one realistic attack is a symlink inside a workspace root pointing at a
slow/unresponsive filesystem (NFS, FUSE) — the 5ms latency budget becomes
unbounded, and the user's shell hangs on TAB. Severity: low, because the
victim must have checked out a malicious workspace onto their own disk, and
the fix is the same `timeout` or escape they already need.

**Mitigation suggestion:** document that enumeration functions use
`Stat` (follows symlinks) rather than `Lstat`. If tighter isolation becomes
desirable, switching `EnumerateInstances`'s sentinel check from `os.Stat` to
`os.Lstat` on the directory entry and skipping symlinked instance dirs would
harden it without breaking legitimate layouts (niwa itself never creates
symlinked instance directories). Not required for v1.

### TOCTOU
**Applies:** Yes (negligible severity)

Between TAB presses, the global config or workspace tree can change. Each
`__complete` invocation re-reads state, so there is no cross-process cache
to poison (Decision 2: no caching). Within a single invocation:

- `LoadGlobalConfig` reads the file once and unmarshals; a concurrent writer
  can produce a partial read, but `LoadGlobalConfig` already returns an
  error in that case and closures swallow it.
- `EnumerateInstances` + `LoadState` pair is not atomic: an instance could
  be deleted between the `ReadDir` and the `Stat`. The Stat then fails,
  the entry is skipped. Benign.

No privilege boundary is crossed, so TOCTOU here is a correctness concern
at worst, not a security one. The "malicious config change mid-completion"
scenario requires the attacker to already have write access to
`$XDG_CONFIG_HOME`, at which point they can just run `niwa` themselves.

### TAB/newline injection in candidate strings
**Applies:** Yes (low severity — worth a sanity guard)

Cobra's protocol:

- Candidates are separated by `\n`.
- Description is separated from candidate by the first `\t`.

A workspace name in TOML cannot contain a newline without being quoted
(TOML map keys are restricted), and niwa's registration path validates
identifiers — so the registry source is clean. Filesystem-sourced names
(instance dirs, repo dirs) are the risk: POSIX allows any byte except `/`
and NUL in filenames, including `\t` and `\n`.

A repo named `api\nworkspace` (literal newline in the filename) would be
emitted by the closure as two lines, which cobra's protocol would parse as
two candidates: `api` and `workspace`. Severity depends on what the user
then does with the selected candidate:

- If the user selects the phantom second line and runs `niwa go workspace`,
  they navigate to something that may not exist, or that does exist with a
  different meaning (name collision with a real workspace). Worst case is
  confusion, not code execution — the runtime resolver still validates the
  selected name against the filesystem.
- A repo named `benign\tmalicious description` would render `benign` with
  a spoofed description but still resolve to the real `benign\t...` dir,
  which the runtime would reject (the real filename contains TAB).

The test-harness parser described in Decision 5 (`completionSuggestions`)
strips TAB-separated descriptions — a filename containing literal TAB would
split the test's view of that candidate into "candidate" and "description"
halves, which could mask bugs but not create them.

**Mitigation suggestion:** in `EnumerateInstances` / `EnumerateRepos`,
reject entries whose names contain `\t`, `\n`, or control characters (< 0x20)
before returning them. One-liner filter. Protects both the cobra protocol
and the test parser. Pre-existing `findRepoDir` code did not need this
guard because it was comparing exact strings, not serializing them to a
line-oriented protocol.

### Destructive-command completion
**Applies:** Yes (UX only, not security)

Decision 3 accepts that `niwa destroy <TAB> <Enter>` is two keystrokes to
delete an instance. Framing this as a security issue would require one of:

- A scenario where an attacker influences *which* instance is sorted first
  in the completion list so the user destroys the wrong one. Instance
  numbers are monotonically allocated by niwa itself; an attacker cannot
  inject entries without already having write access to the workspace
  root. At that point the attacker can just `rm -rf` the workspace.
- A scenario where completion output is interpreted as code. Ruled out
  under "Tab-induced side effects".

The UX footgun remains (accidentally destroying work), but that is a
user-mistake category, not an adversarial one. No security impact.

## Recommended Outcome

**OPTION 2 - Document considerations:**

Add a Security Considerations section to the design doc before Consequences,
covering the three items worth pinning down: symlink resolution semantics,
filesystem name sanitization, and the explicit no-network/no-exec contract.
Draft:

> ### Security Considerations
>
> **Threat model.** The completion subprocess runs with the user's own UID,
> reads only files the interactive CLI already reads, and writes only to the
> shell's completion buffer. No network I/O, no sub-process execution, no
> writes to the filesystem. An attacker's only reachable surface is whatever
> they can place under the user's workspace root or `$XDG_CONFIG_HOME`.
>
> **Filesystem name sanitization.** `EnumerateInstances` and `EnumerateRepos`
> must filter out entries whose names contain `\t`, `\n`, or control
> characters (< 0x20) before returning them. Cobra's `__complete` protocol
> uses `\n` as the candidate separator and `\t` as the description separator;
> a filename containing either byte would be split into phantom candidates
> under cobra's parser and also confuse the functional-test
> `completionSuggestions` helper. The filter is a one-liner per enumeration
> function and does not affect legitimate niwa-created directories.
>
> **Symlink semantics.** Enumeration uses `os.Stat` (follows symlinks) to
> match existing `findRepoDir` behavior. A symlinked instance directory
> pointing at a slow or remote filesystem could cause a tab press to hang;
> this is a denial-of-service on the user's own shell, not a privilege
> boundary crossing. Users who manually symlink into workspaces accept the
> same latency risk at runtime. No change from pre-feature behavior.
>
> **Error handling.** Per Implicit Decision C, closures return an empty
> candidate list on any error rather than `ShellCompDirectiveError`. This
> avoids leaking pathnames or stack traces into the shell's completion
> banner on transient filesystem failures.
>
> **Destructive commands.** Decision 3's choice to complete normally is a
> UX trade-off, not a security one. Instance numbers are allocated by niwa
> itself; an attacker able to inject a spoofed instance into the sorted list
> already has workspace write access, at which point the workspace itself
> is compromised.

Alongside the doc change, apply the one-line filter in `EnumerateInstances`
and `EnumerateRepos`:

```go
if strings.ContainsAny(name, "\t\n") || hasControlChar(name) {
    continue
}
```

This is small enough to fold into the Phase 1 data-layer deliverable without
a new issue.

## Summary

The design exposes no new network, execution, or privilege surface — it's a
local, read-only subprocess over data the interactive CLI already handles.
The only concrete hardening worth adding is a filter against `\t`/`\n`/control
chars in filesystem-derived candidates, because cobra's protocol parses those
bytes as delimiters; without the filter, a crafted repo or instance name
could surface as phantom candidates. Recommend landing Option 2 (document
plus one-line filter) during Phase 1.
