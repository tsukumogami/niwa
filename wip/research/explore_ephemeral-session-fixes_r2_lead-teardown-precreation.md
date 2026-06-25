# Lead: Does the existing teardown + mapping + reaper machinery still work if niwa PRE-CREATES the instance and writes the session->instance mapping at create time (instead of the SessionStart hook writing it)?

## Findings

### 1. The mapping store is agnostic about *who* writes it — pre-creation works for the store + SessionEnd teardown branch

`WriteSessionMapping` (`internal/workspace/session_map.go:83-109`) takes a plain `SessionMapping` struct and persists it atomically to `<workspaceRoot>/.niwa/sessions/<session_id>.json`. Nothing about it cares whether the caller is the SessionStart hook or `niwa` at dispatch time. The only constraint it enforces is `ValidSessionID(m.SessionID)` (must be a canonical lowercase UUID) via `sessionMappingPath` (`session_map.go:71-76`). `Created` is auto-stamped if zero (`session_map.go:88-90`).

The SessionEnd teardown branch `runInstanceHookEnd` (`internal/cli/instance_from_hook.go:194-235`) reads the mapping purely by `session_id`:
- validates `payload.SessionID` (`:199`),
- resolves the workspace root from the hook's `cwd` (`:203`),
- `ReadSessionMapping(workspaceRoot, payload.SessionID)` (`:209`),
- destroys only if `mapping.Ephemeral` is true (`:218`),
- destroys `mapping.InstancePath` via `destroyInstanceFunc` (`:222-223`),
- deletes the mapping (`:231`).

So if niwa pre-writes a mapping keyed by the bg session id with `Ephemeral: true` and a valid `InstancePath`, SessionEnd **will** resolve and destroy it correctly — provided the hook fires (see point 4) and provided the key is the FULL UUID (see point 2).

**Fields niwa must populate at dispatch time:**
- `SessionID` — REQUIRED, must be the full canonical UUID. This is the key; an invalid value is rejected by `WriteSessionMapping`.
- `InstancePath` — REQUIRED and load-bearing. SessionEnd guards `if mapping.InstancePath != ""` (`instance_from_hook.go:222`); the reaper joins instances to mappings on `InstancePath` (`reap.go:100-104, :116`). An empty path means SessionEnd no-ops the destroy and the reaper can never join this mapping to its instance record.
- `Ephemeral: true` — REQUIRED. Both SessionEnd (`:218`) and the reaper (`reap.go:112, :129`) refuse to destroy anything without it.
- `InstanceName` — not read by teardown or reaper; metadata/UX only. Safe to populate, not load-bearing.
- `TranscriptPath` — written by the SessionStart path (`instance_from_hook.go:168`) but **never read** anywhere in teardown or the reaper. Pure metadata. niwa can leave it empty under pre-creation with no teardown consequence.
- `Created` — auto-stamped if zero; optional.
- `Label` — optional metadata, never used for teardown.

Net: the mapping contract is `SessionID` + `InstancePath` + `Ephemeral:true`. Everything else is cosmetic.

### 2. ID-format mismatch risk: the mapping MUST be keyed on the FULL UUID, not the short `claude --bg` id

This is the sharpest correctness hazard under pre-creation.

The mapping store keys on the full UUID: `sessionMappingPath` rejects anything that isn't a full canonical UUID via `ValidSessionID` / `sessionUUIDRe = ^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$` (`session_map.go:20-27, :71-76`). A 8-char short id like `1e3503bc` **fails `ValidSessionID`**, so `WriteSessionMapping` returns an error and writes nothing. SessionEnd, which receives the full UUID, would then `ReadSessionMapping` on the full UUID and miss (file named by short id would not exist anyway). Result: the instance is never torn down by SessionEnd, and — worse — the reaper can't help either because `sessionLive`/`readJobState` are also driven off the mapping's `SessionID`.

The job-dir layout confirms the relationship: `readJobState` (`job_state.go:116-144`) tries `<jobsDir>/<full-id>/state.json` first, then **falls back to scanning for a dir whose name is a PREFIX of the session id** (`:128-142`: `sessionID[:len(name)] == name`). The codebase's documented empirical layout is "the dir name is the session-id prefix" (guide `:84-85`, `:101-104`; `job_state.go:21`, `:95-98`). So the short `claude --bg` printed id (e.g. `1e3503bc`) is the **prefix** of the full UUID, and the full UUID is the canonical key everywhere in niwa.

**Implication for niwa at dispatch:** niwa captures the short id from the `claude --bg` launch, but it must persist the mapping under the FULL UUID. niwa therefore needs to recover the full UUID before writing the mapping. Two viable sources, both already present on disk:
- Read `~/.claude/jobs/<short-id>/state.json` and take its `sessionId` field (the full UUID) — this is exactly what `readJobState` returns and what `isBackgroundWorker` already trusts (`instance_from_hook.go:281-296`).
- Or defer the mapping write until the full id is known.

If niwa instead naively keyed the mapping on the short id, teardown would silently break (write rejected) — this is the one place pre-creation can go wrong quietly.

### 3. The reaper liveness check works as-is for `claude --bg` sessions (with the same full-UUID caveat)

The reaper (`reap.go`) joins on-disk instance records (`EnumerateInstanceRecords`) against session mappings by `InstancePath` (`:91-105`), and for each ephemeral instance with a mapping it calls `sessionLive(jobsDir, mapping.SessionID, now)` (`reap.go:133`). `sessionLive` (`job_state.go:86-109`):
- reads job state via `readJobState` (handles the prefix-dir fallback, so the short bg dir name resolves from the full UUID key),
- confirms `js.SessionID == sessionID` when recorded (`:99`),
- DEAD if state is terminal (`terminalJobStates`, matched case-insensitively, `:42-55`, `:102`),
- DEAD if `updatedAt` is older than `jobLivenessTTL = 30m` (`job_state.go:18`, `:105`),
- otherwise LIVE.

We confirmed on the live machine that `claude --bg` sessions DO produce `~/.claude/jobs/<id>/state.json` with `state`, `updatedAt`, `backend:"daemon"`. niwa reads `sessionId`, `template`, `state`, `updatedAt` (`job_state.go:30-35`); `backend` is ignored. So:
- While the bg session runs and rewrites state.json, `updatedAt` stays fresh and state is non-terminal → LIVE → spared. Correct.
- When the bg session ends, either the job dir is removed (`readJobState` miss → DEAD), or the state goes terminal, or `updatedAt` goes stale past 30m → DEAD → reaped. Correct.

**TTL / terminal-state nuances:**
- The terminal-state set is broad and case-insensitive but **not exhaustive** — an unrecognized terminal label (e.g. some future `state: "stopped"`) is treated as non-terminal, so reclamation falls back to the 30-minute TTL. A bg session that ends cleanly but leaves a non-terminal-looking state with a stale `updatedAt` is reaped after up to 30 minutes, not immediately. That's a delay, not a leak.
- If the daemon keeps `updatedAt` warm after the session logically ended (a backend that heartbeats independent of session life), liveness could read LIVE longer than desired. This is an **open risk worth confirming for the `backend:"daemon"` bg case specifically** — does `updatedAt` stop advancing promptly when the worker finishes? If the daemon keeps touching it, neither the terminal-state nor the TTL path fires and the instance lingers. (Round 1 confirmed state.json exists with `state`/`updatedAt`; what it doesn't confirm is whether `updatedAt` reliably goes stale at session end under the daemon backend.)
- Same full-UUID caveat as point 2: the reaper's liveness key is `mapping.SessionID`. If the mapping were keyed on the short id it would also be the wrong key for `readJobState`'s exact-match fast path (it would only resolve via the prefix scan if the short id happened to be a dir name, which it is — but the mapping write would have failed first, so the mapping wouldn't exist at all).

### 4. (Highest stakes) The workspace-ROOT SessionEnd hook almost certainly does NOT fire for a `claude --bg` session rooted in the INSTANCE — teardown falls to the reaper

This is the decisive finding.

**Where the SessionEnd hook lives.** The SessionStart/SessionEnd hooks are written ONLY into the workspace-ROOT `.claude/settings.json`. `MaterializeWorkspaceRoot` → `writeRootSettings` is the sole production caller that passes `SessionHooks` to `buildSettingsDoc` (`root_materializer.go:117-130`). The hooks block is emitted only when `cfg.SessionHooks != nil && sh.Command != ""` (`materialize.go:488-502`).

**The INSTANCE settings do NOT carry the session hooks.** The instance-root materializer is `InstallWorkspaceRootSettings` (`workspace_context.go:242`, called from `apply.go:1279`). Its `buildSettingsDoc` call (`workspace_context.go:334-345`) sets `Settings`, `InstalledHooks`, `ResolvedEnvVars`, `Plugins`, `Marketplaces`, `RepoIndex`, `BaseDir`, `IncludeGitInstructions`, `UseAbsolutePaths`, `Reports` — but **NOT `SessionHooks`**. So a materialized instance's `.claude/settings.json` has no SessionStart/SessionEnd hook entry. (The name `InstallWorkspaceRootSettings` is misleading — it targets the INSTANCE root, per its own doc comment and the comment at `root_materializer.go:72`.)

**Settings resolution for an instance-rooted bg session.** Claude Code resolves `.claude/settings.json` from the session's working root (and its parent chain). The validated model has niwa launching `claude --bg` *rooted in the instance* (settings resolve from the instance; the session shows up in Agent View under the instance). A session rooted in the instance loads the INSTANCE's `.claude/settings.json` — which carries no SessionEnd hook. Whether Claude walks up to the workspace-root settings is the load-bearing unknown:
- Claude Code's project-settings discovery is anchored at the session's project root, not an unbounded walk to filesystem root. The instance directory is the project root for an instance-rooted session, and it has its own `.claude/settings.json`. There is no mechanism by which the workspace-root `.claude/settings.json` (a level ABOVE the instance) is layered into an instance-rooted session's hook set.
- Contrast with the *current* (hook-driven) model: there the bg session is launched at the workspace ROOT, the SessionStart hook fires from the root settings, provisions the instance, and injects a `cd` instruction — the session's project root stays the workspace root, so the root SessionEnd hook is in scope at end. **Pre-creation changes the launch root from workspace-root to instance**, which is precisely what removes the root SessionEnd hook from scope.

**Consequence:** under pre-creation with an instance-rooted launch, the workspace-root SessionEnd hook does **not** run for the session. SessionEnd teardown (`runInstanceHookEnd`) never executes for these sessions. Teardown falls **entirely to `niwa reap`** — the reaper backstop that the design already documents as the guarantee (the SessionEnd hook was always "best-effort"; guide and `reap.go:19-36`). The good news: the reaper is self-sufficient given a correct mapping (full-UUID key + `InstancePath` + `Ephemeral:true`), runs opportunistically at the start of `niwa create` (`reap.go:181-192`), and on demand. So the model still reclaims — just on the reaper's cadence, not instantly at session end.

**Could we fix it by adding the SessionEnd hook to the instance settings?** Yes in principle — teach `InstallWorkspaceRootSettings` to pass `SessionHooks` so the instance's own `.claude/settings.json` carries a SessionEnd entry. But note the SessionEnd handler resolves the workspace root from the hook `cwd` (`instance_from_hook.go:203`, `resolveHookWorkspaceRoot` → `ClassifyCwd`) and then reads the mapping from `<workspaceRoot>/.niwa/sessions/`. For an instance-rooted session the hook `cwd` is the instance; `ClassifyCwd` must still resolve the enclosing workspace root from inside an instance for the mapping read to land. That needs verification but is plausible (`ClassifyCwd` already classifies instance cwds elsewhere). There's also a SessionStart concern: if the instance settings also carried the SessionStart hook, an instance-rooted session would re-fire SessionStart — but the re-entrancy guard (`sessionStartGuardPasses` step 3, `instance_from_hook.go:266-270`) already no-ops when cwd resolves inside an instance, so a SessionStart hook in the instance settings would be inert. So adding the hook to instance settings is feasible, but it's a *change*, not something that works as-is.

### 5. Net: teardown under pre-creation

**Works as-is (no code change):**
- The mapping store (`WriteSessionMapping`/`ReadSessionMapping`/`Delete`/`List`) — agnostic to who writes it; accepts a niwa-authored mapping.
- The reaper (`reap.go`) + liveness (`sessionLive`) — fully self-sufficient given a correct mapping; works for `claude --bg` job-state files; runs opportunistically on `niwa create` and on demand. This becomes the PRIMARY teardown path under pre-creation.
- The SessionEnd teardown branch logic itself (`runInstanceHookEnd`) — correct IF it runs.

**Must change / must be handled by niwa at dispatch:**
1. **Key the mapping on the FULL UUID, not the short `claude --bg` id.** niwa must recover the full UUID (read `~/.claude/jobs/<short-id>/state.json`'s `sessionId`) before `WriteSessionMapping`, or the write is rejected and teardown silently breaks. (Point 2.)
2. **Populate `InstancePath` + `Ephemeral:true`** (and `SessionID`=full UUID). `TranscriptPath`/`InstanceName`/`Label` are optional metadata. (Point 1.)
3. **Accept that SessionEnd likely won't fire** for an instance-rooted bg session, so the reaper is the teardown guarantee, not a backstop. If instant teardown at session-end is required, add `SessionHooks` to the INSTANCE materializer (`InstallWorkspaceRootSettings`) AND verify `ClassifyCwd` resolves the workspace root from an instance cwd so the SessionEnd handler can find the mapping. (Point 4.)
4. **Confirm daemon-backend `updatedAt` staleness.** Verify that a finished `claude --bg` (backend:"daemon") session's `state.json` either goes terminal or stops advancing `updatedAt`, so the reaper's liveness eventually reads DEAD. If the daemon heartbeats indefinitely, the 30m TTL never trips and the instance leaks. (Point 3.)

## Implications

- Pre-creation is viable for teardown because the reaper was always the guarantee and it's mapping-driven, not hook-driven. The SessionEnd hook was best-effort; pre-creation just makes "best-effort" closer to "never fires" for instance-rooted sessions.
- The single highest-risk implementation detail is the **id format**: capturing the short id at dispatch but keying the durable mapping on the full UUID. Get this wrong and BOTH teardown paths break (SessionEnd misses, reaper can't join), silently.
- Teardown latency shifts from "at session end" to "next `niwa create` or next manual `niwa reap`." For a fan-out workload that calls `niwa create` repeatedly, the opportunistic reap keeps the instance count self-bounding, so this is acceptable. For a long-lived workspace that dispatches one session and stops, an orphaned instance can linger until something triggers a reap.

## Surprises

- `TranscriptPath` is written into the mapping by the current SessionStart path but is **read by nothing** in teardown or the reaper — it's dead metadata. Pre-creation can omit it freely.
- The reaper's liveness already handles the prefix-named job dir (`readJobState` prefix scan), so the short-id job dir resolves fine FROM the full-UUID key — the asymmetry is only on the mapping-write side, where the full UUID is mandatory.
- `InstallWorkspaceRootSettings` (which despite its name materializes the INSTANCE root) deliberately omits SessionHooks, so the instance never self-installs the teardown hook — this is the structural reason teardown can't ride the instance.

## Open Questions

1. Does Claude Code layer a parent directory's `.claude/settings.json` (workspace root) into an instance-rooted session's hooks, or is settings discovery anchored at the instance project root? (Strongly believed to be anchored at the project root → root SessionEnd hook out of scope, but worth a direct empirical check: launch `claude --bg` rooted in an instance and observe whether the root SessionEnd hook fires.)
2. For `claude --bg` with `backend:"daemon"`, does `state.json`'s `updatedAt` reliably go stale (or `state` go terminal) promptly when the worker finishes? If the daemon keeps it warm, the 30m TTL never trips.
3. Does `ClassifyCwd` resolve the enclosing workspace root when called with an instance directory as cwd? (Needed if we choose to install the SessionEnd hook into the instance settings as a fix.)
4. Does niwa have reliable access to the full UUID at dispatch time (from the job state file), or only the short printed id? (The fix in point 5.1 depends on this.)

## Summary
Pre-creation works for the mapping store and the reaper as-is, but only if niwa keys the durable mapping on the FULL session UUID (recovered from `~/.claude/jobs/<short-id>/state.json`'s `sessionId`) with `InstancePath` + `Ephemeral:true` populated — keying on the short `claude --bg` id silently breaks both teardown paths because `WriteSessionMapping` rejects non-UUID keys. The workspace-root SessionEnd hook almost certainly does NOT fire for an instance-rooted bg session (the instance materializer omits SessionHooks and settings resolve from the instance, not the root), so teardown falls entirely to `niwa reap`, which is self-sufficient and runs opportunistically on every `niwa create`. The biggest open question is whether a finished `backend:"daemon"` bg session reliably lets its job-state `updatedAt` go stale or `state` go terminal — if the daemon heartbeats indefinitely, neither the terminal-state nor the 30-minute TTL liveness path trips and the instance leaks.
