# Decision 4 (standard): Total-staged cap — counting, config, composition

## Question
How are live staged agents counted across runs, what is the config surface and
default, and how does the cap compose with the per-run bound?

## Evidence
- `DefaultPerRunBound = 3` (select.go:14) caps NEW stages per pass only; `Select`
  truncates `kept[:bound]` and treats `bound <= 0` as "use default" (select.go:36-64).
- `ListStagedHandles` counts records, but records are never deleted today, so a
  record-count cap would overcount (dead/dismissed sessions still counted).
- Liveness for a watch instance = `instanceHasLiveJob(jobsDir, instancePath)`
  (job_state.go:110) — needs the record to carry `InstancePath` (Decision 1).
- Config pattern: `GlobalSettings.WatchSandbox string toml:"watch_sandbox,omitempty"`
  (registry.go:59), resolved at the use site by `resolveSandboxMode` (watch.go:47-60);
  no central validator. Default lives as a const in `internal/watch`.

## Decision
- **Count live, not records.** The cap counts staged records whose instance still has
  a live job (`instanceHasLiveJob` over `record.InstancePath`). Dead records are
  pruned each pass (staged-record GC), so the count reflects actually-live agents and
  the record store stops growing unbounded — closing the monotonic-growth gap the
  investigation flagged.
- **Only fresh stages consume cap.** Continuing a live-idle session (Decision 2)
  reuses an already-counted agent, so it is not blocked by and does not increment the
  cap (PRD R4/R12).
- **Config.** Add `WatchMaxStaged int toml:"watch_max_staged,omitempty"` to
  `GlobalSettings` beside `WatchSandbox`; resolve at the watch use site with a
  `resolveMaxStaged` mirroring `resolveSandboxMode` (default when <= 0, reject
  negatives with a hard error). Plain `int` (not `*int`) since Select already treats
  non-positive as "use default". Default const `DefaultMaxStaged = 5` in
  `internal/watch` next to `DefaultPerRunBound` — modestly above the per-run bound of
  3; each staged agent is a full contained instance, so 5 simultaneous is already
  substantial. Tunable via `watch_max_staged`.
- **Composition.** In `runWatchOnce`, between `LoadHandledSet` and `Select`: prune
  dead records (GC), compute `liveCount`, `remaining = maxStaged - liveCount`; if
  `remaining <= 0`, take the "nothing to stage" path; else pass
  `min(DefaultPerRunBound, remaining)` as `Select`'s bound. Select's existing
  truncation enforces both bounds in one place, preserving oldest-first order and
  leaving un-staged PRs unrecorded (PRD R12 backfill).

## Alternatives
- Count records without GC: rejected — overcounts dead sessions, cap drifts wrong.
- `*int` tri-state config: rejected — Select's `<=0`-means-default convention makes a
  plain int the closer fit; no need to distinguish explicit-0 from unset.
