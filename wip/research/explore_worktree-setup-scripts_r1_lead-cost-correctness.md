# Lead: Would running setup scripts in a worktree actually work, and what would it cost?

Evidence base: niwa at `origin/main` `d25ad4d`, plus the live instance at
`/home/dgazineu/dev/niwaw/tsuku/tsuku+niwa_worktree_setup-b37e586e` that this
session is running inside. All command output below was actually run.

## Findings

### 1. What the setup mechanism actually is

`internal/workspace/setup.go` is 130 lines and there is exactly one call site.

```
$ grep -rn "RunSetupScripts\|ResolveSetupDir" --include="*.go" internal/ cmd/ | grep -v _test.go
internal/workspace/apply.go:1957:  setupDir := ResolveSetupDir(effectiveCfg, cr.Repo.Name)
internal/workspace/apply.go:1959:  result := RunSetupScripts(repoDir, setupDir, a.Reporter, redactor)
internal/workspace/setup.go:33:   func ResolveSetupDir(...)
internal/workspace/setup.go:53:   func RunSetupScripts(...)
```

The contract, read off the source:

- **Directory scanned.** `filepath.Join(repoDir, setupDir)`, where `setupDir`
  resolves repo override -> workspace default -> `scripts/setup`
  (`setup.go:30-40`). An empty repo override means disabled and sets
  `SetupResult.Disabled`. Config fields are `SetupDir string` on the workspace
  (`internal/config/config.go:328`) and `SetupDir *string` per repo
  (`config.go:473`) — the pointer is what lets `setup_dir = ""` mean "off"
  rather than "unset".
- **Eligibility.** Every non-directory entry is collected, then `sort.Strings`,
  then each is `os.Stat`ed and checked for `info.Mode()&0o111`. A non-executable
  file is *not* skipped silently: it records an error `"not executable (chmod +x
  to enable)"` and `continue`s. So a stray non-executable file in the directory
  marks the repo as setup-incomplete without stopping the run.
- **Order.** Lexical, hence the `01-`, `02-` prefix convention.
- **Working directory.** `cmd.Dir = repoDir` — the repo root, *not* the setup
  directory. This is the single most load-bearing fact for the worktree
  question (see §3).
- **Environment.** `cmd := exec.Command(scriptPath)` with **no `cmd.Env` set**,
  so the script inherits niwa's own process environment and nothing more. The
  design doc is explicit and this is a deliberate security property, not an
  oversight (`docs/designs/current/DESIGN-post-clone-scripts.md:375-377`):

  > Secrets reach setup scripts through **files only**. niwa never sets
  > `cmd.Env` for a setup script and exports nothing into its own process
  > environment, so there is no `env`-borne route to a niwa-managed secret.

  A script that wants a workspace secret reads the materialized env file out of
  its cwd. That matters below, because the worktree path *does* already
  materialize those files.
- **Output.** Announced as `running setup script <repo>/<script>`, then every
  line prefixed `[<repo>/<script>] ` and streamed durably through
  `runCmdWithReporter` with the apply's `*secret.Redactor` applied. Both the
  repo name and the script filename go through `stripEscapes` first, because
  script filenames are repo-controlled and reach a terminal.
- **Non-zero exit.** `break` — stops the *remaining scripts for that repo* only.
  The next repo still runs. At the call site (`apply.go:1955-1976`) a failure is
  carried as data into `setupIncomplete`, surfaced via `DeferWarn`, and the
  apply still exits 0. The comment says why: on `create` the error path deletes
  the instance root, so setup must never reach it.
- **`SetupResult`** carries `RepoName`, `[]ScriptResult{Name, Error}`, and two
  booleans `Skipped` (directory absent or empty) and `Disabled` (explicitly
  `""`). Note there is no "which repo dir did this run against" field — a
  worktree-aware version would want one for the warning text.

`setup_visibility_test.go` is a regression suite for issue #239 (a failing
script's stderr must survive to the operator off a TTY *and* on one, since it
previously went through the spinner) plus the announcement/prefix format. It
exercises `RunSetupScripts` against a bare `t.TempDir()` with no git repo at
all — which is worth noting: **nothing in the test suite assumes the target is a
git clone**, so the function is already shape-agnostic about what directory it
is handed.

### 2. Survey of real setup scripts in this workspace

I checked all ten repos:

```
$ for d in public/* private/*; do echo "=== $d"; ls -la "$d/scripts/setup/" 2>/dev/null || echo "  (no scripts/setup)"; done
=== public/dot-niwa    (no scripts/setup)
=== public/koto        (no scripts/setup)
=== public/niwa        (no scripts/setup)
=== public/shirabe     (no scripts/setup)
=== public/tsuku       (no scripts/setup)
=== public/.github     (no scripts/setup)
... four private repos, three with no scripts/setup ...
=== <one private repo>
-rwxrwxr-x 1 dgazineu dgazineu 696 Sep  6 09:24 01-build-workflow-tool.sh
```

And no repo overrides the location:

```
$ grep -rn "setup_dir" .niwa/instance.json public/dot-niwa/ <overlay>/
(no matches)
```

So the effective setup dir everywhere is the default `scripts/setup`, and
**exactly one setup script exists across the entire workspace**. That is itself
a finding: the mechanism is barely used, so the blast radius of changing it is
small, and the sample for "what do setup scripts do" is n=1 first-party.

Classification of that one script (it lives in a private repo, so I describe its
shape rather than reproducing it; the binary it produces, `workflow-tool`, is
referenced by name from a committed *public* hook in `dot-niwa`, at
`public/dot-niwa/.niwa/hooks/stop/workflow-continue.sh:54`):

- **Writes outside the repo tree, above it.** It computes
  `INSTANCE_ROOT="$(cd ../.. && pwd)"` from its cwd and builds a Go binary to
  `$INSTANCE_ROOT/.claude/bin/workflow-tool`. Its own comment states the
  assumption in prose: niwa runs it with cwd at the repo root, the repo sits two
  directories below the instance root, therefore `../..` is the instance root.
- **Compiles source that *is* inside the repo tree** (a Go module under the
  repo), so the input is per-tree, but the output is not.
- **Idempotent only in the weak sense.** It guards on `command -v go` and exits
  0 with a warning if Go is absent, but it then rebuilds unconditionally. There
  is no "already built, skip" check. Go's build cache makes the rebuild cheap
  (§5) but it is not a no-op: it relinks and rewrites the output file every run.

### 3. The dogfooding case: what a worktree in *this* instance would miss

This is the sharpest result of the lead, and it is not "the worktree is missing
an artifact". It is worse than that.

First, where the artifact actually landed:

```
$ ls -la <instance>/.claude/bin/
-rwxrwxr-x 1 dgazineu dgazineu 4201161 Sep  6 09:24 workflow-tool
```

It is at the **instance root**, not inside any repo. Per the framing in the
brief, that is the good case: an instance-root artifact is above every worktree,
so a worktree inherits it for free and needs no per-worktree build. On the
question as posed — "what would a fresh worktree be missing?" — the honest
answer for this instance is **nothing that this setup script produces**, because
the only setup script in the workspace writes above the worktree, not into it.
There are no `node_modules`, no `.venv`, no per-repo build outputs anywhere in
this workspace to miss.

But the *reason* it lands there is a relative path, and that is where it breaks.

Worktrees are created at `<instanceRoot>/.niwa/worktrees/<repo>-<sid>/`
(`internal/worktree/worktree.go:194-199`), while clones live at
`<instanceRoot>/<group>/<repo>/` (`apply.go:1958`). Different depths — 3 versus
2. So `cd ../..` resolves differently:

| cwd | `cd ../..` resolves to | correct? |
|---|---|---|
| `<instance>/<group>/<repo>` (clone) | `<instance>` | yes |
| `<instance>/.niwa/worktrees/<repo>-<sid>` (worktree) | `<instance>/.niwa` | **no** |

I confirmed the arithmetic directly by running an identical two-up probe from a
real worktree in my scratch repo:

```
=== running the repo's own setup script from the worktree ===
cwd=/home/dgazineu/.claude/jobs/1bad08a4/tmp/wtexp/wt
two-up=/home/dgazineu/.claude/jobs/1bad08a4/tmp
```

cwd is the worktree root, and two-up is the directory *containing* the worktrees
directory — exactly one level shy of where the clone-relative version lands.

So if niwa naively ran setup scripts in worktrees today, the first and only
first-party setup script in this workspace would **silently write its binary to
`<instance>/.niwa/.claude/bin/workflow-tool`** — a path nothing reads. Exit code
0. No warning. The apply reports success. The binary is simply in the wrong
place, and the failure surfaces later and elsewhere, as a hook telling an agent
to call a `workflow-tool` that is not on PATH.

That is the headline: **the naive implementation of this feature is not "slow",
it is silently wrong on the only first-party script that exists.** The one
real-world sample fails, and it fails quietly.

### 4. What `git worktree add` actually produces

Run against a scratch repo in `/home/dgazineu/.claude/jobs/1bad08a4/tmp` with
`node_modules/`, `dist/` and `.claude/bin/` gitignored and populated in the
clone first (full script at `/home/dgazineu/.claude/jobs/1bad08a4/tmp/wtexp.sh`):

```
=== worktree top-level contents ===
.  ..  .git  .gitignore  README.md  scripts

=== worktree .git ===
-rw-rw-r-- 1 dgazineu dgazineu 79 ... /tmp/wtexp/wt/.git
--- its content:
gitdir: /home/dgazineu/.claude/jobs/1bad08a4/tmp/wtexp/clone/.git/worktrees/wt

=== node_modules carried over? ===   ABSENT
=== dist carried over? ===           ABSENT
=== .claude/bin carried over? ===    ABSENT

=== git rev-parse inside worktree ===
show-toplevel:       /home/.../wtexp/wt
git-dir:             /home/.../wtexp/clone/.git/worktrees/wt
git-common-dir:      /home/.../wtexp/clone/.git
is-inside-work-tree: true

=== is .git a file or directory? ===
-> .git is a FILE
```

Stated exactly: a fresh worktree contains **tracked content at the checked-out
commit and nothing else**. Every gitignored artifact is absent — that is the
whole gap. `.git` is a *file* containing a `gitdir:` pointer, not a directory.
`--show-toplevel` correctly returns the worktree root (so scripts using it are
fine); `--git-dir` returns the per-worktree admin dir under the clone, and
`--git-common-dir` returns the shared one.

Hazard inventory for a script running in a worktree, ranked by what I actually
observed:

1. **Relative escapes from the repo root** (`cd ../..`, `../../foo`). Broken by
   the depth difference. This is the one that bites in practice, and it is the
   one first-party script we have.
2. **`test -d .git` / writing into `.git/hooks/`.** `.git` is a file, so a
   `-d` test fails. Note the design doc's stated motivating use case for the
   whole feature is *"repos need git hooks installed"*
   (`DESIGN-post-clone-scripts.md:6-7`) — and hooks live in the *shared*
   `git-common-dir`, so a per-worktree hook install is both a
   redundant re-write and a cross-worktree shared-path write.
3. **`git rev-parse --show-toplevel`** is safe; it returns the worktree root.
4. **Absolute paths baked at clone time** — none observed here.
5. **Concurrent writes to a shared output path** — see §6.

Also worth recording: `scaffoldWorktreeNiwa` (`worktree.go:97-117`) creates only
`.niwa/sessions/` in a worktree, deliberately *not* `mcp.json` or
`workspace-context.md`, "main-instance artifacts that are not needed in session
worktrees". There is already an explicit editorial line about what a worktree is
and is not entitled to.

### 5. Per-worktree cost, generalized

For *this* workspace the answer is: near-free, and the JS profile is the worst
case, not the representative one.

The single real setup script's expensive step is a Go build, and Go's build
cache is in `$HOME`, shared across every worktree:

```
$ go env GOCACHE GOMODCACHE
/home/dgazineu/.cache/go-build
/home/dgazineu/go/pkg/mod
$ du -sh /home/dgazineu/.cache/go-build
2.5G
```

Measured, warm — what a second worktree would actually pay:

```
$ time go build -o /tmp/wf-test ./cmd/workflow-tool/
real  0m0.291s
```

**0.29 seconds and 4 MB of output**, versus the JS monorepo's ~13s and 341 MB
per tree. The structural reason is the one the brief already identified, and
this is the other side of it: `node_modules` is per-tree *by construction* and
can never amortize; a Go build cache, a Cargo registry, a pip wheel cache, a
Turbo remote cache all live in `$HOME` and amortize completely. Go is the
extreme good case because Go has no per-project dependency directory at all —
there is no `node_modules` analogue to reproduce.

So the cost profile is bimodal, and which mode a repo is in is determined by its
ecosystem, not by anything niwa controls:

- **Ecosystems with a per-tree dependency directory** (npm/pnpm/yarn
  `node_modules`, Python `.venv`, Ruby vendored `bundle`): unavoidable real cost
  per worktree, both time and disk. pnpm is the partial exception — its store is
  in `$HOME` and it hardlinks, so disk mostly amortizes even though the
  link-farm materialization does not.
- **Ecosystems with only a `$HOME` cache** (Go, Rust, and any build step behind
  a shared cache): sub-second, essentially free.

The practical consequence for design: because the cost is bimodal and
per-ecosystem, **a global on/off is the wrong shape and `setup_dir = ""` is
definitely the wrong lever.** `setup_dir = ""` disables setup for the *clone*
too, which is not what anyone wants — the tools repo here needs its clone-time
build and would be actively broken by disabling it. Whatever this becomes needs
a switch that is orthogonal to `setup_dir`, and given the bimodality it plausibly
wants to be per-repo rather than per-workspace. Worth noting the design doc
already has a **deferred `setup_policy`** concept
(`DESIGN-post-clone-scripts.md:669`), which is the natural place for it.

### 6. Concurrency

**niwa's apply does not parallelize setup.** `DESIGN-parallel-clones.md`
parallelizes Step 3 (clone/sync) with a worker pool capped at 8; setup is Step
6.75 and the loop at `apply.go:1955` is a plain sequential `for _, cr := range
classified`. Grepping the parallel design for `setup` returns nothing. So today
there is no intra-apply concurrency on setup at all.

A worktree fan-out would introduce it, and the one script we have is *not* safe
under it. Two hazards, both real:

1. **Every worktree of a given repo computes the same wrong output path.**
   `cd ../..` from `<instance>/.niwa/worktrees/<repo>-<sid>` yields
   `<instance>/.niwa` regardless of `<sid>`. So it is not that each worktree
   writes to its own wrong place — *all* of them write to the same single file,
   `<instance>/.niwa/.claude/bin/workflow-tool`. Concurrent `go build -o` to one
   path is a genuine race (torn output, or `ETXTBSY` if another process has it
   open). Sequentially it is merely wasteful: N identical rebuilds of one
   artifact.
2. **Git hook installation**, the design's own motivating use case, targets
   `git-common-dir` — shared by the clone and every worktree of it. N worktrees
   installing hooks means N writes to one shared location.

The existing worktree env refresh already models the right defensive posture and
is worth copying: `refreshWorktreeEnvs` (`apply.go:2372-2440`) skips a worktree
that is missing, that is **attached (locked) by another process** via
`ReadAttachState` with `reapStale=false`, or that git no longer registers — and
in the skip-but-live cases it forward-carries prior state rather than dropping
it. A setup fan-out would need the same attach-lock guard, because running a
build inside a worktree another process is actively working in is worse than
skipping it.

### The precedent nobody has connected yet

There is **already a script-execution mechanism that runs against worktrees**,
and its own source comment names the gap:

```go
// 5. Worktree-event hooks, run on create/apply. Analog of the instance
//    setup-script run: discovered from <configDir>/worktree-hooks/ and
//    executed against the worktree, with worktree context in the env.
```
(`internal/workspace/worktree_content.go:707-714`, calling `runWorktreeHooks` at
line 1061.)

`runWorktreeHooks` already does lexical ordering, the same executable-bit check
with an explicit comment saying it "match[es] the setup-script policy", and
`cmd.Dir = worktreePath`. The difference is ownership and environment:

| | setup scripts | worktree hooks |
|---|---|---|
| Source | the **repo** (`<repo>/scripts/setup/`) | the **workspace config** (`<configDir>/worktree-hooks/`) |
| Runs against | clone only | worktree only |
| cwd | repo root | worktree root |
| Env | inherits only; **no `cmd.Env`** | `os.Environ()` + `NIWA_WORKTREE_PATH`, `_REPO`, `_PURPOSE`, `_BRANCH` |
| Redactor | yes | **no** — `cmd.Stdout = stderr` raw |
| Non-zero exit | warn, continue to next repo | returns error, fails the worktree apply |

So the gap is precisely and only: **repo-owned scripts have no worktree analog,
while workspace-owned scripts do.** And `NIWA_WORKTREE_PATH` already exists as
the answer to the `cd ../..` problem — a script that wants the instance root
could be given `NIWA_INSTANCE_ROOT` explicitly rather than deriving it by
counting directories.

The asymmetry in the table is also a defect in its own right, independent of
this exploration: worktree hooks stream unredacted into a worktree that step 2b
has just populated with inherited secret files.

## Implications

1. **The naive version of this feature is silently wrong, not merely slow.** The
   only first-party setup script in the workspace would write its output to a
   dead path with exit code 0. Any design must confront relative-path escape
   from the repo root, and the fix is to *provide* the anchor
   (`NIWA_INSTANCE_ROOT`, `NIWA_WORKTREE_PATH`) rather than let scripts count
   `../..`.
2. **`setup_dir = ""` cannot be the opt-out.** It disables the clone-time run
   too, which is the run that actually matters for the one script we have. This
   needs an orthogonal switch; the deferred `setup_policy` in the existing design
   doc is where it belongs.
3. **Cost is bimodal and ecosystem-determined** — 0.29s for Go here versus ~13s
   and 341 MB for the JS profile. A single workspace-wide default will be wrong
   for one half of any mixed workspace, which argues for a per-repo setting.
4. **`runWorktreeHooks` is the design precedent and probably the implementation
   site.** It already runs scripts against worktrees with the identical
   ordering and executable-bit policy, and it already exports worktree context
   into the env. The feature is closer to "extend an existing mechanism to
   repo-owned scripts" than to "build a new one".
5. **Setup would be the first concurrency-exposed step in the pipeline.** It is
   sequential today and the parallel-clones work never touched it. The
   attach-lock guard in `refreshWorktreeEnvs` is the pattern to reuse.
6. **Scripts would need a way to know they are in a worktree.** Some work is
   genuinely per-tree (`node_modules`); some is genuinely shared and must be
   skipped (git hooks in `git-common-dir`, instance-root binaries). Without a
   signal, a script cannot tell, and a well-written script cannot be written.

## Surprises

- **Only one setup script exists in the entire ten-repo workspace**, and it is
  the counterexample rather than the example: it writes *above* the repo, so it
  needs no per-worktree run at all — it needs to be *not* run per-worktree.
  The feature's motivating case (`node_modules`) has zero instances here.
- **The gap is already named in a source comment.** `worktree_content.go:707`
  calls worktree hooks the "Analog of the instance setup-script run" — the
  asymmetry was seen and left.
- **The design doc's stated motivating use case, installing git hooks, is
  exactly the case that must NOT run per-worktree**, since hooks live in the
  shared `git-common-dir`.
- **Setup scripts get a redactor but no worktree env; worktree hooks get the
  env but no redactor.** Each mechanism has the half the other is missing.
- **`RunSetupScripts` never touches git.** Its tests run it against a bare
  `t.TempDir()`. Pointing it at a worktree path requires no change to the
  function at all — every hard problem is in the scripts and the policy, none in
  the runner.

## Open Questions

- Should a worktree setup run be opt-in per repo, or opt-out? Given that the one
  first-party script would be *harmed* by running (it would write to a dead
  path), opt-in looks safer, but opt-in means the JS case — the case the feature
  exists for — needs explicit configuration to work.
- What is the signal to a script that it is in a worktree? `NIWA_WORKTREE_PATH`
  already exists for hooks; reusing it for setup scripts is the obvious move,
  but it changes the "no `cmd.Env`" property that the security section of
  `DESIGN-post-clone-scripts.md` reasons about explicitly.
- Does `niwa worktree create` run setup synchronously (slow create, correct
  tree) or in the background (fast create, a window where the tree is
  incomplete)? At ~13s for the JS case this is a real UX decision; at 0.29s it
  would not matter.
- Should the failure policy match setup's (warn, exit 0) or worktree hooks'
  (error, fail the apply)? They currently disagree, and a merged path has to
  pick.
- Is `SetupResult` sufficient once it can describe a run against N worktrees, or
  does it need a target-directory field for the warning text to be intelligible?

## Summary

Running a repo's setup scripts in a worktree works mechanically — `RunSetupScripts` never touches git, its tests already run it against a bare temp dir, and a fresh worktree is confirmed to contain tracked content and nothing else — but it is silently wrong on the only first-party setup script that exists in this workspace, which derives the instance root as `cd ../..` and would therefore write its binary to a dead path under `.niwa/` with exit code 0 and no warning. Cost is bimodal rather than uniform: that script rebuilds warm in 0.29s because Go's cache lives in `$HOME`, so the JS monorepo's ~13s and 341 MB per tree is the worst case and not the representative one, which means the opt-out has to be per-repo and orthogonal to `setup_dir = ""` (that flag would break the clone-time run the workspace actually depends on). The strongest lead for a fix is `runWorktreeHooks`, whose own source comment already calls itself the "Analog of the instance setup-script run" and which already exports `NIWA_WORKTREE_PATH` into the script environment — the anchor that would make `cd ../..` unnecessary.
