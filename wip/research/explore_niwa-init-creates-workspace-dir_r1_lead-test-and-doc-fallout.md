# Lead: What test and documentation fallout does this change produce?

> Reconstructed from agent in-context summary (the agent did not have Write tool access).
> **Caveat:** the agent's headline claim that "20+ functional test scenarios already
> expect the new behavior" is incorrect — it conflated niwa **instances** (subdirs
> created by `niwa create` after `niwa apply`) with the workspace directory itself.
> Verified: `critical-path.feature:29` "the instance \"myws\" exists" refers to the
> apply-created instance, not the workspace root, and is unrelated to the init UX
> change. Treat the test-fallout findings below as a starting list, not as
> evidence that work is already partially done.

## Findings

### 1. Functional tests (Gherkin)

Affected feature files (contain `niwa init from config repo "<name>"` steps):
- `test/functional/features/critical-path.feature`
- `test/functional/features/workspace-config-sources.feature`
- `test/functional/features/workspace-imports.feature`
- `test/functional/features/parallel-clones.feature` (per agent grep)

The Gherkin step "I run niwa init from config repo `<name>`" is implemented in
test step Go code that today shells out to `niwa init <name> --from <fixture>`
inside a temp workspace dir. The agent did not quote the exact step
implementation, but the affected step is the one that needs to either:
- Stop pre-creating / `cd`-ing into a target dir before invoking init, or
- Adapt to the new "init creates the folder" flow.

A `@critical` Gherkin scenario for the new behavior should be added per the
project's testing convention (CLAUDE.md notes: "When you ship a user-facing
CLI command... add a `@critical` Gherkin scenario").

### 2. Unit tests

`internal/cli/init_test.go`:
- `TestRunInit_NamedMode` — exercises `niwa init <name>`. Assertions check
  `os.Stat(filepath.Join(dir, workspace.StateDir))` where `dir` is the
  test's temp cwd. After the change, `.niwa/` lands at
  `filepath.Join(dir, name, workspace.StateDir)`. Assertions need updating.
- `TestRunInit_ScaffoldMode` — no positional name; behavior unchanged.
- Conflict tests — need a new case for "target dir already exists" (the new
  `ErrTargetDirExists` sentinel, see lead 2).
- `resolveInitMode` table-driven cases — unchanged (mode resolution logic
  isn't affected by the directory question).

`internal/workspace/preflight_test.go`:
- Existing cases (workspace exists, orphan .niwa/, inside instance) stay valid.
- Add a case verifying the "target dir already exists" behavior, regardless of
  whether the existence check lives in the caller or in `CheckInitConflicts`.

### 3. Documentation

- **`internal/cli/init.go` Long help text** (lines 57-72): currently describes
  the three modes assuming "in the current directory." Needs rewording for
  "creates `<cwd>/<name>` and initializes inside it" when a positional name
  is given.
- **`README.md`** (project root): per the agent, contains examples of the
  `mkdir foo && cd foo && niwa init foo --from src` pattern. Needs updating
  to the new flow.
- **`docs/guides/`** — needs a grep pass; the agent did not enumerate hits.
- **`tsuku.dev` website** — niwa is a separate repo from tsuku, so unlikely
  to have `niwa init` examples on tsuku.dev. Quick grep confirms before
  shipping.

### 4. New tests to add

- Unit: `niwa init <name>` creates `<cwd>/<name>/.niwa/workspace.toml`.
- Unit: `niwa init <name>` errors when `<cwd>/<name>` already exists (file
  or directory).
- Unit: `niwa init` with no positional name still inits in cwd (regression
  guard for the no-name path).
- Unit: `niwa init <name> --from <src>` registers `Root = <cwd>/<name>` so
  `niwa go <name>` lands correctly.
- Functional `@critical`: end-to-end flow showing the new init UX from a
  parent dir.

## Implications

The test work is mostly mechanical updates to existing assertions plus a
small set of new cases. No broad rewrite. Documentation work concentrates on
the `init.go` help text and the project README.

Effort sizing: low-to-medium. Roughly
- 2-3 unit test files to update, plus 4-5 new test cases
- 1 functional feature scenario to add (and possibly minor adjustments to
  existing scenarios' setup steps)
- 2 doc updates (init.go help, README) plus a `docs/guides/` grep

## Surprises

The agent's headline claim about pre-existing test expectation alignment was
incorrect (terminology confusion between workspace and instance). Real test
fallout is what the tables above describe — modest but non-trivial.

## Open Questions

1. What does the Gherkin step "I run niwa init from config repo" do under the
   hood today (where does it cd to)? Step implementation needs to be read
   before deciding how to adapt it.
2. Are there any guides under `docs/guides/` that bake in the old flow?
   Needs a grep pass at implementation time.
3. Are there examples of `niwa init <name>` (without `--from`) anywhere in
   docs? That mode is also affected.

## Summary

Test fallout is concentrated in `internal/cli/init_test.go` (named-mode
assertions need to look at `<cwd>/<name>/` instead of `<cwd>/`) and a
`@critical` Gherkin scenario per project convention. Documentation updates
hit `init.go` help text and the project README. The agent's claim that 20+
existing tests already expect the new behavior was a terminology mix-up
(niwa instance ≠ workspace dir) and should be discarded — real fallout is
modest but non-trivial.
