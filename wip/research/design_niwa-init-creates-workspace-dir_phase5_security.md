# Security Review

## Verdict: APPROVE WITH CHANGES

## Issues Identified

### Issue 1: Leading-dot names produce hidden directories without explicit acknowledgment
- **Severity:** low
- **Description:** The regex `^[a-zA-Z0-9._-]+$` admits names like `.foo`, `..foo`, `.git`, `.ssh`, `.niwa`. The design's only explicit rejections are `.` and `..`. Examples of risk:
  - `niwa init .git` creates `<cwd>/.git/.niwa/...`. If invoked at the root of an existing Git repo, this collides with Git's metadata directory and either fails noisily mid-MkdirAll (if `.git` already exists) or creates an attacker-discoverable shadow that subsequent Git operations may not flag.
  - `niwa init .niwa` creates `<cwd>/.niwa/.niwa/workspace.toml`. The outer `.niwa` is exactly the marker `CheckInitConflicts` walks up looking for, so any subsequent `niwa` command run from a parent directory would walk into a confused state.
  - `niwa init .ssh` creates a hidden directory many users would not see in a default `ls`. While not directly exploitable, a user lured into running `niwa init <name>` with a hidden-dot name (e.g., from a copy-paste in a prepared README) might end up writing files under a trust-laden hidden directory without realizing it.
  The PRD explicitly defers "tightening the name regex itself (length cap, excluding leading dots, restricting to ASCII-strict)" to a separate concern, so this is a known scope limitation, not an oversight. However, the design did not surface that the inherited regex permits these names.
- **Mitigation:** Add a short note in the design's Security Considerations section documenting the regex's permissiveness around leading dots and known-special directory names (`.git`, `.niwa`). At minimum, blacklist `.niwa` explicitly (since it has special meaning to niwa itself); consider blacklisting `.git` opportunistically. Length-cap and leading-dot tightening can stay deferred per PRD.
- **Already covered in design:** no (the Security Considerations section claims the regex "forecloses traversal and absolute-path injection at the input layer" but does not mention semantically dangerous but regex-conforming names)

### Issue 2: Registry rebind without user consent enables a CWD-poisoning attack
- **Severity:** medium
- **Description:** Per R8 / AC-19, when `<name>` is already registered to `Root = /path/A` and the user runs `niwa init <name>` from `/path/B`, init succeeds, the registry's `Root` is silently rebound to `/path/B/<name>`, and only a stderr warning is emitted. This creates a useful primitive for an attacker who can write to a directory the user's shell will land in:
  - Attacker convinces user to clone or extract an archive into a directory they control (`~/Downloads/malicious-tarball/`).
  - The tarball contains a niwa workspace skeleton or a `.niwa/` directory at a known path.
  - User runs `niwa init my-existing-workspace` (a workspace name they recognize) from `~/Downloads/malicious-tarball/`. Maybe a phishy README told them to.
  - The registry now points `my-existing-workspace` at the attacker-controlled location. A subsequent `niwa go my-existing-workspace` lands the user in the attacker-controlled directory, where any `CLAUDE.md`, `.claude/settings.json`, or workspace.toml can do further damage (claude-code hooks, env vars, etc., per the niwa overlay docs).
  The stderr warning is the only mitigation, and stderr warnings are routinely missed (especially when buried in init output). The design should either (a) require an explicit `--rebind` flag for this case, or (b) make the warning more prominent (e.g., a confirmation prompt for interactive sessions).
- **Mitigation:** Reconsider the PRD's "warn, don't error" stance. At minimum:
  - Require interactive confirmation ("Y/N") when stdin is a TTY and a rebind is happening.
  - Print the warning to stderr in a visually distinct way (e.g., prefixed with `WARNING:` and including a trailing newline so it doesn't get swallowed).
  - Consider a `--rebind` flag that must be explicitly passed; without it, hard-error and instruct the user to either pass `--rebind` or pick a different name.
  - At the very least, capture this as an explicit security trade-off in Security Considerations so reviewers know the threat was considered.
- **Already covered in design:** no (the design discusses rebind only as a UX behavior; the Security Considerations section is silent on it)

### Issue 3: TOCTOU window between Lstat and MkdirAll has a more severe failure mode than acknowledged
- **Severity:** low
- **Description:** The design correctly notes the TOCTOU window between `os.Lstat` returning `ErrNotExist` and the subsequent `os.MkdirAll`. It claims the failure mode is "loud" because `MkdirAll` would fail on a raced regular file or symlink. This is mostly true, but with a wrinkle: if an attacker races a *symlink* to a directory the attacker controls (e.g., `<cwd>/<name>` -> `/tmp/attacker-controlled-dir`) into the gap, `os.MkdirAll` will *succeed* (because the symlink resolves to a directory, and `MkdirAll` is satisfied). The subsequent `MaterializeFromSource` then writes the cloned `.niwa/` into the attacker's directory. The success message would then print the resolved `EvalSymlinks` path, which the attacker controls. This is a narrow window (the race must complete between two consecutive syscalls in the same Go function) but it is a realistic local-attacker primitive on shared systems.
- **Mitigation:** Either:
  - After `MkdirAll`, re-stat the target and verify it's not a symlink (a defensive check that closes the race in a way the user can verify).
  - Use `os.Mkdir` (not `MkdirAll`) for the final component, which fails if the target exists at all, including as a symlink. Then handle the parent-directory creation separately.
  - Document the residual risk in Security Considerations and mark it explicit-out-of-scope rather than dismissing it as "narrow and loud."
- **Already covered in design:** partial (TOCTOU is acknowledged; the symlink-to-attacker-directory variant is not)

### Issue 4: `EvalSymlinks` for the success message can leak the resolved path of an attacker-controlled symlink
- **Severity:** low
- **Description:** The design uses `filepath.EvalSymlinks` to resolve the absolute path printed in the success message (per R9). If the parent of `<cwd>/<name>` (i.e., `<cwd>` itself) contains a symlink in its ancestry that the attacker controls, the resolved path printed to stdout could disclose attacker-chosen path segments. This is not a privilege escalation but it could be used in a social-engineering context (e.g., "this resolves to your home directory, but the success message says `/tmp/foo/...`, that's normal" — the attacker uses the unexpected output to confuse the user). More concretely: if the workspace was actually created at the symlink target rather than where the user expected, the printed path makes the divergence visible (which is the intent of R9), but if the symlink was raced in via Issue 3, the printed path would point at the attacker's location and the user might not notice.
- **Mitigation:** Print *both* the user-facing path (`<cwd>/<name>` constructed) and the `EvalSymlinks`-resolved path when they differ. This gives the user explicit visibility into symlink resolution. Alternatively, refuse to follow symlinks at all in the parent path during init (using `filepath.Abs` instead of `EvalSymlinks` for the success message).
- **Already covered in design:** no (the design references EvalSymlinks as a macOS `/var/...` -> `/private/var/...` convenience but does not discuss its security implications)

### Issue 5: Concurrent `niwa init <name>` invocations have undefined behavior
- **Severity:** low
- **Description:** Two concurrent `niwa init my-ws` invocations from the same cwd:
  - Both pass `os.Lstat` (target doesn't exist).
  - Both attempt `os.MkdirAll`. The first wins; the second succeeds because `MkdirAll` is happy with an extant directory.
  - Both proceed to `MaterializeFromSource`. The two clones race into the same `.niwa/` directory, potentially producing a corrupted or inconsistent workspace state.
  - Both attempt `SaveState`. `SaveState`'s atomic-rename semantics (assuming standard niwa pattern) means one wins and the other is overwritten, but both entries point at the same target.
  - Both update the global registry. Last-writer-wins.
  The result is a workspace that may be partially materialized, with a state file from one invocation but content from another. The design does not discuss concurrency or any locking mechanism.
- **Mitigation:** Add a process-level lock (e.g., a flock on `<targetDir>/.niwa.lock` acquired immediately after MkdirAll). niwa likely already has lock primitives elsewhere — the design should reference them or add a section explaining why they aren't needed. At minimum, document the concurrency story in Security Considerations.
- **Already covered in design:** no

### Issue 6: `ConfigNameOverride` in plain-text JSON is trust-establishing but unsigned
- **Severity:** low
- **Description:** The override flows from init-time JSON (`<workspaceRoot>/.niwa/instance.json`) into `Apply.Create`'s `InstanceName` and `ConfigName`. Any user (or process) with write access to the JSON can rewrite the override before `niwa apply` runs. Since `InstanceName` likely flows into directory paths, registry entries, and possibly Claude-Code overlay paths, a malicious modification could redirect downstream commands.
  Specifically: if `InstanceName` is used as a path segment anywhere downstream (e.g., for instance directories, vault keys, mesh coordinator paths), a poisoned override containing path-like characters that pass downstream (NOT validated again) could cause directory traversal at apply time. The design relies on init-time validation, but the JSON file is a persistence boundary; a value present at apply time may have been written by a different process than the one that ran `niwa init`. A defensive design would re-validate `ConfigNameOverride` against the same regex when `Apply.Create` reads it.
- **Mitigation:** When `Apply.Create` reads `ConfigNameOverride`, re-validate it against the same regex (`validateInitName` or its workspace-package equivalent). If validation fails, error out rather than using the value. This is cheap and closes the persistence-boundary attack.
- **Already covered in design:** no (the design's Security Considerations section dismisses the override field with "plain text; it does not store credentials" but does not consider the override's role as a downstream input)

### Issue 7: Regex evaluation is anchored, but the regex itself permits `_` only in middle/end (this isn't a security issue, ignore — actual concern below)
- **Severity:** low
- **Description:** The regex `^[a-zA-Z0-9._-]+$` is correctly anchored and ASCII-only, so it does inherently exclude:
  - All control characters (no Unicode class needed because the set is positively allowlisted).
  - All Unicode bidi/RTL/LTR override characters (ditto).
  - Newlines, tabs, NUL bytes (ditto).
  This is more restrictive than the existing `validRegistryName` (which uses a Unicode denylist). Good.
  However: cobra parses argv before validation, and Go's argv handling is byte-clean (no NUL stripping), but if `<name>` ever flows through a layer that decodes percent-encoding or backslash-escapes (e.g., a future remote-trigger or RPC interface), the upfront regex check on the post-decode string is what matters. The design's location of the validation (in `runInit` after cobra parsing, before any filesystem touch) is the right point in the call chain — but the design should document that the validation must run on the post-decode string at every entry point, not just `niwa init`'s CLI.
  This is forward-looking guidance, not a current vulnerability.
- **Mitigation:** Add a sentence to Security Considerations stating that all entry points that ingest `<name>` (current or future) must run `validateInitName` after any decoding step. Make `validateInitName` exported from `internal/cli` or a shared package so future callers reuse it.
- **Already covered in design:** no

### Issue 8: Error messages disclose absolute filesystem paths (information disclosure)
- **Severity:** low
- **Description:** The error format `"<absolute-path> already exists (<qualifier>)"` includes the absolute path, which contains the username (e.g., `/home/danielgazineu/foo` or `/Users/danielgazineu/...`). In CI/log-aggregation contexts, this can leak the operator's username. niwa likely already does this throughout its codebase, so the change doesn't introduce new disclosure beyond existing convention. The rebind warning (R8) is more notable: it prints two `Root` paths (previous and new), which could disclose private project locations to anyone who reads stderr (e.g., a CI log uploader, a screen recording, a paste into a chat).
- **Mitigation:** This is a pre-existing convention and changing it is out of scope for this PRD. Document in Security Considerations that paths in errors include the username and may include sensitive parent-directory components, and that operators in regulated environments should be aware. No code change required.
- **Already covered in design:** no

### Issue 9: Clean-break stance leaves no defense for users misinterpreting old patterns
- **Severity:** low (UX-adjacent, mentioned for completeness)
- **Description:** The design relies on the success-message absolute path to surface unintended nesting (`<cwd>/foo/foo/`). A user running the old pattern (`mkdir foo && cd foo && niwa init foo`) gets a successful init at `foo/foo/`, sees the success message, but if the message scrolls off-screen (e.g., in a script that pipes init output), the user wouldn't notice the nesting. From a security perspective, this is a low-severity issue because the workspace is still under the user's control; from a UX perspective, the design has acknowledged this trade-off.
- **Mitigation:** Out of scope per PRD; recorded for completeness.
- **Already covered in design:** yes (PRD Known Limitations explicitly accepts this)

## Additional Notes

- **Permissions on the workspace dir.** `0o755` is a reasonable default but world-readable. If the workspace's `.niwa/` ends up containing vault-resolved env vars (per niwa's overlay model), those would be world-readable too. The design should verify (or note) that downstream writers (Apply, vault provider) re-tighten permissions where credentials land. This is mostly an apply-time concern, not an init-time concern, but the choice of `0o755` for the workspace root is the upstream gate.

- **`os.MkdirAll` and parent-creation semantics.** `MkdirAll` creates the entire path including missing intermediate components. If `<cwd>` is somehow not the cwd by the time `MkdirAll` runs (Go's `os.Getwd` can be stale if another goroutine `Chdir`s), `MkdirAll` could create the workspace at an unexpected location. The design should pin the cwd snapshot taken at the start of `runInit` and use only the snapshotted path. This is likely already the case (the design references `cwd, err := os.Getwd()` once at line 118), but worth confirming.

- **The override note (R4) on stderr.** This is fine. The note quotes the user-supplied name and the cloned config's name, both of which have already passed validation by that point. No injection risk.

- **`ErrTargetDirExists` does not mention path-traversal-via-symlink-in-parent.** The error fires when `<cwd>/<name>` itself exists. It does not detect a case where `<cwd>` contains a symlink in its own ancestry that points into an attacker-controlled location. This is largely the user's responsibility (the user picked their cwd) but worth noting in the design as a non-goal.

## Summary

The design is largely sound and the author's three-point Security Considerations section addresses the obvious threats. However, several non-trivial issues warrant attention before merge: (a) the registry-rebind path needs a stronger consent gate or an explicit security trade-off statement (Issue 2), (b) the override field should be re-validated when read by Apply.Create (Issue 6), and (c) the TOCTOU window has a more severe symlink-into-attacker-dir variant than the design acknowledges (Issue 3). The remaining issues are documentation gaps or low-severity defense-in-depth opportunities. Approve with changes — none of the issues are blockers if the author chooses to accept the trade-offs explicitly, but each should be addressed in the design's Security Considerations section so reviewers can audit the choices.
