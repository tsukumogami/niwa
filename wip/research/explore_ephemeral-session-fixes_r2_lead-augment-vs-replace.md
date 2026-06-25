# Lead: AUGMENT vs REPLACE — what concretely changes in the ephemeral-session feature under each option, and does #171 evaporate if hook auto-provisioning is retired?

Context: the new blessed path is a niwa command that pre-creates an instance and
launches `claude --bg` rooted inside it (full per-instance settings fidelity +
Agent View management). The open question is what to do with the EXISTING
SessionStart-hook auto-provisioning that shipped in PR #169.

- **AUGMENT** — keep hook auto-provisioning as a best-effort net for un-wrapped
  sessions; fix #171/#172 just enough not to misfire/orphan.
- **REPLACE** — retire hook auto-provisioning; the niwa command is the only
  provisioning path.

## Findings

### 1. Current hook-provisioning surface, and what becomes DEAD code under REPLACE

The hook-provisioning machinery, traced by caller:

**SessionStart provisioning chain (dies entirely under REPLACE):**
- `runInstanceFromHook` SessionStart branch — `internal/cli/instance_from_hook.go:118-128` dispatches `hook_event_name` to `runInstanceHookStart`.
- `runInstanceHookStart` — `instance_from_hook.go:139-183`: validates session_id, resolves workspace root, runs the guard, provisions, writes the mapping, emits the injection. This is the entire auto-provision entry point.
- `sessionStartGuardPasses` — `instance_from_hook.go:248-273`: the three-part guard (master switch + `isBackgroundWorker` + re-entrancy). **Used by exactly one caller** (`runInstanceHookStart`, line 154 — confirmed by grep: the only non-test references are the definition and that one call).
- `isBackgroundWorker` — `instance_from_hook.go:281-296`: the #171 signal (job-state `template == "bg"`). **Only caller is `sessionStartGuardPasses` (line 255).**
- `buildSessionStartInjection` + `sessionStartInjection` struct — `instance_from_hook.go:301-340`: the cd+inject text (#172b). **Only caller is `runInstanceHookStart` (line 175).**
- `realProvisionInstance` / `provisionInstanceFunc` — `instance_from_hook.go:100, 364-408`: the auto-clone-on-SessionStart provisioner. **Only caller is `runInstanceHookStart` (line 159).** (The blessed niwa command would have its OWN create path; this specific wrapper exists only to provision from the hook.)

**Shared / survives REPLACE (used by the reaper and/or SessionEnd teardown too):**
- `readJobState` / `decodeJobState` — `job_state.go:116-158`: read by BOTH `isBackgroundWorker` (dies) AND `sessionLive` (reaper, survives). The file stays; only the `Template` field's guard-consumer goes away.
- `sessionLive` + `jobLivenessTTL` + `terminalJobStates` — `job_state.go:18-109`: reaper liveness only (`reap.go:133`). **Independent of hook provisioning — survives.**
- `EphemeralSessionMode` master switch — `session_map.go:37-42`, `state.go:117`: read by the guard (`instance_from_hook.go:250`, dies) AND written by init/apply into root state + settings. The switch's role as a *guard gate* dies; its role as a *workspace posture flag* is re-evaluated in §4.
- `SessionMapping` store (`WriteSessionMapping`/`ReadSessionMapping`/`DeleteSessionMapping`/`ListSessionMappings`) — survives: the reaper (`reap.go:96-99,167`) and SessionEnd both consume it. Under REPLACE the *writer* moves from the hook (`instance_from_hook.go:171`) to the niwa dispatch command, but the store itself is unchanged.

**Net dead-code under REPLACE:** `runInstanceHookStart`, `sessionStartGuardPasses`, `isBackgroundWorker`, `buildSessionStartInjection`+struct, `realProvisionInstance`/`provisionInstanceFunc`, the `Template` field's guard use, and `sessionNamePrefixLen`/`namePrefix` plumbing. That is the whole SessionStart half of `instance_from_hook.go` (~roughly lines 131-183, 237-340, 360-408). The SessionEnd half, the reaper, the mapping store, and `readJobState` survive.

### 2. Does #171 fully evaporate under REPLACE? — YES

#171 is the wrong-guard-signal bug: `template == "bg"` is empirically unstable across 2.1.x (false-negative when it carried the agent name, false-positive when stamped `"bg"` on every session — round 1). The guard exists *only* to decide whether SessionStart should auto-provision. Grep confirms `isBackgroundWorker` has exactly one caller (`sessionStartGuardPasses`), which itself has exactly one caller (`runInstanceHookStart`). Under REPLACE nothing auto-provisions on SessionStart, so **there is no remaining need to detect a background worker at SessionStart.** The reaper's liveness check (`sessionLive`) reads `State`/`UpdatedAt`, NOT `Template` — so killing the guard does not touch the reaper. #171 evaporates completely; it is not merely deprioritized.

### 3. What of #172 survives under REPLACE? — #172a (root-hoist) survives as an independent fix; #172b dies

#172 has two halves:
- **#172b (cd+inject can't deliver settings):** the SessionStart injection tells the agent "cd into the instance," but Claude Code's settings/plugins/permissions are already locked in at process launch from the launch root, so a later `cd` can't re-apply them (round 1: the hook can't re-root). This is `buildSessionStartInjection`. It **dies with the SessionStart path under REPLACE** — the blessed command launches `claude --bg` already rooted in the instance, so there is nothing to inject.
- **#172a (root scaffold drops Plugins/Marketplaces):** confirmed live and **independent of augment/replace.** `writeRootSettings` (`root_materializer.go:112-155`) computes the full effective config via `MergeInstanceOverrides` (line 113) but forwards only `Settings`, `BaseDir`, `IncludeGitInstructions`, `UseAbsolutePaths`, `Reports`, `SessionHooks` to `buildSettingsDoc`. It passes **no `Plugins`, no `Marketplaces`, no `RepoIndex`** (compare the instance path, which sets all three: `workspace_context.go:338-339`, `materialize.go:670-672`). `buildSettingsDoc` only emits `enabledPlugins` when `cfg.Plugins` is non-empty (`materialize.go:528-534`) and `extraKnownMarketplaces` when `cfg.Marketplaces` is non-empty (537-555). So the workspace-ROOT `.claude/settings.json` gets hooks + permission posture but **no plugins/marketplaces.**

The decisive question for #172a: does the workspace-ROOT coordinator session (the one running `claude agents` / dispatching) still want plugins/marketplaces in its own config? **Yes.** Under both AUGMENT and REPLACE the coordinator runs `claude` at the workspace root and loads the root `settings.json`. A coordinator that dispatches work but has no plugins/marketplaces of its own is degraded regardless of how workers get provisioned. **#172a is a root-coordinator-fidelity fix that stands on its own** — it should be fixed whether or not hook auto-provisioning survives. (Caveat from round 1: github-sourced marketplaces hoist with two field-forwards, but `repo:<name>/<path>` directory marketplaces have no root-stable path at init — they `os.Stat` a cloned-instance path — so the fix must filter/defer those or run at root-scope `apply` where a `RepoIndex` exists.)

### 4. Under REPLACE, does SessionEnd / teardown still need to exist?

Two backstops reclaim instances today: SessionEnd teardown (`runInstanceHookEnd`, `instance_from_hook.go:194-235`) and the reaper (`reap.go`). They are redundant by design — SessionEnd is the fast path, the reaper is the liveness backstop. Under REPLACE:

- **The mapping is still written** — but by the niwa dispatch command at dispatch time, not by the hook. The reaper (`sessionLive` on job-state liveness) still reclaims orphans. So **teardown correctness does not depend on the SessionEnd hook.**
- **SessionEnd is still useful as a faster-reclaim optimization** (immediate destroy on clean exit vs. waiting up to `jobLivenessTTL` = 30 min for the reaper). It is NOT required for correctness. `runInstanceHookEnd` is already self-contained: it resolves the instance by session_id from the mapping (never from cwd), only destroys `Ephemeral:true` mappings, and is best-effort (always exits 0). It has no dependency on the SessionStart guard or `isBackgroundWorker`.

**Recommendation for the hook block under REPLACE: remove the SessionStart hook entry, keep the SessionEnd hook entry.** This is a clean asymmetry — `buildSettingsDoc` (`materialize.go:488-502`) currently emits both events from one `SessionHooks` injection, so this needs a small split (emit SessionStart only when a provisioning policy wants it; always/optionally emit SessionEnd as a fast-reclaim hook). Keeping SessionEnd costs nothing and speeds reclamation; the reaper remains the correctness guarantee. If simplicity is preferred over reclaim latency, both hooks can be dropped and the reaper alone suffices — but keeping SessionEnd is the lower-risk default since it's already correct and guard-free.

### 5. Under AUGMENT, what is the MINIMAL #171/#172 fix — and is AUGMENT even safely achievable?

This is the crux. Round 1 established there is **no race-free, correct guard signal at SessionStart on 2.1.191**:
- `source` and `agent_type` are identical for coordinator and worker.
- `template == "bg"` (the current #171 signal) is wrong in BOTH directions across 2.1.x.
- The semantically-correct `sessionKind:"bg"` lives in the transcript JSONL, which is async-flushed AFTER the SessionStart hook fires (racy, #56631).
- The only race-free job-state-resident candidate is the currently-unread `backend:"daemon"` field, and round 1's biggest open question is whether interactive sessions ALSO carry `backend:"daemon"` post-2.1.139 — i.e. whether it's even a clean discriminator.

So the "minimal AUGMENT fix" is not a small patch — it is a bet on an unvalidated signal. Two options:
- **(a) Switch the guard to `backend == "daemon"`** — race-free IF interactive sessions never carry it. Unconfirmed. If they do, the coordinator self-provisions an orphan on every launch (the exact misfire AUGMENT is supposed to avoid).
- **(b) Combine signals + lean on the master switch + reaper** to absorb residual false positives — accepts that the guard WILL occasionally misfire and relies on the reaper to clean up. This means AUGMENT cannot promise "never misfire on the coordinator"; it can only promise "misfires get reaped within the TTL."

**AUGMENT's whole value proposition is being a safe best-effort net.** A net that can wrongly provision an instance for the coordinator session (consuming a clone + vault cost, then orphaning it until reaped) is not obviously net-positive over having no net at all — especially once the blessed command exists and is the documented path. The lack of a correct SessionStart signal **pushes toward REPLACE by elimination**: you cannot build a safe augment net on a signal that round 1 proved doesn't exist. The only way AUGMENT becomes safe is if `backend:"daemon"` turns out to be a clean discriminator on the pinned version — which is an open empirical question, not a code fix.

### 6. Migration / compat (PR #169 installed state in the wild)

- **`niwa init` installs this by default.** `init.go:766-772`: unless `--no-ephemeral-sessions` is passed, init calls `MaterializeWorkspaceRoot` with `EphemeralSessionMode: true`, writing the root `settings.json` (with both SessionStart and SessionEnd hooks) and setting `ephemeral_session_mode: true` in root state (`init.go:1007-1017`).
- **`niwa apply` rewrites root config unconditionally and idempotently.** `apply.go:193-199`: at workspace-root scope, `MaterializeWorkspaceRoot` runs on every apply with `EphemeralSessionMode: workspace.EphemeralSessionMode(scope.WorkspaceRoot)` (reads the persisted flag). The comment at `apply.go:190-192` confirms it rewrites via unconditional `os.WriteFile` every time. **This is the convergence lever:** changing what `writeRootSettings` emits (drop SessionStart hook, add Plugins/Marketplaces, keep/adjust SessionEnd) means any existing workspace converges to the new model on its next `niwa apply` — no manual migration, no state-format change.
- **Installed state to consider:** any workspace `niwa init`'d since PR #169 (cb16c4c) has the SessionStart+SessionEnd hooks in its root `settings.json` and `ephemeral_session_mode: true` in root state. Under REPLACE, the next `apply` rewrites `settings.json` to drop the SessionStart hook automatically. The `ephemeral_session_mode` state flag can be repurposed (does the workspace opt into ephemeral instances at all / does the blessed command default on) rather than removed, avoiding a state-schema migration. No `.gitignore` or branch-merge concern — these are local workspace files, not committed repo artifacts.

## Implications

- **REPLACE is the low-risk default.** It deletes the entire #171 surface (no guard signal needed at all), deletes #172b (cd+inject), and converges cleanly via the existing unconditional root-`apply` rewrite. The reaper + mapping store + SessionEnd already provide correct teardown without the SessionStart hook.
- **#172a must be fixed regardless.** The root coordinator session wants its own plugins/marketplaces under either option. This is a `writeRootSettings` two-field-forward (github sources) plus a filter/defer for `repo:`-sourced marketplaces — orthogonal to the augment/replace decision.
- **AUGMENT is gated on an unvalidated signal.** It cannot be "minimally fixed" without first proving `backend:"daemon"` is a clean coordinator-vs-worker discriminator on the pinned version. Until that's proven, AUGMENT's net can misfire on the coordinator — undercutting its own reason to exist.
- **Keep SessionEnd, drop SessionStart** is the recommended hook posture under REPLACE: SessionEnd is already guard-free and correct, and gives faster reclaim than the 30-min reaper TTL at zero added risk.

## Surprises

- The reaper's liveness signal (`sessionLive`, job-state `State`/`UpdatedAt`) is a *completely different* field set from the #171 guard signal (`isBackgroundWorker`, job-state `Template`). They share only the `readJobState` reader. So killing the broken `Template` guard leaves the reaper's liveness logic fully intact — REPLACE is much cleaner than "rip out the job-state integration" would suggest.
- `realProvisionInstance` (the hook's auto-clone path) is a near-duplicate of the regular create flow (`config.Discover` -> `config.Load` -> `applier.Create`). The blessed niwa command would reuse the real create path directly, so this wrapper is pure dead weight under REPLACE.
- The root scaffold already *computes* the full effective config (`MergeInstanceOverrides`, root_materializer.go:113) — it just throws away Plugins/Marketplaces before the write. #172a is a forwarding omission, not a missing computation.

## Open Questions

- (Blocks AUGMENT) Does an interactive/foreground session carry `backend:"daemon"` in job state on 2.1.191? If no, AUGMENT has a race-free guard and becomes viable; if yes, there is no safe SessionStart guard and REPLACE is forced. (Round 1's central open question — unresolved.)
- Under REPLACE, do we keep the SessionEnd hook (faster reclaim, recommended) or drop both and rely solely on the reaper (simpler, up-to-30-min reclaim latency)? Requires splitting `buildSettingsDoc`'s single `SessionHooks` injection (`materialize.go:488-502`) so the two events can be emitted independently.
- For #172a, where do `repo:`-sourced marketplaces resolve for the root — defer hoisting to root-scope `apply` (where a `RepoIndex` exists after instances clone) vs. filter them out at `init`? (Carried from round-1 root-scaffold-fields.)
- Should the `ephemeral_session_mode` state flag be repurposed (workspace-opts-into-ephemeral / blessed-command-default) rather than removed, to avoid a state-schema migration on existing PR #169 workspaces?

## Summary
Under REPLACE the entire SessionStart auto-provision chain becomes dead code (`runInstanceHookStart`, `sessionStartGuardPasses`, `isBackgroundWorker`, `buildSessionStartInjection`, `realProvisionInstance`), #171 evaporates completely because the broken `template=="bg"` guard has exactly one caller and the reaper's liveness check reads a different field set, and #172b (cd+inject) dies with it — while the reaper, mapping store, and the already-correct guard-free SessionEnd teardown all survive. AUGMENT cannot be safely "minimally fixed" because round 1 proved there is no race-free correct guard signal at SessionStart on 2.1.191, so any net it provides can misfire on the coordinator and orphan instances — pushing toward REPLACE by elimination; #172a (root scaffold drops Plugins/Marketplaces) is a coordinator-fidelity fix that survives independently of the decision and converges via the existing unconditional root-`apply` rewrite. The biggest open question is whether `backend:"daemon"` cleanly discriminates worker from coordinator on the pinned version — the single empirical fact that would either rescue AUGMENT or confirm REPLACE.
