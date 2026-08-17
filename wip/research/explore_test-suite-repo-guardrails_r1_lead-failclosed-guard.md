# Lead: What guard can fail closed in CI and on a developer machine at the same time, without depending on a server-side rule?

## Verified git semantics

All tests run in a scratch repo under `/home/dgazineu/.claude/jobs/b61ec711/tmp/gitexp/`, never inside the workspace. `real/` is a throwaway repo standing in for "the repo a fixture accidentally sits inside."

**Upward discovery is real and silent.** A directory with no `.git` of its own, nested under a real repo, resolves to that repo with no warning:

```
$ cd real/nested_fixture_no_ceiling && git rev-parse --show-toplevel
/home/dgazineu/.claude/jobs/b61ec711/tmp/gitexp/real
$ git rev-parse --is-inside-work-tree
true
```

This is the exact mechanism in the incident: a fixture directory without its own `.git` inherited the enclosing repo's identity.

**`GIT_CEILING_DIRECTORIES` stops it, reliably, when set correctly:**

```
$ cd real/nested_fixture_ceiling
$ GIT_CEILING_DIRECTORIES="/home/.../gitexp/real" git rev-parse --show-toplevel
fatal: not a git repository (or any of the parent directories): .git
```

Confirmed gotchas, all verified rather than assumed:

- **It does not apply to the ceiling directory itself, only to searching past it.** If cwd *is* the ceiling directory and that directory is itself a repo root, discovery still succeeds — the ceiling only blocks upward `..` traversal, it does not blind git to the repo it's standing in:
  ```
  $ cd real && GIT_CEILING_DIRECTORIES="/home/.../gitexp/real" git rev-parse --show-toplevel
  /home/.../gitexp/real     # succeeds — cwd itself is never blocked
  ```
  Consequence for the incident scenario: setting the ceiling to the real repo's root does not, by itself, stop a fixture *at* that exact path — only fixtures nested *below* it. In practice a fixture always lives below the real repo root (that's the whole failure mode), so this is not a gap for the incident's shape, but it is a gap to know about.
- **Relative paths are silently ignored** — passed `../../real` and it had no effect at all (discovery still succeeded). The env var only accepts absolute, colon-separated paths; a relative-path typo is not an error, it is a silent no-op.
- **Symlinks do not bypass a correctly-specified ceiling.** Set the ceiling to the real physical path of a repo and access the same directory through a symlinked route (`symlink_test/real_link/nested_via_symlink`) — discovery still failed as expected, because both bash's `cd` and git resolve to the physical (`realpath`) form of the cwd before comparing against the ceiling list. I could not construct a case on this system where a symlink defeated the ceiling; the documented advice to always give ceiling entries as canonical paths is consistent with what I observed, not contradicted by it.
- It's a plain process environment variable — no special scoping, no persistence, no config file. It has to be set on every invocation (or exported once in a wrapper) that must not escape its sandbox.

Docs: https://git-scm.com/docs/git#Documentation/git.txt-codeGITCEILINGDIRECTORIEScode

**`core.hooksPath` pre-push hooks fire locally and are not distributed by clone** — both confirmed:

```
$ git config core.hooksPath .githooks   # in the "developer" repo
$ git push origin main
pre-push hook ran
error: failed to push some refs to '../bare_remote.git'   # hook exited 1, push aborted before the network write
```

```
$ git clone hookstest hooksclonetest
$ git -C hooksclonetest config --get core.hooksPath
(empty)
$ ls hooksclonetest/.git/hooks/
applypatch-msg.sample  commit-msg.sample   # only stock samples — no custom hook, no hooksPath setting
```

`core.hooksPath` (and the hooks themselves, whether under `.git/hooks/` or a checked-in `.githooks/`) is repo-local config or working-tree content that `git clone` does not carry over as an active setting. A fresh clone — which is what a CI checkout and a `niwa create`'d fixture both are — starts with hooks disarmed unless something re-runs the install step after cloning.

**`git symbolic-ref HEAD` fails cleanly on a detached HEAD; `git rev-parse HEAD` does not:**

```
$ git checkout --detach HEAD
$ git symbolic-ref HEAD
fatal: ref HEAD is not a symbolic ref     # exit 128
$ git rev-parse HEAD
a3d970f...                                 # still works
```

`actions/checkout` leaves the runner in detached HEAD by default (it checks out the resolved SHA, not a branch — documented at https://github.com/actions/checkout#usage). A "HEAD didn't move" CI guard must compare `git rev-parse HEAD` (works either way), not `git symbolic-ref HEAD` (fails on a stock checkout with no test involved, which would make the guard permanently red rather than a signal).

## Mechanism comparison

| Mechanism | Catches mechanism or fingerprint | Works locally | Works in CI | Works for a NEW test tomorrow | Install cost | Would it have stopped the incident |
|---|---|---|---|---|---|---|
| `GIT_CEILING_DIRECTORIES` | Mechanism — blocks the discovery step itself | Yes, if exported before any git subprocess | Yes, same semantics, no CI-specific behavior | Yes, as long as the new test's git calls inherit the same env | Low: one env var set in `TestMain`/the sandbox harness, no per-call code | Yes — the fixture's `git add -A` would have failed "not a git repository" instead of silently targeting `real` |
| `GIT_DIR`/`GIT_WORK_TREE` set explicitly on every fixture call | Mechanism — removes discovery from the picture entirely, doesn't just fence it | Yes | Yes | No by default — only protects call sites that go through the helper that sets it; a new test that shells out to `git` directly bypasses it | Medium: every fixture git invocation must route through one helper; a stray `exec.Command("git", ...)` elsewhere in the suite is invisible to this guard | Yes for the call sites that used it; the incident happened at a call site precisely because this wasn't universal |
| `git config --global protocol.*` / `safe.directory` | Neither — these govern which repos git will *operate on* once already pointed at them (protocol allowlist, ownership mismatch), not *which repo it discovers*. `safe.directory` actually widens what git will treat as safe; it's the wrong direction for this problem | N/A | N/A | N/A | N/A | No — doesn't touch upward discovery or push targets at all |
| Test-time assertion: sandbox root has no `.git` ancestor | Mechanism, and belt-and-suspenders on top of `GIT_CEILING_DIRECTORIES` — catches the case where the ceiling was forgotten or misconfigured | Yes | Yes | Yes, if run once centrally (e.g. `TestMain`) rather than per-test | Low: one check, one place, using `git rev-parse --show-toplevel` (or equivalent) with cwd unset from any env | Yes — this is a "second lock on the same door"; it would have failed loudly at suite start before any fixture git call ran |
| CI step: fail on dirtied checkout / moved HEAD (`git status --porcelain`, `rev-parse HEAD` before/after) | Mechanism, but only after the fact — it's a tripwire, not a preventer. It catches the *symptom* (repo state changed) regardless of which new test caused it | No — this is CI-only by construction, it wraps the CI checkout, not a developer's working copy | Yes, and generalizes to any future test that writes to the checkout, not just git-shaped ones | Yes — this is the one mechanism here that requires zero foreknowledge of *how* a future test misbehaves, only that it changes the checkout | Low: a few lines of shell around the existing test step in each workflow's `test.yml`-equivalent | Yes, would have gone red on `HEAD` mismatch (branch attach + new commit) and on `status --porcelain` diff — but only reports *after* the push already happened in this org's actual job ordering (see caveat below) |
| Pre-push hook rejecting a known test identity (`niwa-test <niwa-test@example.com>`) | Fingerprint only — matches the specific author string this incident happened to use | Yes, once installed (`core.hooksPath` + committed `.githooks/`, verified above) | No — hooks are client-side; `actions/checkout` does a plain clone with no hook installer step, so CI never runs them unless a workflow step explicitly re-installs and invokes them, at which point it's really a CI *step*, not a hook | No — a differently-authored accidental commit (real developer identity, or a different bot name) sails through untouched | Low to install, but see clone caveat | Only by coincidence — only if the test happened to use that literal identity, which is not guaranteed and is trivial to break by using the ambient `user.name`/`user.email` instead |
| `git config --global core.hooksPath` set machine-wide | Same limits as the repo-local hook above, plus one more: it protects a specific configured machine, not the repo | Yes, for machines where someone remembered to set it | No — CI runners are fresh every run, nothing persists a global gitconfig entry between jobs unless a workflow step sets it, at which point again it's a CI step, not really "the hook" doing the work | No, same fingerprint limitation as above | Low per machine, but doesn't scale — every agent sandbox / CI runner / contributor laptop needs it set independently, with no repo-carried enforcement that it *was* set | No, for the same reason as above, and additionally: the incident ran the test as part of an agent/CI-shaped process, not from a machine with a hand-configured global hook |
| CI check scanning PR commits for a test identity | Fingerprint, explicitly | No — PR-commit-scanning is inherently CI/server-side, doesn't run pre-push locally | Yes, cheaply | No — same generalization failure as the pre-push hook: catches this exact author string, not the class of bug | Low: one grep-shaped Actions step | Only by coincidence, for the same reason as the pre-push hook. Also note: the incident was a **push directly to `main`**, not a PR — a PR-commit scanner never sees a push that skips PR review entirely, which is exactly what happened here |
| `GIT_ALLOW_PROTOCOL`, running tests as a different user, containerizing the test run | Mechanism, but a different mechanism than the one that broke (network transport, or filesystem/process isolation instead of git repo discovery) | Partial — depends on sandboxing infrastructure being present locally, which most developer machines and lightweight CI jobs don't have | Partial — same caveat, and adds real infra cost (a container runtime, a scratch user) that this org's CI doesn't currently use anywhere in the workflows read | Yes, if the isolation boundary is process/filesystem-level rather than git-semantics-level, so a new test's git mistake is contained regardless of what git command it runs | High: this is infrastructure, not a config line — new base images, new CI job shape, changes to how `test-functional` is invoked everywhere | Yes, structurally — if the fixture literally could not see or write to the real repo's filesystem path, the specific git commands wouldn't matter. This is the most complete answer and also the most expensive one |

## Findings

`GIT_CEILING_DIRECTORIES` plus a startup assertion is the pair that actually answers the lead question: both are pure environment/process-level guards with identical semantics locally and in CI, both work without any GitHub feature (so the Free-plan private-repo gap is irrelevant to them), and both catch the mechanism — a test resolving to a repo it has no business touching — rather than one incident's specific fingerprint. Layering them matters because `GIT_CEILING_DIRECTORIES` is easy to leave unset at a new call site (the incident's actual root cause was exactly that: one call path didn't get the discovery-blocking treatment the others had), while the assertion is a single centralized check that fires regardless of how many call sites exist or will exist tomorrow.

The CI dirty-checkout tripwire is the other mechanism that genuinely generalizes to a future, differently-shaped bug — it doesn't need to know *how* a test escapes, only that the checkout changed. Its weakness is timing, not scope: read literally, "fail when a test run dirties the checkout" is a post-hoc alarm, and in this org's actual `niwa/.github/workflows/test.yml`, the `Functional tests` step (`make test-functional`) runs as one `run:` block. A dirty-checkout check wrapped around that whole step would catch this class of bug *before the job reports success*, which is enough to stop it from being silent — but it does not stop the push itself if the runner's checkout has live credentials and network access, since the damaging `git push` already executed inside the wrapped step by the time the wrapper's post-check runs. It is a strong detection control and a weak prevention control; `GIT_CEILING_DIRECTORIES` / the startup assertion are prevention controls because they fail before the dangerous git command ever executes.

Everything identity-based — the pre-push hook, the global `core.hooksPath`, the PR-commit scanner — fails the "new test tomorrow" test explicitly asked for in the brief. All three assume the next accidental commit carries the same `niwa-test <niwa-test@example.com>` marker or the same author shape as this one. There is no reason to believe that; the org's own R18 invariant (see `sanitizeCommitEnv` in `internal/workspace/bootstrap.go`, which strips `GIT_AUTHOR_*`/`GIT_COMMITTER_*` from a specific commit path precisely so a parent process's identity can't leak in) already treats commit identity as attacker/mistake-controlled and not a thing to trust for detection. Additionally, the PR-commit scanner has a second, sharper failure: the incident was a direct push to `main`, not a PR. A control that only inspects PR commits has no view of a push that bypasses PR review altogether, which is the exact shape of what happened.

`protocol.*` and `safe.directory` are dead ends for this problem — they gate which repos git is willing to *operate on* once already pointed at a path (protocol allowlisting, ownership-mismatch trust), not *which repo git discovers* from a bare directory. `safe.directory` in particular widens trust rather than narrowing it, so it points the wrong way for a containment guard.

## Implications

For the niwa functional suite specifically (per the RCA context already established): the fix belongs at two layers, both purely process/env-level and therefore free of the GitHub Free-plan gap that blocks server-side protection on private repos. Layer one is `GIT_CEILING_DIRECTORIES` (or equivalently, explicit `GIT_DIR`/`GIT_WORK_TREE` on the fixture helper, which is stronger per-call but weaker as a safety net because it only protects call sites that remember to use it) set once in the suite's `TestMain`/sandbox setup, covering every subprocess the suite spawns regardless of which step file added it. Layer two is a startup assertion that the sandbox root itself has no `.git` ancestor, which is cheap insurance against the ceiling being forgotten, misconfigured, or bypassed by a future test that constructs its own subprocess environment without inheriting the suite's.

For the org-wide question (an eventual cross-repo guard, per lead-cross-repo-distribution's scope): a CI dirty-checkout/moved-HEAD tripwire is the one mechanism here that's worth distributing as a reusable workflow step (the org already has the `tsukumogami/shirabe/.github/workflows/*.yml@main` pattern for exactly this kind of shared check, e.g. `niwa/.github/workflows/validate-docs.yml` calling `shirabe/validate-docs.yml@main`). It's cheap, generalizes to test bugs nobody has written yet, and needs no GitHub plan tier. But it should be framed to reviewers as a detection backstop that shortens the blast radius (loud CI failure instead of a silent successful push), not as the fix — the actual fix is the prevention layer inside each repo's own test harness, which a shared CI step can't reach into.

## Surprises

`GIT_CEILING_DIRECTORIES` not applying to the ceiling directory itself was not obvious from the name and is worth calling out explicitly in whatever doc/PR describes the fix, since a reviewer skimming "ceiling" might assume it blinds git to that directory too.

The relative-path silent-no-op behavior is a real footgun: a future refactor that accidentally passes a relative path (e.g. from a helper that builds the ceiling path with `filepath.Join` against a cwd-relative base before an absolute conversion) would silently disable the guard with no error, no warning, and a suite that still passes — because without the ceiling, discovery still (correctly, in the non-broken case) finds nothing wrong 99% of the time. This is exactly the kind of guard that must be paired with the startup assertion, so a misconfigured ceiling still gets caught by something else.

The pre-push-hook clone-blindness was expected qualitatively but the ".git/hooks/ only has .sample files after clone" result is a good concrete artifact for anyone who needs convincing that hooks are not a distribution mechanism at all — not even a leaky one.

## Open Questions

Whether `GIT_CEILING_DIRECTORIES` composes cleanly with godog's process model (does the suite spawn the fixture's git calls as direct subprocesses of the Go test binary, inheriting `os.Environ()`, or does anything go through a shell that could `unset` or rewrite the env) is a niwa-suite-fix-lead question, not answered here — the RCA scope doc's proposed `fixtureGit` helper suggests the org has already converged on exactly this pairing (ceiling + explicit `GIT_DIR`/`GIT_WORK_TREE`), which this investigation's findings support as sound.

Whether the dirty-checkout CI tripwire has real false-positive risk against this org's actual workflows — e.g. `go mod tidy` legitimately rewriting `go.mod`/`go.sum` in `niwa/.github/workflows/test.yml`'s "Verify go.mod is tidy" step, which runs in the same job before the functional-test step — needs a concrete workflow edit to answer definitively; I did not attempt to draft that edit here since it's implementation, not research, but the existing job already treats "unexpected diff after a command" as a first-class pattern (`git diff --exit-code go.mod go.sum`), so a HEAD-and-status wrapper around the functional-test step specifically (not the whole job) avoids colliding with steps that are supposed to produce diffs.

## Summary

`GIT_CEILING_DIRECTORIES` combined with a startup assertion (no `.git` ancestor above the sandbox root) is the strongest fail-closed pair: both are pure process/env mechanisms with identical semantics locally and in CI, both catch the discovery mechanism itself rather than this incident's specific fingerprint, and both are cheap and verified experimentally in this investigation. Identity-based controls — pre-push hooks, global `core.hooksPath`, PR-commit scanning — all fail the "works for a new test tomorrow" bar and additionally miss this incident's shape entirely, since it was a direct push to `main`, not a reviewed PR; a CI dirty-checkout/moved-HEAD tripwire is a good, cheap, org-distributable detection backstop but fires after the dangerous command already ran, so it shortens the incident rather than preventing it.
