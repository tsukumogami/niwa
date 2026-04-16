# Testability Review

## Verdict: CONDITIONAL PASS
The acceptance criteria are largely testable for happy-path and failure scenarios, but several criteria lack sufficient specificity for deterministic verification, and some implementation details are underspecified.

## Untestable Criteria

1. **AC: "After `niwa config set private <inaccessible-repo>`, `niwa apply` produces no error and no output related to the companion."**
   - Problem: "No output related to the companion" is vague. Does this include debug/verbose logs? Does it include STDERR? What is the acceptable error surface?
   - How to make testable: Specify exact output channels (STDOUT vs STDERR vs debug logs) and define what constitutes "output related to the companion." Example: "Standard STDOUT and STDERR contain no mention of the companion; debug logs may include it."

2. **AC: "First `niwa apply` with a private companion registered on a machine where the companion was never cloned: if clone fails (auth error, not found, network error), apply completes successfully with public repos only. No error or warning output related to the companion."**
   - Problem: Same vagueness as above, plus the criterion conflates three failure modes (auth, not found, network) without specifying if they all behave identically.
   - How to make testable: List each error type separately with expected behavior. Example: "(a) GitHub 404 or 403 — skip silently; (b) network timeout — skip silently; (c) [list others]. All cases produce no STDOUT/STDERR about companion."

3. **AC: "Companion declaring `[[sources]] org = "tsukumogami"` (same org as public config), and public config also declaring `[[sources]] org = "tsukumogami"`: `niwa apply` aborts with a duplicate-source-org error before any repos are modified."**
   - Problem: "Before any repos are modified" is vague—modified in what way? Git state? Filesystem? What counts as a repo modification?
   - How to make testable: Define the verification point. Example: "No git pulls, checkouts, or file writes occur to any repo on disk; apply fails with exit code 1 and error message matching pattern 'Duplicate source org'."

4. **AC: "Workspace `CLAUDE.md` import order: workspace context appears before `@CLAUDE.private.md`, which appears before `@CLAUDE.global.md` (if global config is also registered)."**
   - Problem: Unclear how to test "appears before" in a parsed file. Is order tested by string search? By parsing import directives? What if the same import is listed multiple times?
   - How to make testable: Specify verification method. Example: "Read CLAUDE.md and verify line numbers: @workspace import < @CLAUDE.private.md < @CLAUDE.global.md (if present)."

5. **AC: "`workspace-extension.toml` with `files` containing a destination path of `../../.ssh/authorized_keys` is rejected at parse time with an error. No disk writes occur."**
   - Problem: "At parse time" is underspecified—parse time relative to what command? Does `niwa config set private` validate, or only `niwa apply`? "No disk writes" is vague (what about temp files, lockfiles?).
   - How to make testable: Specify when parsing occurs. Example: "When `niwa config set private` is run, or when `niwa apply` runs if the companion was pre-registered, the parse fails before any file operations. Exit code is 1; no files written to user directories."

6. **AC: "`workspace-extension.toml` with `env.files` containing an absolute path is rejected at parse time with an error."**
   - Problem: Same issue as above—when does parsing occur? What defines an absolute path (does `/` count, or only on POSIX)?
   - How to make testable: Specify absolute path definition and when rejection occurs. Example: "Paths starting with / are rejected as absolute. Rejection occurs during `niwa apply`. Exit code 1; error message matches pattern 'absolute path not allowed'."

## Missing Test Coverage

1. **Merge precedence with multiple sources of the same name**: R13–R16 define merge semantics, but there's no AC testing what happens when the companion defines sources that result in duplicate repos after merge (e.g., both public and private source the same GitHub org explicitly). Is there an error, silent deduplication, or user visibility?
   - Missing: "Companion and public config both explicitly list the same repo in `[[sources]]` (e.g., via different org declarations): repos are deduplicated or error occurs [specify which]."

2. **Hook script execution in companion**: R22 says hook scripts are resolved to absolute paths using the companion's local directory. No AC tests that hooks execute correctly or that relative paths are resolved correctly.
   - Missing: "Companion with `[claude.hooks] on_apply = ["scripts/setup.sh"]`: hook script resolves to companion's directory and executes (or is skipped if hook succeeds, depending on config)."

3. **Env file merging**: R11 mentions `env files append, env vars per-key`, but no AC validates this merge behavior.
   - Missing: "Companion with `env.files = ["private.env"]` and public config with `env.files = ["public.env"]`: both files are sourced in order; later values override earlier values."

4. **Invalid TOML in companion**: What happens if `workspace-extension.toml` has syntax errors (not just path traversal or other validation errors)?
   - Missing: "Companion with malformed TOML in `workspace-extension.toml`: apply fails with a TOML parse error."

5. **Companion without required fields**: What if the companion repo is cloned but `workspace-extension.toml` is missing? R9 says it must exist, but there's no AC for this error case.
   - Missing: "Companion cloned successfully but lacks `workspace-extension.toml`: apply fails with an error [specify message]."

6. **Sync behavior details**: R5 says "equivalent to the global config sync step," but sync behavior is not tested. What does "sync" mean—git pull, git fetch + reset, git clone on first run?
   - Missing: "After companion clone succeeds, subsequent `niwa apply` runs `git pull` (or equivalent sync) on the companion repo."

7. **CLAUDE.private.md placement edge case**: The AC says it's "placed after the workspace context import and before the global config import," but what if the workspace `CLAUDE.md` doesn't import context or global config yet? Does the injection still occur correctly?
   - Missing: "Companion with `CLAUDE.private.md` and workspace `CLAUDE.md` with no existing imports: injection adds `@CLAUDE.private.md` to the file."

8. **Registration persistence**: The AC checks that `niwa config set private` stores the URL, but doesn't verify it persists across sessions or machine reboots.
   - Missing: "After `niwa config set private <repo>`, the registration persists across new shell sessions and `niwa apply` invocations."

9. **Error message specificity**: R7 specifies an example error message for sync failure, but the AC doesn't verify the exact message format or that it includes the companion identifier.
   - Missing: "When private companion sync fails after successful clone, error message includes the companion repo identifier or URL."

10. **Quiet failure for first-time clone**: The AC says "no error or warning," but doesn't specify whether diagnostics can be gathered (e.g., in debug mode). Can a user troubleshoot why the companion didn't clone?
    - Missing: "First-time companion clone failure is silent in normal output, but can be diagnosed with `--verbose` or `--debug` flags."

## Untestable Scenarios and Edge Cases

1. **Network transience**: R6 and R7 distinguish between "never cloned" and "previously cloned" using local filesystem state. But what if the directory exists but is corrupted (partial git repo)? Is it treated as "previously cloned" and fail, or as "never cloned" and skip silently?
   - Recommendation: Specify the validation logic. Example: "If `$XDG_CONFIG_HOME/niwa/private/.git` exists and is a valid git repository, treat as previously cloned. Otherwise treat as never cloned."

2. **Offline scenarios**: If a user has registered a companion, previously cloned it, but is now offline and tries to apply: does the sync fail fatally (per R7) or should there be a way to apply offline?
   - Recommendation: Add a requirement for offline-mode behavior, or add an AC: "Offline apply with a previously cloned companion: sync fails, user must use `--skip-private` to apply."

3. **Partial success after merge**: If the companion defines 5 repos but 3 fail to clone (due to access or network), does the apply abort or continue with 2? This isn't covered.
   - Recommendation: Add AC for partial failure: "If some repos from the companion fail to clone, apply [aborts / continues with successful repos]. Error message lists failed repos."

4. **Concurrent applies**: What if two `niwa apply` invocations run simultaneously and both try to sync the companion?
   - Recommendation: Add AC: "Concurrent `niwa apply` invocations on the same machine: only one succeeds in updating the companion; others wait or fail with a lock message."

5. **Custom XDG_CONFIG_HOME**: R20 and R8 reference `$XDG_CONFIG_HOME/niwa/private/`, but what if `$XDG_CONFIG_HOME` is not set (defaults to `~/.config`)? Is this testable across platforms?
   - Recommendation: Explicitly define default behavior. Example: "If `$XDG_CONFIG_HOME` is unset, use `~/.config` as default."

## Summary

The acceptance criteria cover the happy path and key failure modes (auth failures, duplicate sources, path traversal), and most criteria are verifiable through file inspection, error message matching, and workspace state checks. However, **6 criteria are vague on verification method or output expectations** (what counts as "no output," when parsing occurs, what "before any repos are modified" means), **10 scenarios are missing coverage** (repo deduplication, hook execution, env merging, malformed TOML, missing workspace-extension.toml, sync behavior, CLAUDE injection edge cases, registration persistence, error message details, debug diagnostics), and **5 edge cases would benefit from explicit specification** (corrupted git repos, offline apply, partial failures, concurrent applies, XDG_CONFIG_HOME defaults).

**Verdict justification**: A test author could write tests for ~70% of the stated criteria, but would need to make assumptions about the remaining 30%. The PRD is implementable, but the test plan would require clarifications on output semantics, error handling edge cases, and sync/merge behavior details before achieving high confidence in coverage.
