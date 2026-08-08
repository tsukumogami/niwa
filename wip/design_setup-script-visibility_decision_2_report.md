# Decision 2 Report: setup-script discoverability and exit code

Prefix: `design_setup-script-visibility_decision_2` — complexity: critical (full path,
three-validator bakeoff plus cross-examination).

<!-- decision:start id="setup-script-visibility-exit-code" status="assumed" -->
### Decision: How a failed setup script is made discoverable, and what happens to the exit code

**Context**

Step 6.75 of `runPipeline` (`internal/workspace/apply.go:1580-1596`) runs each repo's setup
scripts and, on a script error, calls `a.Reporter.DeferWarn`. `DeferWarn` appends a string to
`Reporter.deferred`; it never touches a return value. The warning is then flushed by
`FlushDeferred`, which by documented contract (`internal/workspace/reporter.go:162-164`) prints
*below* the `created/applied <ws> (N repos)` summary line. So the operator reads a success
verdict first and a warning second, the command exits 0, and — because `runCmdWithReporter`
routes script output through `r.Status`, a no-op off-TTY — the script's own stdout and stderr
never appear at all.

This is not an oversight. Decision 2 of `DESIGN-post-clone-scripts.md` deliberately chose
"warn on failure, stop on first script error" so one repo's failure cannot block the others,
and that resilience is not being reversed. The question is only whether the outcome can be made
honest without losing it. A separate, already-taken decision restores script output to
`Reporter.Log`; this decision covers the *verdict*: how the failure is surfaced structurally,
and what the process exit code says.

The mechanical constraint that shapes every answer: `runPipeline` writes no state. `SaveState`
happens afterward in `Create` (`apply.go:489`) and `Apply` (`apply.go:681`), and `Create` runs
`os.RemoveAll(instanceRoot)` on *any* `runPipeline` error (`apply.go:428-431`) — documented at
`bootstrap.go:213-222` as the load-bearing contract "either `Applier.Create` returns nil OR the
instance dir does not survive". Five call sites reach the pipeline, and the last
(`instance_from_hook.go:417`) fans out to the SessionStart hook, `niwa dispatch`, and
`niwa watch` through a provisioner that discards the instance path on error.

**Assumptions**

- No scripted or CI consumer of `niwa create` / `niwa apply` exit codes exists today. Verified:
  no CI workflow, Makefile target, or `install.sh` path invokes them; none of the 30 functional
  feature files or 288 exit-code assertions involve setup scripts; no public repo in this
  workspace has a `scripts/setup/` directory. If such a consumer is named later, the deferred
  half of this decision (below) becomes warranted.
- Decision 1 (streamed, `[<repo>/<script>]`-prefixed output via `Reporter.Log`) lands in the
  same change. The counted line's wording assumes the script's own error text is already
  visible above it.
- The operator running `niwa create` / `niwa apply` is at a terminal reading stderr. This holds
  for four of the surfaces and explicitly does not hold for a dispatched worker — see
  Consequences.
- Made in `--auto` mode without author confirmation, hence `status="assumed"`.

**Chosen: Counted verdict line placed below the summary; exit code unchanged; `setup_policy` specified and deferred**

Three parts.

*1. Carry the outcome out of the pipeline as data.* Add `setupFailedRepos []string` to
`pipelineResult` (`apply.go:322-343`), a sibling of the existing `warnings []string` field which
is already used exactly this way. Step 6.75 appends a repo name to it once per repo that had any
script error, in addition to the `DeferWarn` calls it already makes. A repo is counted once even
when several of its scripts errored — a non-executable script `continue`s rather than breaking
(`setup.go:92-96`), so multi-error repos are real. `runPipeline` keeps returning a nil error, so
`os.RemoveAll` at `apply.go:430` is never reached and `state.json` is never left stale.

*2. Emit a counted, permanent line immediately below the verdict.* In `Create`, between the
summary `Log` (`apply.go:493-497`) and the `result.warnings` → `DeferWarn` loop
(`apply.go:499-501`); identically in `Apply`, between `apply.go:685-689` and `apply.go:691-693`.
Plain `Reporter.Log`, no `warning:` prefix, singular/plural branch matching the adjacent summary:

```go
if n := len(result.setupFailedRepos); n == 1 {
    a.Reporter.Log("setup incomplete for 1 repo: %s", result.setupFailedRepos[0])
} else if n > 1 {
    a.Reporter.Log("setup incomplete for %d repos: %s", n, strings.Join(result.setupFailedRepos, ", "))
}
```

*3. Leave the exit code at 0, and record `setup_policy` as the designed-but-deferred mechanism
for making it non-zero.* The full specification is written down (below, and in the design
amendment) so that adding it later is additive rather than a re-litigation: it requires no
signature change to anything this decision establishes.

**Rationale**

The stated requirement is that a failed setup script be discoverable without reading a warning
stream. The counted line satisfies it completely and is the smallest change that does: one new
`pipelineResult` field, one `Log` call in each of two functions, no signature changes, no config
surface, no behavior change for any caller.

The placement is the substance, not the wording. Today's warning is invisible *structurally* —
`FlushDeferred` prints below the summary by contract, so the failure reads as trailing noise
after a success verdict. Putting the counted line above the summary (the `healed %d dangling
plugin record(s)` idiom at `apply.go:1685`, inline in `runPipeline`) does not fix that: the
verdict would still be the last thing printed and it would still say `created myws (2 repos)`.
Worse, once Decision 1 streams 2-139 lines of script output per script, an inline line's distance
from the verdict becomes unbounded and run-dependent — the exact failure mode being fixed. All
three validators converged here; the validator who initially argued for inline placement checked
the actual order (Step 8 `healed`, Step 9 `updated`, then the return at `apply.go:1646`) and
conceded that below-both-success-lines-but-above-the-verdict is "as good as inline gets, still
worse than after the summary."

Exit 0 stays because `create`'s exit code already carries a meaning that is written down and
relied upon: "does a usable instance exist." That is `bootstrap.go:213-222`, enforced by
`os.RemoveAll` at `apply.go:430`, and depended on by every caller that discards the path on
error. A non-zero exit for an instance that *does* exist is a lie in the other direction, and it
strips the operator of the recovery path — the shell wrapper's `cd` (`shell_init.go:39-50` gates
on `$? -eq 0`), `--json`'s path, and the SessionStart session mapping are all gated on exit 0.
niwa also already commits in writing (`docs/guides/workspace-config-sources.md:262`) that a
warning-emitting apply exits 0.

`setup_policy` is deferred rather than rejected, and rather than shipped, because its warrant is
absent and its cost is not the marginal one it first appears to be. Its own validator conceded
this under cross-examination: the failed-repo list is needed by the *baseline* counted line, not
by the policy, so the shared-collection-site argument evaporates; the real marginal cost is the
config key, the cascade, generalizing `Action.UnmarshalText`'s hardcoded message, the outcome
accessor plus its reset, and five call-site decisions. That validator offered the falsifiable
trigger directly — "if no concrete scripted surface can be named today, half two is correctly
deferred" — and no such surface can be named. Decisively, a `fail` default of `warn` does not
give the issue author what they asked for: it gives them a way to ask again, and asking requires
already knowing setup can fail silently, which is the knowledge the counted line now supplies.
Because the chosen mechanism (an accessor, not an error) needs no signature change, adding it
later touches nothing this decision establishes, which makes deferral low-regret.

**Alternatives Considered**

- **Non-zero exit by default (issue option 2).** Mechanically expressible — see the trace below —
  but rejected. Even placed correctly after `SaveState`, it changes behavior for every existing
  workspace on upgrade to serve a consumer nobody has named, and it must be expressed as an
  error that four of five call sites are required *not* to treat as failure. That is the wrong
  failure direction in Go: a site that forgets the sentinel propagates the error unexamined,
  discarding the landing path at `create.go:247` and orphaning a live instance at
  `instance_from_hook.go:417` where the reap backstop deliberately excludes hook-named instances
  (`reap.go:264-266`). A site that forgets the chosen accessor merely exits 0 — today's behavior,
  loud in the terminal. It also requires a sixth `errors.As` *inside* `internal/workspace`, in
  `RunBootstrap`, the function whose doc comment states the contract being violated.
- **Non-zero exit by default plus a run-scoped `--allow-setup-failures` flag.** The revised form
  its validator adopted under cross-examination, and materially stronger than the bare version —
  it would be the fourth `--allow-*` escape hatch on `apply`, matching the house convention
  exactly. Still rejected: the escape hatch fixes the migration problem but not the semantic one
  (the exit-code bit is already spoken for), and the "default" already carries a documented
  exception — the SessionStart hook must exit 0 regardless, or it loses the `additionalContext`
  injection that is the only channel reaching the agent.
- **`--strict-setup` CLI flag (issue option 3).** Rejected on convention. niwa has no
  `--strict`-anything and three `--allow-*` flags on this exact command; a strict flag would be
  the first whose default is the lenient side and would read backwards against its three
  siblings. Where a run-scoped flag is right, the shape is `--allow-setup-failures` (above); where
  a durable posture is right, the shape is config.
- **`setup_policy = "warn" | "fail"` shipped now (issue options 4 and 5).** Not rejected —
  specified and deferred. See the specification below and the falsifiable trigger in the
  Rationale.
- **A `setup_failed: ["beta"]` field on `niwa create --json`.** Newly identified, not in the
  original option set. If a scripted consumer does appear, this is probably the cheaper and more
  precise first response than a config key: `--json` currently emits `{name, number, path}` and
  no partial-failure signal at all, and a field gives the caller the repo names rather than a
  single bit. Recorded so the deferred question is reopened with both options on the table.

**Consequences**

A human operator now sees the failure attached to the verdict rather than trailing below it, on
every surface whose Reporter writes to a terminal they are reading. The warning stream is
unchanged — the `warning: setup script ... failed for ...` line still flushes below, still names
the script — so nothing is lost, only led.

Cross-repo resilience is untouched: `runPipeline` still returns nil, every repo still gets its
turn, stop-on-first-error stays scoped within a repo. Decision 2 of `DESIGN-post-clone-scripts.md`
is narrowed, not overturned — from "warn is the only behavior" to "warn is the behavior, and the
outcome is counted in the verdict."

Two gaps remain open and should be stated in the amendment rather than papered over. A dispatched
worker still learns nothing: `realProvisionInstance` wires the Reporter to the dispatch process's
own stderr (`instance_from_hook.go:366`), which is the dispatcher's terminal, not the worker's
context — the `additionalContext` injection is built only on the SessionStart hook path
(`instance_from_hook.go:186`). And `niwa create --json` still has no partial-failure field. Both
are the scripted/automated case, which is exactly the case the deferred half was for; neither is
made worse by this decision, and the `--json` field is the more direct fix for both.

Adding fatality later costs nothing this decision spends: the accessor mechanism changes no
signature, so no call site established here has to be revisited.
<!-- decision:end -->

---

## Concrete answers to the sub-questions

### 1. Is option 2 mechanically expressible without deleting the instance or leaving state stale?

**Yes.** The signal must not travel as a `runPipeline` error — that return is guarded by
`os.RemoveAll(instanceRoot)` at `apply.go:428-431` in `Create` and by a bare `return err` at
`apply.go:576-578` in `Apply` that skips `SaveState`, `cleanRemovedFiles`, `cleanRemovedGroupDirs`,
`emitRotatedFiles`, and `saveWorkspaceRootDisclosures`.

It travels instead on `pipelineResult` (the `warnings []string` field at `apply.go:327` is the
existing precedent for exactly this plumbing) and becomes a verdict only after the state write.
Exact insertion points:

- **`Create`**: after `SaveState` at `apply.go:489`, after the summary `Log` at `apply.go:493-497`,
  and after `FlushDeferred()` at `apply.go:502` — i.e. replacing `return instanceRoot, nil` at
  `apply.go:504`.
- **`Apply`**: after `SaveState` at `apply.go:681`, after the summary at `apply.go:685-689`, and
  after `FlushDeferred()` at `apply.go:694` — i.e. replacing `return nil` at `apply.go:696`.

At those points `state.json` is written and consistent, and the instance directory is intact.

Expressible is not the same as safe, which is why option 2 is still rejected: even correctly
placed, `create.go:247` returns before `writeLandingPath` (`create.go:268`) and before the
`--json` encode (`create.go:271-283`), and `instance_from_hook.go:417` returns `provisionResult{}`,
discarding the path. Both would have to be taught the sentinel before the correct placement buys
anything.

### 2. Where is the error raised under an opt-in fatal mode, and what happens to `Create`'s signature?

**`Create` and `Apply` never return an error for a setup failure.** The outcome is exposed as
data and each CLI command decides. This was the load-bearing finding of the cross-examination:
the validator arguing for the sentinel-error shape conceded that if the outcome is read per call
rather than off a shared struct, the error shape buys nothing but a compile-time-invisible
reminder — while the sentinel's failure mode (a site that forgets it propagates the error and
orphans a live instance) is strictly worse than the accessor's (a site that forgets it exits 0,
which is today's behavior).

Shape, in `internal/workspace`:

```go
// SetupOutcome reports which repos did not finish their setup scripts during
// the most recent Create or Apply on this Applier, and whether the resolved
// policy makes that fatal to the caller.
type SetupOutcome struct {
    Failed []string
    Fatal  bool
}

func (a *Applier) SetupOutcome() SetupOutcome { return a.setupOutcome }
```

`Create` keeps `(string, error)`; `Apply` keeps `error`. The backing field **must** be zeroed at
the top of both `Create` and `Apply`: `internal/cli/apply.go:151` constructs one `Applier` and
reuses it across the instance loop at `:261-272`, the same cross-instance accumulation that
already affects `Reporter.deferred`. Because no signature changes, the `bootstrap.go:213-222`
invariant ("either `Applier.Create` returns nil OR the instance dir does not survive") stays
literally true and needs no amendment.

Per-call-site diff, if and when `setup_policy` ships:

| # | Site | Diff |
|---|---|---|
| 1 | `internal/cli/apply.go:262` | Inside the existing instance loop, after the `Apply` call, read `applier.SetupOutcome()` into a **second** slice, separate from `applyErrors`. Return it after `updateRegistry`, distinct from `combineInstanceErrors` — a converged instance must not be reported as `apply failed for N instances`, which would be false. |
| 2 | `internal/cli/create.go` | Check **after** `writeLandingPath` (`:268`) and **after** the `--json` encode: one check before `return nil` at `:281` in the `createJSON` branch, one before `return nil` at `:286`. The landing path is written and the JSON is emitted, then the command exits non-zero. |
| 3 | `internal/cli/reset.go:151` | Check after the `Reset instance: <path>` print at `:156`. |
| 4 | `internal/cli/init.go` | **Not** inside `createWrapper` — that is a seam whose error return feeds `bootstrap.go` step 3 and would abort steps 4-7 (session, scaffold, `git add`, `git commit`). Check after `runBootstrapOrchestrator` returns and after the R19 success block at `:236-250`, which carries an Appendix B byte-equality contract and must not change. |
| 5 | `internal/cli/instance_from_hook.go:417` | **No diff recommended.** `realProvisionInstance` returns `provisionResult{Name, Path}` as today, so the session mapping is written, the worker launches, and the instance stays reclaimable. Note this is one *shared provisioner* behind three callers, not one command: only `instance from-hook` (`:172`) is forced to decline — it is the surface whose status byte is read by Claude Code rather than a person, and a non-zero exit costs the `additionalContext` injection plus a permanently unreclaimable orphan (`reap.go:264-266`). Whether `niwa dispatch` (`dispatch.go:300`) and `niwa watch` (`watch.go:777`) also decline is a genuine open sub-choice, deferred with the rest; making them fatal would require `provisionResult` to grow a field their callers query. |

That asymmetry — `fail` is fatal on the operator-facing commands and inert on at least the hook
surface — is the shape's real weakness, since it is invisible from the config file. It must be
stated in the key's doc comment and in the guide, or it is a trap.

One boundary worth settling now, because it will be raised: a repo with **no** setup directory at
all is `Skipped`, not failed (`setup.go:56-59`), exactly like the `Disabled` case, so it never
enters `setupFailedRepos` and never consults the policy. A workspace-wide `fail` therefore does
not fire for a repo that was *supposed* to have provisioning scripts and doesn't — niwa has no way
to know that intent. That is a real limitation of the policy, not a bug in the cascade, and it is
one more reason the counted line (which reports what actually ran) is the load-bearing half.

Note one cost of `fail` on `niwa create` that must be documented: `__niwa_cd_wrap`
(`shell_init.go:39-50`) gates `cd` on `$? -eq 0`, so an opted-in workspace loses the automatic
`cd` into a new instance. The landing path is still written, so `niwa go` still works. Arguably
correct under an explicit opt-in — you should not silently land inside an unprovisioned instance —
but it is a behavior change the operator is buying knowingly.

### 3. Where exactly is the counted line, and what does a two-repo run look like?

Between the summary `Log` and the `result.warnings` → `DeferWarn` loop, therefore above
`FlushDeferred()`. In `Create` that is between `apply.go:497` and `apply.go:499`; in `Apply`,
between `apply.go:689` and `apply.go:691`.

Workspace `myws`, repos `alpha` and `beta`, `beta`'s second script `20-deps.sh` exits 1, non-TTY:

```
[alpha/00-hooks.sh] installing git hooks
[alpha/00-hooks.sh] done
[beta/10-fetch.sh] fetching vendor archive
[beta/20-deps.sh] resolving dependencies
[beta/20-deps.sh] error: could not resolve 'libfoo'
created myws (2 repos) → /home/u/ws/myws
setup incomplete for 1 repo: beta
warning: setup script scripts/setup/20-deps.sh failed for beta: exit status 1
```

Exit code 0. The `[repo/script]`-prefixed lines are Decision 1's territory, shown for context.
For `niwa apply` the same run reads `applied myws (2 repos)` in place of the `created` line.

Three wording choices worth recording. No `warning:` prefix: that prefix is the uniform of the
stream that failed to be read. Singular/plural branch rather than the `record(s)` parenthetical
used by `healed %d dangling plugin record(s)`, because this line sits directly beneath a line
that uses the branch idiom (`apply.go:495-497`, `:687-689`). Repo names but not script names,
because the `warning:` line immediately below already names the script.

**Not** spliced into the summary line itself (`created myws (2 repos, 1 setup incomplete) → path`):
that makes one line answer two questions, quadruples the format strings across the two functions,
and pushes the `→ <path>` recovery pointer further right, in a register (a parenthetical) the
codebase uses for incidental metadata.

### 4. Config key name, cascade, and the `setup_dir = ""` interaction

For the deferred half, specified so it can be added without re-deciding.

**Key**: `setup_policy`, values `"warn"` | `"fail"`, Go type `*config.Action` — the existing type
from `internal/config/env_example_policy.go`, not a new struct. There is one axis here, unlike
`EnvExamplePolicy`'s per-category and per-variable structure, so a bare `*Action` matches the
`*bool ReadEnvExample` idiom instead.

Name defended against the alternatives: `_policy` is already the house suffix for "an `Action`
lives here" (`env_example_policy`), and the feature's existing key is `setup_dir`, not
`setup_script_dir` — so `setup_policy` is the sibling that reads correctly next to it.
`setup_failure` names the event rather than the response; `setup_script_policy` does not match
`setup_dir`.

**Positions**: `WorkspaceMeta.SetupPolicy` immediately after `SetupDir` (`config.go:281`), and
`RepoOverride.SetupPolicy` immediately after `SetupDir` (`config.go:385`).

**Cascade**, most-specific-wins: per-repo `setup_policy` → workspace `setup_policy` → default
`warn`. Two rungs, not the env-example cascade's five. No `GlobalOverride` rung:
`GlobalOverride` (`config.go:627`) carries `EnvExamplePolicy` but not `SetupDir`, so a
personal-global rung could make other people's workspaces fatal with no same-rung way to exempt a
repo. Resolver `config.EffectiveSetupPolicy(ws *WorkspaceConfig, repoName string) Action`, called
in the Step 6.75 loop next to the existing `ResolveSetupDir`.

**`setup_dir = ""` interaction**: silently inert, **not** a validation error. `ResolveSetupDir`
returns `""`, `RunSetupScripts` sets `Disabled = true` and returns before running anything
(`setup.go:47-51`), so the repo can never contribute a failure and the policy is never consulted.
This mirrors `read_env_example = false` alongside an `env_example_policy`. More than mirroring it,
this *is* the intended exemption idiom — a workspace sets `setup_policy = "fail"` at the top level
and one repo opts out with `setup_dir = ""`. Erroring on the combination would outlaw the pattern
the cascade exists to support.

One concrete prerequisite: `Action.UnmarshalText` (`env_example_policy.go:27`) hardcodes
`invalid env_example_policy action %q (want "warn" or "fail")`. Reusing `Action` for a second key
requires generalizing that message. Verified free — no test, feature file, or doc asserts on that
string.

### 5. Smallest sufficient change, and is the opt-in required?

**Smallest**: `setupFailedRepos []string` on `pipelineResult`, populated in the Step 6.75 loop
that already exists, and one `Log` call in each of `Create` and `Apply`. No signature changes, no
config, no exit-code change, no call-site behavior change. Roughly fifteen lines plus tests.

**The opt-in fatal mode is additive scope, not required.** The counted line fully satisfies "a
failed setup script is discoverable without reading a warning stream" on its own.

**Recommendation: do not include it in this change.** Ship the counted line; specify `setup_policy`
in the design amendment as the mechanism of record should fatality ever be wanted; reopen when a
concrete scripted consumer of the exit code can be named, and evaluate the `--json` field against
it at that point. The reasons, in order of weight: no such consumer exists today anywhere in this
repo or workspace; the accessor mechanism means adding it later touches nothing this change
establishes, so deferral is low-regret; and a policy defaulting to `warn` does not actually serve
the operator who motivated the issue, since opting in requires already knowing setup can fail
silently — which is precisely the knowledge the counted line now supplies for free.

The honest counter, recorded because it is not weak: `niwa create --json` and a dispatched worker
get no signal under this decision, and an exit code is the only thing a CI wrapper can read. If
that surface materializes, the `--json` field is likely the better first answer, and
`setup_policy` the second.
