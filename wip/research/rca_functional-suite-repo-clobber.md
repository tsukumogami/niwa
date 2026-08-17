# RCA: the functional suite reinitialized the working repository

> **Read this first — the incident is not closed.** `git ls-remote origin refs/heads/main`
> returns `e133f46ea9c196780cd80c93726b6cd986821899`. The clobbering commit was
> **pushed to GitHub and is still upstream main**. Only the local refs were
> recovered. Upstream main should be `230c5c9`. No other upstream ref is affected
> (17 heads checked; only `refs/heads/main` carries `e133f46`).

## Mechanism

**Two `make test-functional*` runs overlapped in the same checkout, and the second
one's per-scenario sandbox wipe deleted the first one's live git working directory
out from under it. The fixture then re-created that directory as a plain
directory — no `.git` inside it — and the next `git add -A` / `git commit` / `git
push` walked up the tree and found the real repository.**

### Why the sandbox is shared and why it sits inside the repo

`Makefile:17-19` sets `NIWA_TEST_BINARY=$(CURDIR)/niwa-test`, so the test binary
lives at the repository root. `test/functional/suite_test.go:145-147` derives the
sandbox from the binary's directory:

```go
repoRoot := filepath.Dir(binPath)          // = the real repository root
sandbox := filepath.Join(repoRoot, ".niwa-test")
_ = os.RemoveAll(sandbox)                  // runs in Before, i.e. once per scenario
```

Two properties matter and both are load-bearing:

1. The path is **fixed** — every process running the suite in this checkout uses
   the identical `<repo>/.niwa-test`, and wipes it at the start of *every*
   scenario (163 times per full run, several times per second).
2. The path is **inside the real repository's working tree**, so any directory
   under it is a directory from which git's upward repository discovery reaches
   `<repo>/.git`.

`.gitignore` carries `/.niwa-test/`, which is why the sandbox itself never showed
up in the runaway commits — only the genuinely-uncommitted working-tree files did.

The same pattern applies to `suite_test.go:174-175`
(`/tmp/niwa-test-workspaces`, also fixed, also `RemoveAll`ed per scenario, also
shared across concurrent runs — and across *different checkouts* on the machine).

### The escape path in the fixture

`test/functional/localrepo_test.go`, `createRepoWithSpec`:

- `:72` `workDir, err := os.MkdirTemp(s.root, "clone-*")` — the clone working
  directory is created **inside the sandbox**, i.e. inside the real repo.
- `:78` `git clone <fileURL> <workDir>` — leaves a valid `workDir/.git`.
- `:82-90` the files loop calls `os.MkdirAll(filepath.Dir(targetPath), 0o755)`
  before each write. **If `workDir` has been deleted in the meantime, this
  silently re-creates it** (and any subdirectories) as ordinary directories.
  Nothing re-creates `workDir/.git`.
- `:109-128` `git add -A`, `git commit -m "initial"`, `git push -u origin HEAD`,
  each with `cmd.Dir = workDir` and `cmd.Env = gitEnv`. The lead's hypothesis that
  `Dir` was unset is indeed wrong — `Dir` is set on all three, confirmed. **But
  none of them sets `GIT_DIR` or `GIT_WORK_TREE`, so all three rely on git's
  upward discovery from `cmd.Dir`.** With `workDir/.git` gone, discovery walks
  `clone-XXXX` → `gitserver` → `.niwa-test` → `<repo>` and finds `<repo>/.git`.

The two `Dir`-less invocations the lead flagged (`git init --bare <repoPath>` at
`:62` and `git clone <fileURL> <workDir>` at `:78`) are not the escape: both take
absolute paths derived from `s.root`, which `suite_test.go:181-182` builds
absolutely. They are still dangerous for a different reason — `git -C <repoPath>
symbolic-ref` at `:66` also relies on discovery, see below.

### The timeline, from the reflog and the session transcript

All times local (`-0400`).

| Time | Event | Evidence |
|---|---|---|
| 03:52:55.6 | subagent `impl-issue-11` starts `make test-functional-critical` | session transcript, Bash tool_use |
| 03:53:00.8 | team lead starts `timeout 1200 make test-functional` in the **same checkout** | session transcript, Bash tool_use |
| ~03:53:02 | the lead's run reaches its first `Before` hook → `os.RemoveAll("<repo>/.niwa-test")` destroys the critical run's live clone workdir | `suite_test.go:147` |
| 03:53:02 | `commit: initial` → **699b887** on `docs/dual-agent-workspace` | `git reflog show docs/dual-agent-workspace@{2}` |
| 03:53:03 | HEAD moved to `main` (230c5c9) with an **empty reflog message** | `git reflog show HEAD` — an empty message is what `git symbolic-ref HEAD <ref>` writes |
| 03:53:03 | `commit: initial` → **e133f46** on `main` | `git reflog show main@{1}` |
| 03:53:04 | `update by push` on `refs/remotes/origin/main` → e133f46 | `git reflog show refs/remotes/origin/main` |
| 03:53:06–03:54:27 | ~30 further empty-message HEAD reflog entries, all at e133f46 | `git reflog show HEAD` |
| 03:53:35, 03:53:49, 03:54:07 | three more overlapping `make test-functional*` launches from both agents | session transcript |

Neither stray commit is a root commit, contrary to the initial report:
`git rev-list --parents -n1` gives `699b887 → 49fcb11` and `e133f46 → 230c5c9`.
Both carry author **`niwa-test <niwa-test@example.com>`**, an identity that exists
in exactly two places in the tree (`localrepo_test.go:104-106` and
`steps_workspace_config_sources_test.go:47-49`); the latter commits with the
message `force-pushed history`, so the actor is unambiguously
`createRepoWithSpec`.

Both stray commits have the **same tree** (`c343e50` for each), and that tree has
669 entries including `.github/workflows/*` — it is the real working tree, not a
fixture tree. `git show --stat 699b887` shows exactly the four files that were
uncommitted at the time: `codex_dual_agent_steps_test.go`, `codex-agent.feature`,
`localrepo_test.go`, `suite_test.go`.

### How `main` got involved

`git symbolic-ref HEAD refs/heads/main` (`localrepo_test.go:66`, intended for the
fixture's bare repo) repointed the **real** repository's HEAD from
`docs/dual-agent-workspace` to `main` without touching the index or the working
tree. The very next `git add -A` + `git commit` therefore wrote the
feature-branch tree onto `main` as e133f46 — identical tree, new parent 230c5c9.
`git push -u origin HEAD` then pushed `main` to `git@github.com:tsukumogami/niwa.git`,
and because `e133f46`'s parent is exactly the upstream tip it was a clean
fast-forward, so the push **succeeded**. That is the `update by push` entry at
03:53:04 and the reason upstream main is still wrong.

For `git -C <repoPath> symbolic-ref` to hit the real repo, `<repoPath>` has to be
a directory that is not a valid git dir — which is what a `git init --bare` racing
a concurrent `os.RemoveAll` leaves behind. Note `suite_test.go:147` discards the
`RemoveAll` error (`_ =`), so a partially-completed delete (RemoveAll unlinks
entries one at a time and its final `rmdir` loses to a concurrent writer) is
silent.

### Both error signatures, explained

- `git commit: exit status 1` + `On branch main` + `Your branch is up to date with
  'origin/main'` + `nothing to commit, working tree clean` — this is the **real
  repository**, after 03:53:04. HEAD is on `main`; `main` tracks `origin/main`
  (`branch.main.merge=refs/heads/main` in `.git/config`); both are at e133f46
  because the push succeeded; and the first runaway commit already swept up every
  uncommitted file, so `git add -A` staged nothing. Every subsequent fixture call
  in that run produced this, which is why the run failed broadly, and each one
  left another empty-message HEAD reflog entry from its `symbolic-ref`.
- `git commit: exit status 128` + `fatal: Unable to read current working
  directory: No such file or directory` — this is git's own `getcwd()` failing,
  which only happens when the process's current directory has been *unlinked
  after* the `chdir`. Go's `exec.Cmd` chdirs in the child before exec, so the
  directory existed at fork time and was gone by the time git ran: a direct
  observation of the other run's `RemoveAll` landing mid-command. (Had `Dir` been
  missing entirely, Go would have failed the `chdir` itself and reported
  `chdir …: no such file or directory` instead — another way to see that `Dir`
  was set.)

## New defect or latent?

**Latent.** Every ingredient predates `df6cd98`.

`git show 49fcb11:test/functional/suite_test.go` contains the sandbox block
verbatim — `repoRoot := filepath.Dir(binPath)`, `sandbox := filepath.Join(repoRoot,
".niwa-test")`, `_ = os.RemoveAll(sandbox)`, the `/tmp/niwa-test-workspaces` wipe.
`git show 49fcb11:test/functional/localrepo_test.go` contains
`createRepoWithFiles` with the identical `os.MkdirTemp(s.root, "clone-*")`, the
identical `MkdirAll`-in-the-files-loop, and the identical `add`/`commit`/`push`
block with `Dir = workDir` and no `GIT_DIR`.

`git diff 49fcb11 df6cd98 -- test/functional/localrepo_test.go` is +28 lines and
adds only the `createRepoWithSpec` rename, a symlink loop, and the
`SourceRepoSpec` wrapper. The `suite_test.go` diff adds struct fields and one
`registerCodexDualAgentSteps(ctx)` call. **Neither touches the dangerous code.**

The new work contributed only exposure: it added 14 more scenarios that build
fixture repos, roughly doubling the wall-clock window in which a clone workdir is
live, and — much more decisively — it created a situation where two agents were
independently exercising the suite in the same checkout within seconds of each
other. **The fix belongs upstream of this feature.** The suite has been unsafe to
run concurrently since the `localGitServer` helper was introduced; nobody had
run two copies at once before.

## Proposed fix

Four changes. (1) and (3) are the structural ones; either alone downgrades this
from "silently rewrites your repo and pushes it" to "test fails loudly".

**1. Give each process its own sandbox, outside the repository.**
`test/functional/suite_test.go:145-157`. Allocate the sandbox root **once per
process** in `TestMain` (or a `sync.Once`), not per scenario, and put it outside
the working tree:

```go
var sandboxRoot string // package-level, set once

func TestMain(m *testing.M) {
    var err error
    sandboxRoot, err = os.MkdirTemp("", "niwa-func-")   // e.g. /tmp/niwa-func-1234
    if err != nil { … }
    code := m.Run()
    if os.Getenv("NIWA_TEST_KEEP_SANDBOX") == "" {
        _ = os.RemoveAll(sandboxRoot)
    }
    os.Exit(code)
}
```

Per scenario, allocate a fresh child (`os.MkdirTemp(sandboxRoot, "scn-")`) instead
of wiping a shared path. Two concurrent runs then cannot see each other's
directories at all. This also removes the discovery hazard outright: a temp dir
outside the repo has no enclosing git repository to fall into. Keep the
"inspectable on failure" property with `NIWA_TEST_KEEP_SANDBOX=1` plus printing
the path on failure — that is what the current comment at `:143-144` was buying,
and it is not worth a shared mutable path inside the repo. Apply the same
treatment to `wsParent` at `:174-175`, and stop discarding the `RemoveAll` error.

**2. Make the Makefile refuse to run two suites at once.** `Makefile:17-27`, wrap
both targets:

```make
test-functional: build-test
	flock -n .niwa-test.lock -c 'NIWA_TEST_BINARY=$(CURDIR)/niwa-test go test -v ./test/functional/...' \
	  || { echo "another functional run holds .niwa-test.lock"; exit 1; }
```

Cheap, and it removes the whole class of "two agents, one checkout" collisions,
including ones that have nothing to do with git.

**3. Never let a fixture git command discover its repository.** This is the guard
that survives a mistake anywhere else. In `localrepo_test.go`, funnel *every* git
invocation through one helper and give it three jobs — bounds-check, explicit
repo addressing, and a ceiling:

```go
// fixtureGit runs git for the fixture. dir must be inside the sandbox, and git
// is told exactly which repository to use, so a vanished or mis-derived path
// fails loudly instead of falling through to whatever repo encloses it.
func (s *localGitServer) fixtureGit(dir, gitDir, workTree string, args ...string) ([]byte, error) {
    if !underRoot(s.root, dir) {
        return nil, fmt.Errorf("fixture git refused: %q is outside the sandbox %q", dir, s.root)
    }
    cmd := exec.Command("git", args...)
    cmd.Dir = dir
    cmd.Env = append(os.Environ(),
        "GIT_AUTHOR_NAME=niwa-test", /* … */
        "GIT_CEILING_DIRECTORIES="+s.root, // discovery may never climb past the sandbox
    )
    if gitDir != "" {
        cmd.Env = append(cmd.Env, "GIT_DIR="+gitDir, "GIT_WORK_TREE="+workTree)
    }
    return cmd.CombinedOutput()
}

func underRoot(root, p string) bool {
    rel, err := filepath.Rel(root, p)
    return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
```

Then:
- `add`/`commit`/`push` pass `gitDir = filepath.Join(workDir, ".git")`,
  `workTree = workDir`. With `GIT_DIR` explicit, a deleted clone fails with
  `fatal: not a git repository: '…/clone-XXXX/.git'` instead of finding
  `<repo>/.git`. **This single change would have turned the incident into a red
  test.**
- `git -C repoPath symbolic-ref …` passes `gitDir = repoPath` (it is bare), which
  is what closes the `main`-repointing half of the incident.
- `GIT_CEILING_DIRECTORIES` covers anything added later that still wants
  discovery.

Compare the guard-the-call-site alternative: adding an `os.Stat(workDir + "/.git")`
check before the commit would have caught this exact instance and nothing else.
The helper is the one to write.

**4. One cheap assertion in the `Before` hook**, for defence in depth: fail the
scenario immediately if the sandbox root has an ancestor containing `.git`. With
fix (1) it never fires; if someone re-parents the sandbox into the repo later, it
fires on the first scenario rather than at 03:53 some night.

## Is `@critical` affected?

**Yes — and it was the victim, not a bystander.** The process that wrote 699b887
and e133f46 and pushed to GitHub was the subagent's `make test-functional-critical`
(started 03:52:55), destroyed by the lead's `make test-functional` (started
03:53:00).

Nothing about the dangerous path is gated by tag or by the new scenarios:

- `make test-functional-critical` (`Makefile:23-27`) differs from
  `test-functional` only by `NIWA_TEST_TAGS=@critical`. Identical binary path,
  identical `<repo>/.niwa-test` sandbox, identical `Before` hook.
- The `@critical` subset is a heavy user of `createRepoWithSpec`: the saved
  `critical.log` contains 63 `a config repo` step invocations, plus the
  `a source repo` steps in `steps_test.go:1039`,
  `worktree_delegation_steps_test.go:68`, and `session_steps_test.go:32,72`.

So CI's `@critical` gate is exposed to exactly the same defect. CI is safer only
because it runs one suite per job in a throwaway checkout — the property fix (1)
gives developers.

## Confidence

**Established by direct evidence:**

- Both stray commits' author (`niwa-test <niwa-test@example.com>`), message, parents,
  and trees (`git log --format=fuller`, `git rev-list --parents`,
  `git ls-tree`) — and that `niwa-test@example.com` exists only in the functional
  fixture, so `createRepoWithSpec` is the actor.
- The exact reflog sequence and its timestamps (`git reflog show HEAD|main|refs/remotes/origin/main --date=iso`).
- **Upstream `refs/heads/main` is still `e133f46`** (`git ls-remote`), and no other
  upstream head is.
- The two overlapping invocations and their start times, from this session's
  transcript (`make test-functional-critical` at 07:52:55.623Z;
  `make test-functional` at 07:53:00.779Z; three more overlaps through 07:54:07).
- The code shape at both `df6cd98` and `49fcb11`, so the latent-vs-new question is
  settled by the diffs rather than by argument.
- That the full suite passes cleanly when run alone: the saved `full.log`
  (04:03) shows `163 scenarios (161 passed, 2 pending)` and left no reflog entry.
  The defect is a concurrency defect, not a defect in the new scenarios.

**Inferred (consistent with everything above, not directly observed):**

- The precise micro-interleaving — `RemoveAll` deleting `workDir/.git`, then the
  files loop's `MkdirAll` re-creating `workDir` as a plain directory, then
  discovery climbing to `<repo>/.git`. Every step is mechanically forced by the
  code, and the two error signatures corroborate it (one is the real repo
  speaking, the other is a `getcwd` on an unlinked directory), but I did not watch
  it happen.
- That the ~30 empty-message HEAD reflog entries after 03:53:06 are
  `git -C repoPath symbolic-ref` calls falling through to the real repo because
  `repoPath` was left non-valid by a racing partial delete. The count and cadence
  match the fixture-call rate of a run in which every subsequent
  `createRepoWithSpec` failed at `git commit`, and `symbolic-ref` is the only
  command in the fixture that writes an empty-message HEAD entry — but a partial
  `RemoveAll` is the one link I am reasoning about rather than observing.

**The experiment that would confirm it** (do not run it in this checkout — use a
throwaway clone, and only after fix (1) is in place elsewhere): in a scratch clone
with a deliberately uncommitted file, start `make test-functional` with
`GIT_TRACE=1 GIT_TRACE_SETUP=1` exported so every fixture git call prints the
repository it resolved (`setup: git_dir: …`, `setup: worktree: …`), and from a
second shell loop `while :; do rm -rf <clone>/.niwa-test; sleep 0.2; done`. The
escape is confirmed the moment a trace line reports `git_dir: <clone>/.git` for a
command whose `cwd` is under `.niwa-test`; `git reflog show HEAD` in the scratch
clone then shows the same `commit: initial` / empty-message pair. Reverting fix
(3)'s `GIT_DIR` alone should flip the outcome, which is the cleanest way to prove
the guard is the right one.

## Summary

Two agents ran the functional suite in the same checkout five seconds apart; the
second run's per-scenario `os.RemoveAll("<repo>/.niwa-test")` (`suite_test.go:147`)
deleted the first run's live clone working directory, the fixture's
`MkdirAll` re-created it without a `.git`, and because the sandbox lives inside
the real repository and the fixture's `git add`/`commit`/`push` rely on upward
discovery (`localrepo_test.go:82-128`), those commands operated on the real
checkout — committing the uncommitted work tree as `initial`, repointing HEAD to
`main` via the fixture's `symbolic-ref`, committing again, and fast-forward
**pushing `e133f46` to GitHub, where `refs/heads/main` still sits today**. The
defect is latent, not new: every dangerous line is byte-identical at `49fcb11`,
and `@critical` is fully exposed — the run that did the damage *was*
`make test-functional-critical`. The fix is to allocate a per-process sandbox
outside the working tree, lock the Makefile targets against concurrent runs, and
route every fixture git call through a helper that bounds-checks its directory and
passes `GIT_DIR`/`GIT_WORK_TREE` explicitly so a missing sandbox fails loudly
instead of resolving to whatever repository encloses it.
