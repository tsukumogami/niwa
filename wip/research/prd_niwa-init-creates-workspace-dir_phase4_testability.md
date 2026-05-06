# Testability Review

## Verdict: PASS

The PRD's acceptance criteria are largely concrete, mappable to either Go unit tests or `@critical` Gherkin scenarios in `test/functional/features/`, and cover happy paths, conflict shapes, validation, registry rebind, cross-command consistency, and docs; a handful of minor wording gaps exist but none block writing a working test plan from the AC list alone.

## Untestable Criteria

1. **AC-6 ("`niwa apply` references `my-name` in its output, not `upstream`")**: "References" is vague — does the assertion check stdout, stderr, a log line, an exit-banner, or a structured field? A test author has to guess which substring to grep for. -> Make it testable by naming the surface explicitly: e.g., "the `niwa apply` summary line printed to stdout contains `my-name` and does not contain `upstream`," or pin it to a specific log/output field (`Workspace: <name>`).

2. **AC-8 ("niwa emits a stderr note identifying that `my-name` overrides `upstream`")**: "Identifying" leaves substring choice to the implementer. R4 gives an example string but the AC itself doesn't anchor it. -> Tighten to: "stderr contains both literal tokens `my-name` and `upstream`" (or pin to an exact format string), so Gherkin `Then stderr should contain "..."` steps have something concrete to match.

3. **AC-16 ("The error mentions `..` and reserves explanation")**: "Reserves explanation" is unclear — likely a typo for "reserved name" or "explains the reservation." A test author cannot determine what string to assert on. -> Reword to: "the error message includes the literal token `..` and explains that `..` is reserved," and ideally show an example phrase.

4. **AC-11 ("When `<cwd>/my-ws` is a symlink (to anywhere), `niwa init my-ws` exits non-zero. The error qualifier is `symlink`.")**: Doesn't pin which class of error sentinel applies, unlike AC-9/AC-10 which name `InitConflictError`. R5 implies `InitConflictError` covers symlinks, but a test could be ambiguous about whether dangling vs. resolved symlinks behave the same. -> Add: "regardless of whether the symlink target exists, resolves to a file, or resolves to a directory" and name the error sentinel.

5. **AC-22 / AC-23 / AC-24 (documentation ACs)**: "Explicitly states," "no longer contains," "reflect the new behavior" are testable in spirit (grep against `--help` output and README sections) but vague enough that a reviewer and a test author could disagree on what counts as compliance. -> Tighten to specific anchor strings ("`--help` output contains the literal phrase `creates <cwd>/<name>`" or "README quickstart code block does not contain `mkdir`") so a unit test or doc lint can assert deterministically.

## Missing Test Coverage

1. **R3 (registry surfacing of override name)**: AC-5/AC-6 cover `niwa status` and `niwa apply`, but R3 also calls out "the global registry, the success message, and any other CLI output." No AC asserts that the registry entry stores `my-name` (not `upstream`) after `niwa init my-name --from org/upstream`. -> Add: "After `niwa init my-name --from org/upstream`, `niwa list` (or the registry file inspection) shows `my-name`, not `upstream`."

2. **R4 / AC-8 ("one-time" semantics)**: R4 says "one-time stderr note" but AC-8 only asserts the note appears once on init. Nothing verifies it doesn't reappear on subsequent `niwa status` / `niwa apply` runs. -> Add: "Subsequent `niwa status` and `niwa apply` invocations do not re-emit the override note."

3. **R5 ("No subdirectory of `<cwd>/<name>` may be created" — partial-write rollback)**: AC-9 covers the file-conflict case ("No directory is created at `<cwd>/<name>`") but AC-10 (unrelated directory) and AC-11 (symlink) don't assert the no-write invariant. A test author would need to check the directory state after each conflict shape. -> Add a per-conflict-shape assertion: "`<cwd>/<name>/.niwa/` does not exist after the failed init."

4. **R7 ("the error MUST quote the offending input")**: AC-14 mentions "quoting the offending input" but AC-15/AC-18 don't, and AC-16/AC-17 don't either. A test author could write inconsistent assertions. -> Either lift quoting into a single AC ("All name-validation errors quote the offending input verbatim") or repeat the requirement in each AC.

5. **R8 / AC-19 (registry rebind: instance state shape after rebind)**: AC-19 asserts the registry's `Root` is updated, but doesn't assert what happens to the old directory at `/path/A`. Is it left intact? Removed? A test author has to guess. -> Add: "After rebind, the previous directory at `/path/A` is left intact (no files at `/path/A` are removed or modified)."

6. **R9 / AC-26 (success message format)**: AC-26 says "includes the absolute path" — testable as substring — but doesn't pin the surface (stdout vs. stderr) or whether the path is resolved through symlinks. Functional tests asserting `$cwd/my-ws` may break on macOS where `$TMPDIR` resolves through `/private`. -> Add: "the success message prints to stdout" and "the path is the resolved (symlink-followed) absolute path," matching how niwa elsewhere handles `/var` -> `/private/var`.

7. **R10 / AC-21 (`niwa go` from any directory)**: AC-21 asserts `niwa go my-ws` works "from any directory" — a Gherkin test would naturally pick one directory; the AC implicitly relies on existing `niwa go` behavior. Acceptable, but the AC could note that this is regression coverage for the registry write, not a new `niwa go` feature.

8. **R6 conflict-routing precedence**: AC-12 covers `<cwd>/my-ws/.niwa/workspace.toml` exists (routes to `ErrWorkspaceExists`); AC-13 covers `.niwa/` without `workspace.toml` (routes to `ErrNiwaDirectoryExists`). Neither AC asserts these route ahead of the generic `ErrTargetDirExists` when both apply. -> Add: "When `<cwd>/my-ws/.niwa/workspace.toml` exists, the error is `ErrWorkspaceExists`, not `InitConflictError` / `ErrTargetDirExists`."

9. **No AC for the no-name + existing `.niwa/` case**: R2 says no-name init behavior is unchanged. No AC re-tests that `niwa init` (no args) into a directory that already has `.niwa/workspace.toml` still routes to `ErrWorkspaceExists`. Probably covered by existing tests, but listing it as a regression AC ("AC-2 / AC-3 must not regress") would help a test plan author confirm coverage.

10. **No AC for `<name>` containing only allowed characters but resolving to existing reserved names**: e.g., a name like `con` on Windows. Probably out of scope (niwa is unix-first), but worth a note in Out of Scope if intentionally excluded.

## Summary

The AC list is unusually well-decomposed: 26 criteria spanning directory creation, name override, conflict handling, validation, rebind, cross-command integration, docs, and success messaging — most of which map cleanly to either Go unit tests (preflight checks, name validation, error sentinels) or `@critical` Gherkin scenarios (end-to-end init flows, `niwa go` integration, stderr output). The main weaknesses are wording precision in a handful of "verifies stderr contains a note" criteria (AC-6, AC-8, AC-16) and a few un-asserted invariants implied by the requirements (one-time note, no partial writes, post-rebind cleanup, registry surfacing of override). A test plan author can absolutely produce a working plan from the ACs alone, but would benefit from the implementer pinning a few exact strings and adding ~5 invariant ACs called out above.
