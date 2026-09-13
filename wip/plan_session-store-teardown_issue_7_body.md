---
complexity: simple
complexity_rationale: No production code changes -- Gherkin scenarios built mostly on step definitions the dispatch and worktree suites already ship, a handful of new steps, and two guide sections rewritten to match behavior Issues 3 and 6 already landed and unit-tested.
---

## Goal

Cover destroy by session id and by handle from the workspace root end to end, recompose the four-way parallel dispatch against a local `file://` config source, and bring `docs/guides/worktree.md` and `docs/guides/workspace-config-sources.md` in line with the new id forms, outcome table, root behavior, lock file and staging layout.

## Acceptance Criteria

- [ ] A new `test/functional/features/worktree-teardown-by-session.feature` sets its workspace up through the existing steps `a clean niwa environment`, `a local git server is set up`, `a config repo "<name>" exists with body:` (declaring one `[repos.app]` entry), `I run niwa init from config repo "<name>"` and `a fake claude for dispatch with session "<uuid>"`, then dispatches with `--detach` from the workspace root, so every scenario runs against a local (`file://`) config source with no GitHub fake and no network.
- [ ] A scenario destroys by session id from the workspace root: with one active niwa-managed worktree on a merged branch in the dispatch instance, `niwa worktree destroy <session-id>` run through `I run "..." from the workspace root` exits 0, stdout carries exactly one `session: destroyed` line naming the worktree id and repo, the lifecycle record is ended, the worktree directory is gone, and the branch is gone from the clone.
- [ ] A scenario destroys the same setup with an unmerged branch: exit code 0, the worktree directory removed, the branch still present in the clone, and stderr carries exactly one line containing `was not deleted (unmerged commits remain)`.
- [ ] A scenario passes the session's recorded handle instead of its session id from the workspace root and asserts the same outcome as the session-id scenario, so the R4 handle route is exercised distinctly from the session-id route.
- [ ] Every destroy scenario above asserts what survives: `the session mapping exists for session "<uuid>"`, `the dispatch instance still exists`, and the cloned repository is still present in the dispatch instance.
- [ ] A scenario covers the root contract for values that resolve to nothing and for `list`: from the multi-instance workspace root an 8-hex value matching no worktree and no session exits 3 with `no worktree or session matches "<value>"` on stderr, and `niwa worktree list` prints the R10 root message on stderr, prints no table, and exits 0.
- [ ] The four-parallel-dispatch scenario in `test/functional/features/session-message-acceptance.feature` is recomposed to initialize from a config repo on the local git server instead of `niwa init` scaffold-in-place, dropping its `the niwa machine config is replaced with:` workaround, and its comment block is rewritten: the paragraph explaining that a non-GitHub config source was avoided because parallel refreshes collide in the shared staging directory (#297) is replaced by one saying the ordering now holds, that this scenario is the PRD's single probabilistic command-level check, and that the forced tests in `internal/workspace` and `internal/cli` carry the determinism.
- [ ] The recomposed scenario keeps its existing assertions (`all parallel runs exit 0`, the notice marker, exactly one audit line per transcript, four mappings recording acceptance, config byte-for-byte unchanged) and adds that the four mappings name four distinct instance directories that all exist and that `niwa list` reports all four instances.
- [ ] New step definitions are added only for what the suite lacks -- creating a worktree in the recorded dispatch instance, committing a change in the last worktree so its branch is unmerged, asserting the lifecycle record state and worktree directory for the dispatch instance, and asserting four mappings point at four distinct existing instance directories -- each registered in the suite and documented with a comment; no existing step is renamed, removed, or changed in behavior.
- [ ] `docs/guides/worktree.md` section `### niwa worktree destroy <id> [--force]` documents all four target forms (worktree id, session id, handle, `--by-path`), the exactly-8-lowercase-hex prefix fallback for a mapping that records no handle, that a session target destroys every active worktree of its instance in worktree-id order and continues past a refusal, that `--force` with a session id or handle is a usage error (exit 2), and that the mapping, the instance and its clones are never removed.
- [ ] The same section carries the R9 outcome table -- each outcome with its exit code (0, 1, 3, 4), the stream each line goes to, and the exact `session: destroyed ...`, `session: nothing to destroy: ...` and `no worktree or session matches ...` wording -- and notes the one behavior change for existing invocations, that a worktree id matching nothing now exits 3 instead of 1.
- [ ] `docs/guides/worktree.md` documents worktree subcommands at a multi-instance workspace root: `destroy` resolves a session there, `list` prints the redirect on stderr and exits 0, and `create`, `apply`, `attach`, `detach` and `niwa go` print the same message on stderr and exit 1; it states that the single-instance layout is unchanged.
- [ ] `docs/guides/workspace-config-sources.md` section `### Atomic refresh` is rewritten to match the implemented swap: the sibling `<dir>.lock` file (regular file, 0600, never removed), the private `.niwa.next-<random>/` staging directory in place of the fixed `.niwa.next/`, the fetch running unlocked with only carry-over and the two renames under the exclusive lock, recovery rather than preflight cleanup repairing an interrupted swap, `.niwa.trash-*` deleted after the lock is released, the deliberately kept `.niwa.stray-*`, and the 30-second bound whose error names the directory; the stale claims that cleanup is an idempotent preflight and that niwa never reads `.niwa/` mid-swap are removed or corrected.
- [ ] `go test ./test/functional/...` passes on Linux with the new and recomposed scenarios, `go vet ./...` is clean, and no committed file in this issue references a `wip/` path.

## Dependencies

Blocked by <<ISSUE:3>>, <<ISSUE:6>>

## Downstream Dependencies

None
