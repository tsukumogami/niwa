# Lead: What is the correct code shape for the niwa functional-suite fix, and does it hold against the failure that actually occurred?

## Git invocation inventory

| file:line | command | `cmd.Dir`/`-C` set? | `GIT_DIR`/`GIT_WORK_TREE` set? | escapes upward on missing `.git`? | fixed by routing through a bounds-checked helper? |
|---|---|---|---|---|---|
| `localrepo_test.go:29` | `git init --bare <repoPath>` | no (absolute path arg) | no | n/a — target is an explicit absolute path, not cwd-derived | already safe; helper would be a no-op here |
| `localrepo_test.go:36` | `git -C <repoPath> symbolic-ref HEAD refs/heads/main` | `-C repoPath` | no | yes, if `repoPath` lacks `.git` | yes |
| `localrepo_test.go:53` | `git init --bare <repoPath>` | no | no | n/a | already safe |
| `localrepo_test.go:57` | `git -C <repoPath> symbolic-ref HEAD refs/heads/main` | `-C repoPath` | no | yes | yes |
| `localrepo_test.go:69` | `git clone <fileURL> <workDir>` | no (`workDir` is a positional arg) | no | no — clone doesn't do upward discovery for its target | n/a |
| `localrepo_test.go:90` | `git add -A` | `cmd.Dir = workDir` | no | **yes — this is the exact command that found the real repo in the incident** | yes |
| `localrepo_test.go:97` | `git commit -m initial` | `cmd.Dir = workDir` | no | **yes — wrote the feature-branch tree onto whatever HEAD it found** | yes |
| `localrepo_test.go:104` | `git push -u origin HEAD` | `cmd.Dir = workDir` | no | **yes — this is what fast-forwarded the real repo's `origin` to GitHub** | yes |
| `session_steps_test.go:696` (`runGitInDir`, shared helper used by ~6 step funcs: `theMainCloneIsOnBranch`, `theSessionBranchExistsInRepo`, etc.) | `git <args...>` | `cmd.Dir = dir` | no | yes, if `dir` lacks `.git` | yes — this is the natural chokepoint; wrapping it fixes every caller in one place |
| `steps_workspace_config_sources_test.go:60-65` (loop) | `git init --initial-branch=main <work>`, `git -C <work> config user.email/name` | mixed: `init` takes `work` as positional arg; `config` uses `-C work` | `GIT_CONFIG_GLOBAL=/dev/null`, `GIT_CONFIG_SYSTEM=/dev/null` set (env, not `GIT_DIR`) | `config` calls yes, in principle, though `work` was just freshly `git init`'d moments earlier in the same function so the window is narrow | yes, for defense in depth |
| `steps_workspace_config_sources_test.go:78-86` (loop) | `git -C <work> add`, `git -C <work> commit`, `git -C <work> push --force <bareDir> main` | `-C work` | no | same narrow-window risk as above | yes |
| `steps_workspace_config_sources_test.go:208` | `git clone <url> <clone>` | no | no | no | n/a |
| `worktree_delegation_steps_test.go:278` (`noWorktreeIsRegisteredForRepo`) | `git -C <repoPath> worktree list` | `-C repoPath` | no | yes, if `repoPath` lacks `.git` — and unlike `repoPathForGroupRepo` (line 686, used elsewhere in the same file), this call site builds `repoPath` directly with no `.git`-existence check first | yes |

Two more non-fixture git call sites exist in the suite (`steps_init_bootstrap_test.go:193` invoking `niwa init` under a pty, `steps_dispatch_capture_test.go:44` invoking a captured command) — both run the compiled `niwa` binary, not raw `git`, so any git activity they trigger is inside niwa's own production code path, out of scope for a test-fixture helper.

**Key finding on `-C`:** `git -C <dir> <cmd>` is not a containment boundary — it only sets the initial working directory, exactly like `cmd.Dir`. If `<dir>` itself is not (or no longer) inside a `.git` tree, git's normal upward-discovery walk continues past it. Every `-C`/`cmd.Dir` call site above is therefore equally exposed; the incident happened to trigger through `cmd.Dir` (`localrepo_test.go:90/97/104`), but the `-C` call sites carry the identical defect and would have escaped just as easily under the same race.

## Fixed shared paths

| Path | Defined at | Wiped by | Scope | Risk |
|---|---|---|---|---|
| `<repoRoot>/.niwa-test` (`sandbox`) | `suite_test.go:117` — `repoRoot := filepath.Dir(binPath)`, `sandbox := filepath.Join(repoRoot, ".niwa-test")` | `os.RemoveAll(sandbox)` at `suite_test.go:118`, error discarded, once per scenario (163×/run) | Shared by every scenario in one `go test` process; also shared by any second concurrent process pointed at the same `NIWA_TEST_BINARY` (same repo checkout) | The incident's root cause — inside the working tree, `RemoveAll` deletes a live sibling process's clone workdir mid-git-command |
| `<sandbox>/gitserver` (`gitServerDir`) | `suite_test.go:152` | inherited from `sandbox` wipe above | same as sandbox | same |
| `<sandbox>/home`, `<sandbox>/tmp`, `<sandbox>/shared-bin` | `suite_test.go:122-136` | inherited from `sandbox` wipe | same | same |
| `os.TempDir()/niwa-test-workspaces` (`wsParent`) | `suite_test.go:145` | `os.RemoveAll(wsParent)` at `suite_test.go:146`, error discarded, once per scenario | **Shared across different checkouts on the same machine** — `os.TempDir()` is machine-global, not repo-scoped | Arguably worse than the sandbox path: two unrelated clones of `niwa` running functional tests concurrently would race here even without both pointing at the same repo |
| `Makefile:20,27,32,40,53` | `rm -rf .niwa-test` in `test-functional`, `test-functional-critical`, `test-functional-claude-integration`, `test-install`, and `clean` targets | n/a (post-run cleanup, not concurrency-relevant by itself, but confirms `.niwa-test` is treated as a single well-known repo-relative path everywhere) | | |

## What breaks if the sandbox moves outside the repo

Checked every place that could assume `.niwa-test` sits beside the binary, and found the design already anticipates most of this:

- **`Makefile:16-17,20,27,32,40,53`** — `rm -rf .niwa-test` in every target. This becomes dead/no-op cleanup once the sandbox moves to `/tmp/niwa-func-XXXX` (each process cleans its own dir via `TestMain`/defer instead). Should be removed or left as a harmless no-op; either way it stops being load-bearing.
- **`.gitignore:23-24`** (`# Functional test sandbox (per-scenario scratch)` / `/.niwa-test/`) — becomes unnecessary once nothing under the repo is ever created, but leaving the ignore rule is harmless.
- **`docs/guides/functional-testing.md:40-51`** — documents `homeDir`/`tmpDir`/`workspaceRoot` as sandbox children and already states `workspaceRoot` is "placed under `os.TempDir()` (not inside the repo) so `CheckInitConflicts` never fires" — i.e. the doc already describes half the target design (`wsParent`) as the norm. Needs updating once the *whole* sandbox (not just `workspaceRoot`) moves to `/tmp`, and the "alongside it... easier to inspect on failure" rationale in the current `suite_test.go:114-115` comment needs replacing with the keep-flag/print-on-failure approach.
- **CI (`.github/workflows/test.yml:102,142`)** — just runs `make test-functional` / `make test-functional-claude-integration`; no hardcoded `.niwa-test` path. Unaffected by the move.
- **No scenario `.feature` file or step function was found that hardcodes `.niwa-test`** — grepped `test/functional/` broadly; every reference to the sandbox goes through `testState` fields (`homeDir`, `tmpDir`, `workspaceRoot`, `gitServer.root`) populated once in the `Before` hook. This is the strongest point in favor of the fix: the sandbox root is *not* leaked as a literal string anywhere in step implementations, so relocating its parent directory is mechanically a one-line change at `suite_test.go:116-117`.
- **PATH/`niwa init` conflict / `HOME` faking** — none of these depend on the sandbox being a *sibling of the binary*. `HOME`/`XDG_CONFIG_HOME`/`TMPDIR` are set from `s.homeDir`/`s.tmpDir` (`steps_test.go:104-106`), which are themselves just children of `sandbox` — wherever `sandbox` lives, these still resolve correctly. `workspaceRoot`'s independence from the binary's location is already established precedent (see `CheckInitConflicts` analysis below).
- **`noNiwaTempFilesRemain` (`steps_test.go:1189`)** scans `s.tmpDir` only (not the whole system temp dir), so it's unaffected by where `s.tmpDir`'s ancestor sandbox lives.

Conclusion: nothing found that actually requires the sandbox to be a sibling of the binary. The `suite_test.go:114-115` comment's stated reason ("makes test artifacts easier to inspect on failure") is the only real design intent being traded away, and the proposed `NIWA_TEST_KEEP_SANDBOX=1` + print-path-on-failure covers it.

### `TestMain` / godog structure

`test/functional/suite_test.go` has **no `TestMain` today** — `TestFeatures` (line 74) is a normal `*testing.T` test that is the sole `Test*` function in the package (confirmed: no other `func Test` in `test/functional/`). Adding a `TestMain(m *testing.M)` is clean: allocate the per-process root before `m.Run()`, defer/clean up after, and have `initializeScenario`'s `Before` hook read a package-level variable (or closure param) set by `TestMain` instead of deriving `repoRoot` from `binPath`. Godog's `ScenarioContext.Before` hook already receives nothing that would block this — it's a plain closure over whatever `initializeScenario`'s caller passes in, so threading a `processSandbox string` parameter through `initializeScenario(ctx, binPath, processSandbox)` is mechanical.

### `CheckInitConflicts` and `workspaceRoot`

Read `internal/workspace/preflight.go:69-107`. `CheckInitConflicts(dir)`:
1. Checks `dir/.niwa/workspace.toml` — already-a-workspace.
2. Checks `dir/.niwa/` without `workspace.toml` — orphaned state.
3. Calls `DiscoverInstance(absDir)`, which walks **upward** from `dir` looking for `.niwa/instance.json`.

None of these depend on where `dir`'s ancestors physically are, only on whether any ancestor happens to already be a niwa-managed tree. A fresh `os.MkdirTemp("", "niwa-func-")` directory has no such ancestor (assuming `/tmp` itself isn't inside one, which is exactly the same assumption the current `wsParent := filepath.Join(os.TempDir(), ...)` already relies on at `suite_test.go:141-144`/documented in `functional-testing.md:47-49`). So yes — `workspaceRoot` can simply become a child of the new per-process sandbox (e.g. `<processSandbox>/<scenario>/workspace-root`) instead of a separate `os.TempDir()`-rooted path; this also closes the second fixed-shared-path problem (`wsParent`) for free, since it moves inside the already-unique-per-process root.

## Proposed code shape

**1. `TestMain` allocates the process-wide root; `Before` allocates a scenario child.**

Replace `suite_test.go:111-150`:

```go
// package-level, set by TestMain
var processSandboxRoot string

func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "niwa-func-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating functional test sandbox: %v\n", err)
		os.Exit(1)
	}
	processSandboxRoot = root
	code := m.Run()
	if os.Getenv("NIWA_TEST_KEEP_SANDBOX") == "" {
		if err := os.RemoveAll(processSandboxRoot); err != nil {
			fmt.Fprintf(os.Stderr, "warning: cleaning up %s: %v\n", processSandboxRoot, err)
		}
	} else {
		fmt.Fprintf(os.Stderr, "sandbox kept at %s (NIWA_TEST_KEEP_SANDBOX set)\n", processSandboxRoot)
	}
	os.Exit(code)
}
```

Replaces `suite_test.go:113-118` (`repoRoot := filepath.Dir(binPath)`; `sandbox := filepath.Join(repoRoot, ".niwa-test")`; `_ = os.RemoveAll(sandbox)`):

```go
ctx.Before(func(ctx context.Context, sc *godog.Scenario) (context.Context, error) {
	sandbox, err := os.MkdirTemp(processSandboxRoot, "scenario-*")
	if err != nil {
		return ctx, fmt.Errorf("allocating scenario sandbox: %w", err)
	}
	// no RemoveAll needed — MkdirTemp guarantees a fresh, unique directory
	...
```

And `workspaceRoot` (replaces `suite_test.go:141-150`):

```go
	workspaceRoot := filepath.Join(sandbox, "workspace-root")
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		return ctx, err
	}
```

— dropping `wsParent`/`os.TempDir()`/`RemoveAll` entirely; the `CheckInitConflicts` analysis above confirms this is safe.

On failure, print the kept path (godog's `ctx.After` receives `scenarioErr`); extend the existing `After` hook at `suite_test.go:172-182`:

```go
ctx.After(func(ctx context.Context, sc *godog.Scenario, scenarioErr error) (context.Context, error) {
	s := getState(ctx)
	if s != nil && scenarioErr != nil && os.Getenv("NIWA_TEST_KEEP_SANDBOX") != "" {
		fmt.Fprintf(os.Stderr, "scenario %q failed; sandbox kept at %s\n", sc.Name, s.sandbox)
	}
	...
```

(requires adding a `sandbox string` field to `testState` — it currently isn't stored, only its children are).

**2. Makefile concurrency guard.** Wrap the three `go test` invocations (`Makefile:17-20,23-27,30-32`) with `flock`:

```makefile
test-functional: build-test
	flock $(CURDIR)/.niwa-test.lock -c ' \
		NIWA_TEST_BINARY=$(CURDIR)/niwa-test \
		go test -v ./test/functional/...'
```

using a lock file at a fixed repo-relative path is fine here — it's not a data directory, just a mutex, so no clobber risk. Also drop `rm -rf .niwa-test` (now a no-op) from all five targets (`Makefile:20,27,32,40,53`).

**3. Bounds-checked git helper.** Add to `localrepo_test.go` or a new `gitfixture_test.go`, and route every call site from the inventory table through it:

```go
// runFixtureGit runs a git command scoped to dir, refusing to run if dir is
// not inside the process sandbox (catches a caller passing a stale/escaped
// path) and pinning GIT_DIR/GIT_WORK_TREE/GIT_CEILING_DIRECTORIES so git
// never discovers a repository outside dir via upward search.
func runFixtureGit(sandboxRoot, dir string, args ...string) (string, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolving %q: %w", dir, err)
	}
	rel, err := filepath.Rel(sandboxRoot, absDir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("refusing to run git in %q: outside sandbox %q", absDir, sandboxRoot)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = absDir
	cmd.Env = append(os.Environ(),
		"GIT_DIR="+filepath.Join(absDir, ".git"),
		"GIT_WORK_TREE="+absDir,
		"GIT_CEILING_DIRECTORIES="+filepath.Dir(sandboxRoot),
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
```

Replaces: `localrepo_test.go:36,57,90,97,104`; `session_steps_test.go:696` (`runGitInDir`); `steps_workspace_config_sources_test.go:60-65,78-86`; `worktree_delegation_steps_test.go:278`.

Note: `git init --bare <repoPath>` (`localrepo_test.go:29,53`) and `git clone <url> <dest>` calls don't need `GIT_DIR`/`GIT_WORK_TREE` (they don't operate on an ambient repo), but should still pass through the sandbox bounds-check for consistency and to catch a caller error early — a thin sibling `runFixtureGitNoRepo(sandboxRoot, args...)` without the env vars covers those two cases.

**4. Stop discarding `RemoveAll` errors.** Moot once `MkdirTemp` replaces `RemoveAll`+`MkdirAll` in the `Before` hook (point 1) — there's no more `RemoveAll` on a shared path to check. If any `RemoveAll` remains (e.g. in `TestMain`'s final cleanup), it should still be logged, not silently dropped, exactly as shown in the `TestMain` snippet above.

## Findings

- The failure mechanism is not specific to `cmd.Dir` — every `-C <dir>` call site in the fixture code is equally exposed, since `-C` only sets the starting directory for git's normal upward discovery, not a hard boundary. `worktree_delegation_steps_test.go:278` and the `steps_workspace_config_sources_test.go` force-push helper carry the same latent defect as the three lines that actually fired in the incident.
- The sandbox-root string is never leaked as a literal outside `suite_test.go`'s `Before` hook — every consumer goes through `testState` fields populated there. This makes the relocation itself low-risk; the actual risk in the fix is entirely in getting the concurrency-safety (`MkdirTemp` vs `RemoveAll`) and the git-boundary hardening right, not in plumbing the new path through the suite.
- `workspaceRoot`'s existing placement under `os.TempDir()` (`suite_test.go:141-150`, documented in `functional-testing.md:47-49`) is a precedent for exactly the target design of point 1 in the proposed fix, and confirms `CheckInitConflicts` doesn't care where `workspaceRoot`'s ancestors are — only whether they happen to be niwa-managed. This directly answers the lead's open question: yes, `workspaceRoot` can simply become a child of the relocated sandbox.
- `noWorktreeIsRegisteredForRepo` (`worktree_delegation_steps_test.go:272-287`) builds its `repoPath` without the `.git`-existence check that its sibling `repoPathForGroupRepo` (line 656-692) performs before returning a path — worth aligning if the helper refactor touches this file anyway.

## Implications

- Because no scenario hardcodes `.niwa-test`, the relocation (point 1) can land as an isolated, low-diff change to `suite_test.go` plus the shared-git-helper refactor (point 3) touching four files. The `Makefile` flock (point 2) is fully independent and can land separately/first as a stopgap.
- Adding `TestMain` changes the package's test-entry semantics slightly: `go test ./test/functional/...` without `NIWA_TEST_BINARY` set still needs to skip cleanly. Since `TestMain` calls `m.Run()` unconditionally and the skip logic is inside `TestFeatures` (`suite_test.go:75-78`), this is unaffected — `TestMain` doesn't need to know about `NIWA_TEST_BINARY` at all, it just needs to always allocate/clean the process root even for a suite that ends up skipping every test.
- The `flock`-based Makefile guard (point 2) protects two concurrent `make test-functional` invocations in *one* checkout. It does **not** protect two different checkouts on the same machine — but that scenario is now fully closed anyway once `wsParent`'s `os.TempDir()`-rooted fixed path is folded into the (per-process, `MkdirTemp`-unique) sandbox, so the two mitigations compose to close both the same-checkout and cross-checkout races.

## Surprises

- `-C` not being a containment boundary is easy to miss when skimming the code — at a glance `git -C repoPath symbolic-ref ...` (`localrepo_test.go:36`) looks self-contained and safe because it names an explicit target directory, but it has the identical escape property as the bare `cmd.Dir = workDir` calls that actually caused the incident.
- The design doc (`functional-testing.md:47-49`) already states the *reasoning* for keeping `workspaceRoot` outside the repo, but the sandbox root itself (`.niwa-test`, holding `gitServer`/clone workdirs — the part that actually caused the incident) was left inside the repo. The asymmetry suggests the original author solved the `CheckInitConflicts` problem for one path and didn't generalize the "keep it out of any real tree" principle to the sibling-of-binary sandbox.

## Open Questions

- Should `runFixtureGit`'s sandbox-bounds check be a hard error (as drafted) or a `t.Fatalf`-style panic, given a caller bug here would otherwise silently corrupt the working tree exactly as in the incident — the proposed shape returns an error, which is right for git failures but arguably too soft for "this is a fixture bug," where failing loud and immediately (panic) might be safer than letting the error propagate through a chain of `fmt.Errorf` wraps that a hurried scenario author could accidentally swallow.
- Whether `GIT_CEILING_DIRECTORIES` should be `filepath.Dir(sandboxRoot)` (one level above the whole process sandbox, as drafted) or the real repo checkout's parent — the former stops upward discovery at the sandbox boundary, but if `os.TempDir()` itself is inside some ancestor niwa/git tree on a given machine, only ceiling at the actual repo root guarantees full protection. Worth checking against a machine where `/tmp` isn't a distinct mount before finalizing.

## Summary

Every fixture git call in `test/functional/` that sets `cmd.Dir` or uses `-C` (nine call sites across four files) shares the same escape defect the incident exploited — `-C` sets a starting directory, not a boundary — so the fix needs a single bounds-checked, `GIT_DIR`/`GIT_WORK_TREE`/`GIT_CEILING_DIRECTORIES`-pinned helper routed through all of them, not just the three lines that happened to fire. No scenario, `.feature` file, Makefile target beyond the now-removable `rm -rf .niwa-test`, or CI workflow hardcodes the sandbox's location, and `CheckInitConflicts`'s upward-walk semantics (already relied on by the existing `workspaceRoot`/`os.TempDir()` placement) confirm a `TestMain`-allocated `os.MkdirTemp("", "niwa-func-")` process root with `workspaceRoot` nested inside it is safe and closes both fixed-shared-path problems (`.niwa-test` and `wsParent`) at once. PR #157 (`fix/claude-integration-local-auth`) only touches `claudeIsAvailable`/`runClaudeP` in `steps_test.go` and does not intersect the sandbox-allocation or git-fixture code, so it carries no merge risk against this fix.
