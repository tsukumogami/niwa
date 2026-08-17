# Lead: Is niwa's functional suite the only place in the tsukumogami organization where a test can reach a real repository?

## Findings

### public/niwa — the known-bad fixture, precisely located

`test/functional/suite_test.go:117-147` is the shared-path root cause. Per scenario:
- `sandbox := filepath.Join(repoRoot, ".niwa-test")` (line 117) — a path **inside the real checkout** (`repoRoot` is the niwa repo's own working tree), not a temp dir.
- `os.RemoveAll(sandbox)` (line 118) then `os.MkdirAll(sandbox, 0o755)` (line 119) — wipe-and-recreate, unguarded by any lock.
- `wsParent := filepath.Join(os.TempDir(), "niwa-test-workspaces")` (line 145) — a second fixed shared path, this time under `/tmp`, with the same `RemoveAll`+`MkdirAll` pattern at lines 146-147.

`test/functional/localrepo_test.go:51-112` (`createRepoWithFiles`) is the fixture that turns that race into a HEAD-repoint. It creates a bare repo under `s.root` (itself under the sandbox), then `workDir, _ := os.MkdirTemp(s.root, "clone-*")` (line 63), clones into it (line 69), and runs `git add`/`git commit`/`git push` with `cmd.Dir = workDir` set (lines 90-109) — no `GIT_DIR`, no `GIT_CEILING_DIRECTORIES`. `symbolic-ref HEAD refs/heads/main` also runs bare-repo-scoped via `-C repoPath` (lines 36, 57) with no ceiling. When a concurrent run's `RemoveAll(sandbox)` deletes `s.root` (and therefore `workDir`) out from under a live `git add`/`commit`/`push` sequence, and the concurrent run's `MkdirAll` recreates the sandbox as a plain, non-git directory before the deleted-command's git binary walks upward looking for `.git`, discovery climbs past the vanished `workDir` all the way to the real niwa checkout's `.git`. This is exactly the incident's described mechanism, confirmed by direct read of both files.

`session_steps_test.go:696-697`, `worktree_delegation_steps_test.go:152,278`, `steps_workspace_config_sources_test.go:60,83,208` all run further git subcommands inside directories built under the same fixed `sandbox`/`wsParent` roots — they inherit the same exposure because they share the sandbox, not because each independently mis-scopes `cmd.Dir`.

All other niwa unit tests (16 files with `exec.Command("git"...)` and 5 files with `RemoveAll` outside `test/functional`/`test/live`) were read individually and are **safe**: every one operates on a path rooted at `t.TempDir()` (or a symlink-resolved wrapper, `canonicalTempDir(t)`, in `internal/cli/destroy_test.go` and `internal/cli/init_test.go`). No fixed or repo-relative path appears in any of them. `test/live/dispatch_live_test.go` (opt-in, not run by default) is also `t.TempDir()`-scoped.

A repo-wide grep confirms `.niwa-test` and `niwa-test-workspaces` appear **only** in `test/functional/suite_test.go` and the `Makefile`'s cleanup targets (`rm -rf .niwa-test` in `clean`/`test-functional*`) — the mechanism does not recur anywhere else in niwa.

### public/koto — one structurally similar but safely-scoped harness, plus an unrelated fixed-path nit

`test/functional/suite_test.go` (koto) runs the same shape of Gherkin/godog harness as niwa — `git init`/`add`/`commit`/`checkout -b` with `cmd.Dir` set to a scenario directory — but the directory comes from `os.MkdirTemp("", "koto-func-*")`, i.e. the OS temp dir, never a path inside the koto checkout. A raced deletion here has nowhere real to climb into. Scenarios run sequentially (no godog `Concurrency` override), and `os.RemoveAll` cleanup in `ctx.After` only ever targets that scenario's own directory after its own git calls finish. Verdict: safe, not the same bug class.

Separately, `koto/tests/integration_test.rs:3695` and `:3736` write and remove fixed sentinel files at `/tmp/koto-test-42` and `/tmp/koto-test-99999` — not git-related, but a fixed-shared-path pattern that could collide if two CI jobs land on the same host concurrently. Worth a hygiene ticket, not a guardrail.

### public/shirabe — git-touching test surface is the largest outside niwa, and all of it pins scope correctly

Rust integration tests (`crates/shirabe/tests/transition_repoint.rs`, `transition_parity.rs`) and shell fixtures (`retry-clearing_test.sh`, `run-cascade_test.sh`, `settled-branch-record_test.sh`, `check-citations_test.sh`) build throwaway repos under `std::env::temp_dir()`/`TempDir`/`mktemp -d`, and invoke git with explicit `-C <root>` rather than ambient `cmd.Dir` discovery. One test (`absorption_corpus.rs`'s `no_existing_document_was_edited`) does run git with `current_dir()` pointed at the real checkout root, but only for a read-only `git status --porcelain` — never a mutating command, never combined with a delete. No parallel test execution found. Verdict: safe.

### public/dot-niwa and public/.github — zero test code

Both repos contain no test suites and no scripts; their CI workflows are thin `uses:` delegations to shirabe's reusable workflows with no local git or `rm -rf` steps. A shared guardrail would be dead weight in both.

### public/tsuku — zero git-in-test invocations anywhere in the monorepo

No `exec.Command("git"...)` (or any other git invocation) exists in any Go, shell, or JS/TS test file in tsuku. All 15 `RemoveAll`-flagged files either (a) call the unrelated domain method `mgr.RemoveAllVersions` (an installed-tool-version cleanup, not `os.RemoveAll`), or (b) call real `os.RemoveAll` on paths built from `t.TempDir()`/`os.MkdirTemp()`, directly or via `testutil.NewTestConfig`. The one functional suite (`test/functional/suite_test.go`) already gives each scenario its own `os.MkdirTemp`-derived home instead of a shared fixed root, specifically (per an in-code comment) to avoid exactly the shared-path race class that hit niwa — a background `tsuku check-updates`/`apply-updates` process outliving a scenario was the concern that drove that design. Root `Makefile`'s `rm -rf .tsuku-dev .tsuku-test` is a fixed relative path, but it's an isolated `$TSUKU_HOME` override, never combined with a git command in the same target. Verdict: clean.

### Private repos (summarized; see note on visibility below)

Of the organization's private repositories, one Go-based repo has git/`cmd.Dir`/`RemoveAll` call sites in its test suite and shell tooling, but every one of them roots the volatile path at `t.TempDir()`, `os.MkdirTemp`, or `mktemp -d`, or gates destructive removal behind a marker-file check / clone-then-swap pattern before any `rm -rf` fires — none reproduce the fixed-path-plus-unguarded-race shape. A second, docs/config-heavy private repo has one archived (not actively run) Go tool with the same safe `os.MkdirTemp`-rooted pattern. The remaining two private repositories have no test code or scripts touching git or fixed paths at all — zero hits on every relevant grep pattern.

## Risk table

| Repo | Site | Fixed shared path? | Unguarded race with delete/mkdir? | Can escape to real repo? | Severity |
|---|---|---|---|---|---|
| niwa | `test/functional/suite_test.go:117-119` (`sandbox`) | Yes — `<repo>/.niwa-test` | Yes — `RemoveAll`+`MkdirAll`, no lock | Yes (confirmed by incident) | Critical |
| niwa | `test/functional/suite_test.go:145-147` (`wsParent`) | Yes — `/tmp/niwa-test-workspaces` | Yes — same pattern | Yes, same mechanism | Critical |
| niwa | `test/functional/localrepo_test.go:90-109` (`git add/commit/push`, `cmd.Dir=workDir`) | Indirectly — workDir nests under the fixed sandbox | Yes, if sandbox wiped mid-sequence | Yes — this is the exact commit/push that landed on real `main` | Critical |
| niwa | all unit tests outside `test/functional`/`test/live` | No — `t.TempDir()`/`canonicalTempDir` throughout | No | No | None |
| koto | `test/functional/suite_test.go` | No — `os.MkdirTemp("", ...)` (OS temp dir) | Sequential scenarios, own-dir cleanup only | No | None |
| koto | `tests/integration_test.rs:3695,3736` | Yes — fixed `/tmp/koto-test-*` sentinel files | Only across concurrent hosts/CI jobs, not git-related | No (not git) | Low (hygiene) |
| shirabe | Rust/shell fixture tests | No — TempDir/mktemp, explicit `-C` | No | No | None |
| shirabe | `absorption_corpus.rs` real-checkout `git status` | N/A (read-only) | N/A | N/A (no mutation) | None |
| tsuku | entire monorepo | No | No | No (no git invocation exists) | None |
| dot-niwa, .github | — | — | — | No test code at all | None |
| private repos (generic) | assorted git/RemoveAll sites in one repo's test/tooling | No — TempDir/mktemp/marker-gated | No | No | None |
| private repos (generic) | remaining repos | — | — | No test code at all | None |

## Implications

The fix should be **niwa-shaped**, not org-shaped. The dangerous mechanism — a fixed path inside (or shared alongside) a real checkout, wiped and recreated without a lock, then handed to git commands that rely on upward discovery instead of pinning `GIT_DIR`/`GIT_CEILING_DIRECTORIES` — exists in exactly one place across the whole organization: niwa's `test/functional/` fixture (`suite_test.go` + `localrepo_test.go`). No other repo, public or private, has a test or CI site that combines (a) a fixed non-temp-dir path with (b) an unguarded delete/recreate racing a git command that isn't scope-pinned. Every other git-touching test in the organization already uses `t.TempDir()`/`os.MkdirTemp`/`mktemp -d` (process-unique, no enclosing repo to fall into) or passes an explicit `-C`/pins the working tree.

That said, a **generic backstop** — cheap, narrowly scoped — is worth adding regardless of blast radius, specifically inside niwa's own test helper: have `createRepoWithFiles` (and any future fixture like it) set `GIT_CEILING_DIRECTORIES` or `GIT_DIR`/`GIT_WORK_TREE` explicitly whenever it shells out to git, so upward discovery can never leave the intended sandbox even if the sandbox path itself is later hardened (e.g. moved off `<repo>/.niwa-test` onto a `t.TempDir()`-style root). That's a one-file, one-package fix, not an org-wide library.

Building a shared cross-repo helper (e.g. a `niwa`-published Go package other repos import in tests) would be over-built: koto, shirabe, and the private Go-based repo already independently arrived at the correct pattern (temp-dir roots, explicit `-C`, marker-gated deletes) without needing one, tsuku and shirabe's markdown/config-only siblings have no git-in-test code to guard at all, and dot-niwa/.github have no test code whatsoever. The koto `/tmp/koto-test-*` sentinel-file hygiene issue is unrelated to the git-clobber class and doesn't change this conclusion — it's a plain fixed-temp-name collision risk, not a repo-escape risk, and belongs in a separate low-priority ticket if pursued at all.

## Surprises

- niwa's own functional suite already has a documented awareness of this exact race class **inside a different repo**: tsuku's `test/functional/suite_test.go` gives each scenario its own `os.MkdirTemp`-derived home directory specifically because a detached background process could outlive a scenario and corrupt a shared fixed home. That lesson was learned and applied in tsuku but not (yet) in niwa's own suite — the fix pattern the org needs already exists as prior art one repo over.
- The niwa fixture's two fixed paths sit at different scopes — one inside the real checkout (`<repo>/.niwa-test`), one under system temp (`/tmp/niwa-test-workspaces`) — but both are equally vulnerable because neither is process-unique; only the first one is what let discovery reach a *real* repo, though a future change that made the sandbox non-repo-relative alone wouldn't fully close the hole without also pinning `GIT_DIR`, since a `/tmp`-relative fixed path shared by two concurrent runs has the identical race shape, just a lower-value target to land on.

## Open Questions

- Does niwa's functional suite ever run scenarios concurrently by design (e.g. `go test -parallel`), or did the incident require two *separate* `make test-functional` invocations racing each other (two developers, or a developer plus CI)? This changes whether the fix should also address `t.Parallel()` usage inside the suite itself, versus only cross-process concurrent runs.
- Should the koto `/tmp/koto-test-*` sentinel-file hygiene item be filed as a follow-up issue, or is it out of scope for this investigation's remit (git-repo-clobber specifically, not general fixed-path collisions)?

## Summary

Only niwa's functional-test fixture (`test/functional/suite_test.go` + `localrepo_test.go`) combines a fixed non-temp path inside the real checkout with an unguarded wipe-and-recreate racing unscoped git commands — no other repo in the organization, public or private, reproduces that shape, since every other git-touching test uses a process-unique temp dir or explicit `-C` scoping. The fix belongs entirely inside niwa: relocate the sandbox off the repo-relative path and/or have the fixture pin `GIT_CEILING_DIRECTORIES`/`GIT_DIR` explicitly, rather than building an org-wide shared helper that koto, shirabe, tsuku, and the private repos don't need. One unrelated hygiene nit surfaced in koto (fixed `/tmp/koto-test-*` sentinel files, not git-related) is worth a separate low-priority ticket if pursued.
