# Phase 2 Research: Ops/concurrency analyst (refresh race)

(Findings returned by a read-only research agent; saved in substance by the orchestrator.)

## Lead 1: Writers under the workspace-root .niwa/ outside materializeAndSwap
### Findings
- materializeAndSwap (snapshotwriter.go:353-474): fixed .next staging with preflight remove; fetch (clone :389 / tarball :378); marker (:423); point-in-time copies of instance.json (:443), dispatch-briefs (:454), sessions (:464); SwapSnapshotAtomic (:469). SwapSnapshotAtomic (snapshot.go:37-102): preflight remove .prev (:53), testfault (:58), target->prev (:69), staging->target (:77, rollback :79), fsync, remove prev (:98). No lock.
- Non-GitHub refresh swaps every call (:161-167). A dispatch refreshes twice: ReconcileAndReloadConfig (instance_from_hook.go:454 -> configreload.go:66) then Applier.Create (apply.go:489). Apply per instance (apply.go:640); global dir (apply.go:964, cli/config_set.go:76); overlays (overlaysync.go:40,51). Root refreshers: cli/apply.go:178, create.go:172, reset.go:115, instance_from_hook.go:454 (dispatch, hook, watch at watch.go:781).
1. WriteSessionMapping (session_map.go:141-167; callers dispatch.go:877, instance_from_hook.go:217): between another's preserve and swap -> mapping lost silently. Between its WriteFile and Rename across a swap -> rename ENOENT -> dispatch errors (foreground retain marker dispatch.go:885-895; detached rollback destroys instance). In the rename window, its MkdirAll recreates .niwa/sessions -> swap rename and rollback fail ENOTEMPTY -> .niwa holds only the mapping; real snapshot only in .niwa.prev (deleted by the next swap's preflight). Microsecond window, catastrophic outcome.
2. DeleteSessionMapping (reap.go:487): between another's preserve and swap -> mapping resurrected after its instance was destroyed. Opportunistic reap runs at the start of every dispatch/create/watch (dispatch.go:492, create.go:187, watch.go:778), so reap and refresh overlap in parallel dispatch.
3. SaveState(workspaceRoot) (state.go:297-313) is non-atomic (truncate+write). saveWorkspaceRootDisclosures (apply.go:2727-2748, from :571, :750) is a long read-modify-write from a start-of-command read (apply.go:481, :664, errors ignored); a failed/transient read makes it write a fresh InstanceState that clobbers EphemeralSessionMode, ConfigNameOverride, OverlayURL. A truncated instance.json can be preserve-copied into staging.
4. WriteProvenance in the GitHub no-drift branch (snapshotwriter.go:186-193; provenance.go:60-66 O_TRUNC, no tmp+rename): concurrent ReadProvenance can parse a partial marker; rename window -> ENOENT "refresh marker".
5. Watch state .niwa/watch-handled and .niwa/watch/ (watch/state.go:19,23,160-198,335-386) are niwa-local state in the rotated dir that is never preserved: every refresh wipes them (sibling defect, not concurrency-dependent).
6. Dispatch briefs are written by the /dispatch skill (rootskills/dispatch/SKILL.md:92), not niwa; same lost-write race; niwa cannot lock for them.
7. Root materialization writes <root>/.claude etc., not .niwa. Single-instance layout (root is the instance; cli/apply.go:358-369) would put lifecycle records in the rotated dir; needs confirmation.
8. Refresh vs refresh on fixed .next/.prev: A's preflight deletes B's staging (ENOENT "missing staging path" or mixed content); B's .prev preflight can delete A's rollback copy; B's Lstat in A's window sees target absent then A's rename fails ENOTEMPTY. The 4-parallel functional scenario avoids a config source for this reason (session-message-acceptance.feature:326-333).
### Implications
- Serialize preserve+swap per config dir against other refreshes and against niwa's local-state writers (mapping write/delete, root SaveState, watch state).
- A reap delete must not be resurrected. No writer may recreate .niwa in the rename window.
- Preserve set is incomplete (watch). Atomic writes for root instance.json and the marker.

## Lead 2: Readers exposed to a transient .niwa
### Findings
- ListSessionMappings returns nil,nil on ENOENT: absent == empty. ephemeralInstancePaths (state.go:447-477) same.
- Reap mapped pass fails safe on empty. Backstop pass (reap.go:572-671): empty read makes every dispatch-named instance "unmapped"; remaining gates are retain marker, 30-minute TTL, instanceHasLiveJob, instanceHasRecordedSession. A mapped idle resumable instance older than 30 minutes could be destroyed. A permanently lost mapping has the same effect.
- niwa list degrades cosmetically. EphemeralSessionMode reads false on any LoadState error (hook silently inert). config.Discover may walk to an enclosing workspace. provenanceMarkerExists false -> refresh skipped. Create's initState nil -> wrong effective name, clobbering disclosure write.
### Implications
- Reaper destructive decisions must not rest on a mapping read taken during a swap; either the reaper serializes with the swap or distinguishes absent from empty and spares.

## Lead 3: Lock design constraints
### Findings
- codex_trust_lock_unix.go:13-57: LOCK_EX|LOCK_NB poll 20ms, 30s timeout, O_CREATE 0600, never unlinked, per open file description (contends in-process). Location ~/.niwa/locks/codex-trust-<hash>.lock (codex_trust.go:633-644). _other.go is a documented no-op.
- Build: goreleaser linux+darwin; CI ubuntu+macos with go test -race; functional Linux only. unix/!unix pairs keep Windows compiling -> new lock needs an _other.go twin.
- Lock must live outside the rotated dir (a lock inside would be renamed away). Options: sibling <configDir>.lock, or ~/.niwa/locks/snapshot-<hash(EvalSymlinks(configDir))>.lock. EnumerateInstances ignores non-dirs.
- Holding the lock across fetch (tarball + HeadCommit backoff 0.5+1+2s; remote clones; two refreshes per dispatch) serializes parallel dispatches and risks the 30s timeout. Alternative: fetch into unique staging unlocked, lock only preserve+swap (ms); sweep stale .next-*/.prev-* under the lock; optional skip if the same commit was already swapped.
### Implications
- Requirement states what is serialized, not the mechanism; per config dir; outside the rotated dir; bounded wait with clear error; correct within one process; non-unix documented gap; hold time independent of fetch time.

## Lead 4: Test harness
### Findings
- Unit: overlaysyncMakeBareRepo (overlaysync_test.go:59-81) gives a file:// bare repo (non-GitHub -> swap every refresh; used by fallback_test.go). planSnapshotWorkspace + driftedFetcher (snapshotwriter_sessions_test.go) for GitHub drift. configreload_test.go marker helpers.
- testfault (NIWA_TEST_FAULT) only injects errors/truncation, cannot pause; labels snapshot-swap, fetch-fallback, fetch-tarball, head-commit, extract-entry. No seam holds a refresh between preserve and swap; a blocking fake fetcher pauses before preserve so doesn't reproduce. Deterministic repro needs a test-only hook (package var) between preserve and swap, following driftCheckBackoff / provisionInstanceFunc override patterns. A goroutine stress test would also fail on main with high probability.
- Functional steps exist: a local git server is set up; a config repo "X" exists with body; I run niwa init from config repo "X"; a fake claude for dispatch that mints a new session per launch; I run "..." N times in parallel under a pty; all parallel runs exit 0; there are N dispatch mappings that record session-message acceptance. The 4-parallel scenario can be recomposed with a file:// config source (probabilistic repro). A generic "there are N dispatch mappings" step is trivial to add.
### Implications
- Deterministic unit test needs a new pause hook; plus a probabilistic functional test. Reap-resurrection test needs the same hook.

## Summary
Nothing serializes the root .niwa swap: refreshes collide on fixed .next/.prev, and mapping writes/deletes, root instance.json writes and the GitHub no-drift marker rewrite can be lost or resurrected in another process's preserve->swap window; a MkdirAll in the rename window can gut .niwa; watch state is never preserved. A lock outside the rotated dir, held only for preserve+swap with fetch into unique staging, plus a test-only pause hook, is the viable shape.
