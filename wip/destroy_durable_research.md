# Research: how niwa destroy decides a branch's work is durable

## Research conducted

- Built throwaway repos with local git 2.43.0 and ran 17 isolated scenarios, one fresh repo each. Every scenario was run through four content tests: merge-tree against main's tip, merge-tree against any first-parent main commit since the fork, patch-id, and per-file equality. Scripts: `mt2.sh`, `mt3.sh`, `ws.sh` under `/home/dangazineu/.claude/jobs/e3fd7042/tmp/`. An earlier combined run (`mt.sh`) stacked every scenario on one main, which is itself useful evidence about drift (see 1.4).
- Timed the checks on a synthetic main with 2000 commits since the fork point (`mt3.sh`).
- Measured `git status --porcelain=v1`, stash scope, nested repos and submodules (`st.sh`).
- Read the git-merge-tree manual for 2.38.0, 2.45.0 and the local 2.43 man page. Read the runner-images READMEs for Ubuntu 24.04 and macOS 15 arm64, and packages.ubuntu.com for jammy.
- Read the GitHub REST "pulls" docs. Made four small live `gh api` calls and one `git ls-remote` against tsukumogami/niwa.
- Read niwa code: `internal/cli/{token,destroy,reset,watch,reap,dispatch,instance_from_hook}.go`, `internal/github/{client,pulls,fetch}.go`, `internal/workspace/{scan,destroy,snapshotwriter}.go`, `internal/worktree/worktree.go`, `.github/workflows/test.yml`, `.goreleaser`.

## Findings

### 1. `git merge-tree --write-tree` and the alternatives

**1.1 Version: 2.38 minimum. VERIFIED-from-docs.** The 2.38.0 manual is the first to document `--write-tree`. Its options were `-z`, `--name-only`, `--[no-]messages` and `--allow-unrelated-histories`. `--stdin` and `--merge-base` are absent in 2.38 and present in the 2.45 manual and in the local 2.43 (`--stdin` and `--merge-base` are listed in `git merge-tree -h`). `--quiet` does not exist in 2.43 (MEASURED: `error: unknown option 'quiet'`, rc=129). INFERRED: `--stdin` and `--merge-base` arrived in 2.40, the next manual revision with changes.

**1.2 Output and exit codes. VERIFIED-from-docs, and MEASURED with one deviation.**
- Clean merge prints one line, the tree OID, with rc=0. A conflicted merge prints the tree OID, then `<mode> <oid> <stage> <path>` lines, a blank line and `Auto-merging` / `CONFLICT (...)` messages, with rc=1. `--name-only` prints just the paths.
- The manual says any error exits with something other than 0 or 1. **MEASURED on 2.43: a nonexistent ref exits 1** (`merge-tree: nosuch - not something we can merge`). So rc=1 does not reliably mean "conflict". Unrelated histories exit 128 unless `--allow-unrelated-histories` is passed.
- With `--stdin`, the exit code is 0 for clean and conflicted merges alike. The per-merge status is the first field of each NUL-terminated record (measured: `1 <tree>`).
- It works in a bare repo and never touches the index or working tree (MEASURED).
- Implication: the only safe predicate is "rc==0 AND printed tree == `rev-parse <base>^{tree}`". Anything else means "not proven landed", which is the conservative answer anyway, so the error/conflict ambiguity costs nothing.

**1.3 Scenario matrix. MEASURED, each scenario in its own fresh repo.** "any-point" means: for each first-parent commit X on main after merge-base(main, branch), PASS if `merge-tree X branch` gives X's tree (plus an `--is-ancestor` shortcut).

| Scenario | tip merge-tree | any-point | patch-id --stable (vs any main commit) | per-file equality | Correct answer |
|---|---|---|---|---|---|
| (a) squash-merged, main unchanged | PASS rc0 | PASS | PASS | PASS | pass |
| (a2) squash-merged, main moved on other files | PASS | PASS | PASS | PASS | pass |
| (b) squash-merged + 1 later commit on branch | block rc0 | block | block | block | block |
| (c) main edited the same line after squash | **block rc1 (conflict)** | PASS | PASS | block | pass |
| (c1) main edited an adjacent line after squash | **block rc1 (conflict)** | PASS | PASS | block | pass |
| (c2) main deleted the squashed file later | **block rc0** | PASS | PASS | block | pass |
| (c3) main edited a distant line, same file | PASS | PASS | PASS | **block** | pass |
| (d) branch merged main into itself, then squash | PASS | PASS | PASS | PASS | pass |
| (d2) same, and main re-edited the merged-in line | PASS | PASS | PASS | PASS | pass |
| (e) never merged | block rc0 | block | block | block | block |
| (e2) never merged, conflicts with main | block rc1 | block | block | block | block |
| (g) never merged, main independently has same content | PASS | PASS | PASS | PASS | pass (content is on main) |
| (k) maintainer edited the squash at merge time | block rc1 | block | block | block | block (defensible) |
| (r) squash-merged, then reverted on main | **block** | PASS | PASS | block | debatable: content is in main's history |
| (p) only part of the branch landed | block | block | block | block | block |
| (m) regular merge commit | PASS | PASS (ancestor) | PASS (empty diff) | PASS | pass |
| (rb) rebase-merged | PASS | PASS | PASS | PASS | pass |

Extra measurements:
- **patch-id false block** (`mt3.sh`): main edited a context line within 3 lines of the branch's hunk *before* the squash landed. Branch-diff patch-id `d642125e9aec` vs squash `fdca8fdf5542`, so it blocks. Tip merge-tree passes (rc0, same tree). Patch-id hashes context lines, so any main edit near the hunk between fork and squash breaks it. That is routine in an active repo.
- **patch-id --stable false pass** (`ws.sh`): after the squash, the branch got a whitespace-only commit that turns a Makefile tab into spaces. The `--stable` ids are equal (`cb10d6ff3023` both), so it passes while real work would be lost. `--verbatim` differs, and merge-tree correctly blocks.
- **Drift is the real weakness of tip merge-tree.** In `mt.sh` all scenarios shared one main that kept changing `f`. Plain case (a) then conflicted, because later main edits touched lines next to the squashed hunk. In a busy repo, tip merge-tree produces false blocks that grow with the time since the squash. The any-point variant fixes (c), (c1), (c2), (r) and the drift case because it finds the squash commit itself.

**1.4 False pass / false block summary.**
- Tip merge-tree gives no false passes in these tests. It false-blocks whenever main later touches the same or adjacent lines, or deletes or reverts the work.
- Any-point merge-tree gives no false passes. It false-blocks only when the maintainer edited the squash at merge time or only part of the branch landed. Both are arguably correct blocks. Its "false pass" candidates, (g) and (r), have the content in main's history, so nothing is lost.
- Patch-id: false blocks on context drift before the squash and on edited squashes. **False passes on whitespace-only extra work** unless `--verbatim` is used. It needs a search over main commits to find the squash.
- Per-file equality: false-blocks on any later main edit to a touched file (c3), which is common. It has no false pass here, but the check is coarse.

**1.5 Cost. MEASURED** (2000 commits on main since the fork, squash at #1000):
- a single tip merge-tree takes about 6 ms;
- `--stdin` batch over all 2000 first-parent commits takes about 214 ms;
- limiting candidates with `git rev-list --first-parent mb..main -- <paths the branch touched>` found 1 candidate in 11 ms, and the whole check took 15 ms.

Path-limiting is sound because the squash commit must touch the branch's paths. `--stdin` needs about 2.40 or later; the path-limited loop only needs 2.38.

**1.6 Runner and platform git versions. VERIFIED-from-docs.**
- niwa CI matrix: `[ubuntu-latest, macos-latest]` in `.github/workflows/test.yml`. Releases ship linux and darwin only (`.goreleaser`).
- runner-images READMEs: Ubuntu 24.04 has **Git 2.55.0**; macOS 15 arm64 (image 20260907) has **Git 2.55.0**.
- **Ubuntu 22.04 (jammy-updates) ships git 2.34.1** (packages.ubuntu.com, `1:2.34.1-1ubuntu1.17`). jammy is still in standard support until 2027, so a supported user platform can have git older than 2.38.
- INFERRED, not checked this session: Debian 11 (2.30) is below 2.38 and Debian 12 (2.39) is above. Current Apple Xcode CLT git is 2.39 or later.
- niwa documents no minimum git version anywhere (grep found none).

### 2. GitHub REST: listing PRs by head branch

- **VERIFIED-from-docs:** `head` is documented as "user:ref-name or organization:ref-name" and `state` takes open/closed/all (default open). The docs say nothing about deleted head branches. `GET /repos/{o}/{r}/pulls/{n}/merge` returns 204 when merged and 404 when not.
- **MEASURED (live, tsukumogami/niwa):**
  - PR #312's head branch `fix/306-tar-exec-bits` is deleted (`git ls-remote` returned 0 refs). `GET /pulls?state=all&head=tsukumogami:fix/306-tar-exec-bits` still returns it, with `merged_at`, `head.sha` and `merge_commit_sha` set. **Merged PRs remain queryable by head after the branch is deleted.**
  - `head=tsukumogami:no-such-branch-xyz` returns `[]`.
  - **Trap: `head=fix/306-tar-exec-bits` without the `owner:` prefix is silently ignored and returns 30 unfiltered PRs.** A client that forgets the prefix would match an unrelated merged PR.
  - Fields seen: `merged_at` (null when not merged), `merge_commit_sha` (null when unmerged, despite the doc summary omitting it), `head.sha`, `head.ref`, `head.label`, and `head.repo.full_name`. For fork PR #308 that was `team-humaki/niwa`.
- **INFERRED:** `head.sha` on a merged PR is frozen at the merged head. Pushes to a same-named branch after the merge don't change it; they would open or attach to a new PR. So a valid check is "local branch tip == merged PR's `head.sha`, or an ancestor of it". A local tip with commits past `head.sha` means unmerged work. Several PRs can share a head name (branch reused), so match on sha, not just the name.
- **INFERRED:** `head.repo` can be null when the fork was deleted. niwa's `GetPullHead` already treats it as `*struct` (nullable).
- Auth: unauthenticated calls get 60 req/hr per IP. Private repos need a token with pull-request read access. The docs as summarized did not state the fine-grained permission name, so treat it as ASSUMED.

### 3. niwa plumbing (VERIFIED-from-code)

- **Token:** `resolveGitHubToken()` in `internal/cli/token.go` reads `GITHUB_TOKEN`, then `GH_TOKEN`, then runs `gh auth token` with no timeout. It returns "" on failure, which leaves the client unauthenticated.
- **Client:** `github.NewAPIClient(token)` in `internal/github/client.go`. BaseURL comes from `NIWA_GITHUB_API_URL`, defaulting to `https://api.github.com`. `HTTPClient` is `http.DefaultClient`, **which has no timeout**, so callers must pass a context with a deadline.
- **httptest is the established pattern:** `internal/cli/init_bootstrap_registry_test.go:44` does `t.Setenv("NIWA_GITHUB_API_URL", ghSrv.URL)`, and `watch_*_test.go` and `providerauth_test.go` also use `httptest.NewServer`. The existing methods `GetPullHead` and `CompareCommits` in `internal/github/pulls.go` are the models to follow. A new `ListPullsByHead` fits there.
- **Remote URL parsers:**
  - `ownerRepoFromGitURL` in `internal/cli/watch.go:1052` handles scp and URL forms, but it **ignores the host**, so it would return owner/repo for a GitLab remote.
  - `parseRemoteURLToSource` in `internal/workspace/snapshotwriter.go:724` returns a `source.Source` with `Host` set when the host is not github.com. It is the better base for deciding "is this a github.com remote".
  - `NIWA_GITHUB_API_URL` is a single global override, not per host, so GHES remotes can't be told apart automatically.
- **Timeouts elsewhere:** `context.WithTimeout` appears at 15s in `watch.go:997` and `setup_sandbox.go:229`, 30s in `setup_sandbox.go:268`, and `worktreeProbeTimeout` in `session_lifecycle_cmd.go`. There's no shared offline-detection helper.
- **Who calls the scan:** only `runDestroyInstance` (`internal/cli/destroy.go:109`, interactive `niwa destroy`) and `runDestroyWorkspace` via `ScanInstancesParallel` (8 workers). Both are gated on `!force`, and a non-TTY run aborts when a loss is found.
- **Non-interactive destroys skip the scan entirely.** They call `workspace.DestroyInstance` directly through `destroyInstanceFunc`, from `reap.go:476,742`, `watch.go:718,800`, `dispatch.go:517` and `instance_from_hook.go:520` (session hooks). `reset.go:126` also calls `DestroyInstance` without the scan, using its own dirty-only check at line 59. So a network call in the scan affects only the interactive destroy paths: latency is visible to the user, and nothing automated would hang.
- **The scan swallows git errors.** In `scanWorkingTree`, every check is `if ...; err == nil { ... }`. If `git status`, `for-each-ref` or `stash list` fails (unreadable `.git`, a `safe.directory` "dubious ownership" refusal, a corrupt repo), no loss is recorded. **That is a silent pass today, INFERRED from code**; the measured `git status` on an unreadable `.git` exits 128. It conflicts with the "never silently pass" constraint and is worth fixing alongside this work.

### 4. Scan coverage (MEASURED unless noted)

- **Untracked directories:** `--porcelain=v1` (default `-unormal`) collapses a new directory to one `?? newdir/` line, so "N untracked" undercounts: 5 files were reported as 1. `-uall` lists each file. The detection is still correct (count > 0), only the number is off.
- **Repo config can hide untracked files:** with `status.showUntrackedFiles=no` set in the repo, the output is **empty**, so untracked work silently passes. Passing `-unormal` (or `-uall`) explicitly overrides it (2 lines again). scan.go passes no `-u` flag.
- **Ignored files** are never reported (`!!` shows only with `--ignored`), so gitignored files such as `.env` or `*.log` are deleted silently. That may be intended, but it isn't documented.
- **Stashes are repo-wide:** a stash made in the primary shows in the linked worktree's `stash list` too (common dir `r/.git`). The scan runs `stash list` per working tree, so each stash is **reported once per worktree**, which duplicates counts. Nothing is lost.
- **Submodules:** `sm/.git` is a regular file. `findPrimaryRepos` only accepts a `.git` *directory* and returns `SkipDir` at each primary, so submodules are never scanned. The superproject shows ` M sm` for a new submodule commit or untracked file inside. But once the superproject commits and pushes the gitlink, a submodule commit that was never pushed to the submodule's own remote is **invisible to the scan** (measured: the submodule's branch was `main [ahead 1]` while the superproject status was clean).
- **Nested plain repos** inside a primary show as `?? nested/` (untracked, so flagged). If they're gitignored they are invisible, and their commits are lost silently.
- **`.niwa/` is skipped by the walk**, but niwa's own session worktrees live at `<instance>/.niwa/worktrees/<repo>-<sid>` (`internal/worktree/worktree.go:128,347`). They **are** covered, because `git worktree list` from the primary enumerates them and `isInside(root, wt)` is true. `.niwa/sessions`, `locks`, `instance.json` and `marketplaces` hold no user git work in the live instance inspected (no `.git` found).
- **Worktrees outside the instance** are flagged as `LossExternalWorktree` (only their admin entry is lost) and are not deep-scanned. Correct.
- Other INFERRED gaps: a repo cloned with `--separate-git-dir` (`.git` is a file) is not found as a primary. Reflog-only commits (after `reset --hard`) aren't checked; that's acceptable.

## Assumptions

- Assumed: `head.sha` of a merged PR stays fixed after the merge. If wrong, a forge check keyed on sha equality could false-block, never false-pass, because the local tip would not match a moved sha.
- Assumed: `--stdin` and `--merge-base` arrived in 2.40. If wrong, it only matters for the batch optimization, and the path-limited loop avoids depending on it.
- Assumed: GitHub squash-merge commits lie on main's first-parent chain. If wrong, the any-point scan misses the squash and blocks, which is conservative.
- Assumed: users on Ubuntu 22.04 or Debian 11 with distro git (below 2.38) exist among niwa users. If wrong, a 2.38 floor is free. If right, the check needs a version probe that falls back to the conservative answer (report "could not verify: git < 2.38") rather than erroring.
- Assumed: an ignored-file loss is intended behavior. If wrong, the scan needs `--ignored`, which will be noisy (build outputs).
- Assumed: an edited squash (k) or partial landing (p) should block. If product wants these to pass, only a forge check (PR merged plus sha match) can say so.

## Summary of critical unknowns

Resolved:
- **Which content test.** Merge-tree against main's tip gives no false pass but false-blocks on routine post-squash drift. Merge-tree against "any first-parent main commit since the fork, limited to commits touching the branch's paths" fixed every drift case measured, with no false pass, at about 15 ms. Patch-id is strictly worse: it false-blocks on context drift and **false-passes whitespace-only work** under `--stable`. Per-file equality false-blocks on any later edit to a touched file.
- **Exit codes.** The contract is "rc==0 AND tree == base tree". rc=1 is ambiguous on 2.43 (a bad ref also gives 1), but both outcomes mean "block", so it doesn't matter.
- **git version.** 2.38 is the floor, and CI has 2.55. Ubuntu 22.04's distro git 2.34.1 does not meet it, so a version fallback is needed.
- **Forge lookup.** `pulls?state=all&head=owner:branch` returns merged PRs after the branch is deleted, but it silently ignores `head` without the owner prefix. The client already supports an httptest override (`NIWA_GITHUB_API_URL`) and has no default timeout. Only interactive destroy runs the scan.
- **Existing silent passes, independent of the question.** Git errors in the scan are swallowed. `status.showUntrackedFiles=no` hides untracked files. Unpushed submodule commits and gitignored nested repos are never seen.

Still open:
- Whether the any-point test needs a fresh `origin/<default>`. If the squash commit isn't fetched locally, every test blocks. That's conservative, but it's a false block unless destroy fetches the default branch, which is a network call. The same holds for `ls-remote` verification of a surviving branch.
- Whether a PR-merged-by-sha forge answer should override a content-test block (cases k and p).
