# Exploration Findings: coordinator-loop

## Core Question

A coordinator delegation workflow failed on a long-running shirabe task (explore+PRD+design) on niwa 0.9.4. Three distinct failure modes appeared: stall watchdog kills before in-flight `niwa_ask` calls could fire, `niwa_ask` to a completed role looping back to the sender, and the decision protocol in the delegation body having no enforcement mechanism. We need to classify which behaviors are expected under current design, which are bugs, and what changes (if any) should be made.

## Round 1

### Key Insights

- **Stall kills are deterministic, expected behavior** (stall-watchdog lead): The watchdog fires at exactly 900s with no `niwa_report_progress` call. The symmetrical 15-minute kills are not a bug — they're the heartbeat model working as designed. Workers that don't call report_progress are indistinguishable from stalled workers.

- **No per-task stall timeout override exists** (stall-watchdog lead): `MaxRestarts` is per-task (stored in `TaskState`), but `StallWatchdog` is daemon-global (via `NIWA_STALL_WATCHDOG_SECONDS` env var, startup-only). There's no field in the task envelope or `TaskState` for a per-task threshold.

- **Progress reporting is documented but injected as "guidance," not a contract** (progress-reporting lead): The niwa-mesh skill says "every 3–5 minutes or ~20 tool calls." PRD R23 calls it "skill-owned behavioral concern." But the user's position: skills shouldn't know how they're invoked — requiring shirabe to call `niwa_report_progress` breaks its abstraction. The fix belongs in niwa's delegation path, not in individual skills.

- **Restart = fresh spawn, not resume** (restart-resume lead): niwa currently restarts with `claude -p` (fresh process, fixed bootstrap prompt). Workers retrieve their task body via `niwa_check_messages` and begin from scratch — all in-session context is lost. In the bug report, the final session happened to complete quickly because shirabe had written `wip/` files to disk during the killed sessions, which that session found and skipped re-running. This was an accidental property of shirabe's implementation, not a niwa design or guarantee. A niwa worker in any other application has no such safety net: stall kill followed by fresh spawn means full context loss, and if the same work takes 15 minutes on the next attempt, it gets killed again until `max_restarts` is exhausted.

- **niwa_ask loopback is a documented fallback with bad UX** (ask-loopback lead): When `handleAsk` finds no live session for the target role, it spawns an ephemeral worker whose result routes back to the delegator. This is in `DESIGN-niwa-ask-live-coordinator.md` as intentional, but there's no typed error and callers can't distinguish "no live session" from a normal response. The 0.9.4 fix (PR #93) addressed worker→coordinator direction; coordinator→worker post-task may still fall through to this path.

- **0.9.4 not the cause** (changelog-094 lead): Stall watchdog dates to v0.9.0. 0.9.4's main relevant change (PR #93) improved ask routing for worker→coordinator, so the reporter was actually on an improved version — the stall kills are unrelated to the release.

- **Failure 1 (stall kills) is untracked; Failure 2 (ask loopback) was partially addressed** (existing-issues lead): Issue #92 and PR #93 fixed worker→coordinator ask routing. Coordinator→worker post-task loopback may be a separate untracked issue. No issue exists for stall kills on long delegated tasks or for per-task stall timeout configuration.

### Tensions

- **"Expected behavior" vs. systemic footgun**: From niwa's view, stall kills are correct. But shirabe is the primary long-running delegation target, and the system has no way to make delegated tasks resilient without either (a) modifying every skill or (b) changing the delegation/restart path. The current design puts the burden on application code that shouldn't carry it.

- **0.9.4 ask fix coverage**: PR #93 fixed worker→coordinator routing. The bug report's Failure Mode 2 is coordinator→worker post-task — a different direction. Whether both are handled or just the first needs verification.

- **Resume-with-reminder feasibility**: `claude --resume` is a Claude Code feature. Whether it's safe to use after SIGTERM/SIGKILL (session state integrity), what happens on repeated resumes, and whether a reminder can be injected into a resumed session are open technical questions.

### Gaps

- Four of six research agents couldn't write their output files (Explore subagent read-only mode). Summaries were captured in chat but not persisted. Full source reads would be needed to verify exact code paths for ask-loopback behavior and existing issue status.
- The coordinator→worker ask scenario post-task needs a dedicated code trace to confirm whether 0.9.4 addressed it.
- `claude --resume` technical constraints (session integrity after SIGKILL, maximum resume depth, reminder injection mechanism) are unexplored.

### Decisions

- Skill-level fix rejected: Requiring shirabe or other skills to call `niwa_report_progress` breaks skill abstraction. Skills shouldn't know how they're being invoked.
- niwa delegation path is the right fix location: The bootstrap prompt or task body should inject authoritative, deterministic progress reporting instructions that any Claude agent will follow — not guidance buried in a skill file.
- Restart path should change: Instead of spawning a fresh worker (`claude -p`) when a stall kill occurs, niwa should resume the killed session (`claude --resume <session_id>`) with a reminder about progress reporting injected. This preserves context and fixes the root cause in-flight.

### User Focus

The user's key concern is abstraction integrity: skills shouldn't carry operational awareness of their execution context. The fix must live in niwa's delegation and restart paths. The resume-with-reminder approach is the preferred direction for stall recovery.

## Accumulated Understanding

**What's expected (no fixes needed):** Stall kills at 15 minutes with no report_progress calls are correct watchdog behavior. The decision protocol not being enforced is by design — niwa has no mechanism to intercept agent decisions.

**What looked correct but isn't a niwa design:** The bug report's final session completing quickly was because shirabe happened to write checkpoint files across restarts. niwa played no role in that recovery — fresh-spawn restart means full context loss for any worker that doesn't implement its own checkpointing. This is not a property niwa can promise or rely on.

**What should change in niwa (three items):**

1. **Delegation instructions** (Failure 1): Inject deterministic, authoritative progress reporting instructions into every delegation — likely in the bootstrap prompt or task envelope body that workers receive via `niwa_check_messages`. Not guidance; a contract the agent reads before starting work.

2. **Stall restart path** (Failure 1): When the watchdog fires, resume the killed session (`claude --resume <session_id>`) with a reminder injected rather than spawning a fresh process. Session state (context, file writes) is preserved; the agent gets a pointed correction.

3. **niwa_ask error handling** (Failure 2): When `handleAsk` finds no live session for the target role, return a typed error (`{status: "no_live_session", role: "..."}`) instead of falling through to the ephemeral-spawn path. Callers should be able to distinguish this case.

**Possible secondary gap in niwa:** Per-task stall timeout override (symmetrical with per-task `MaxRestarts`). Low priority relative to the above three — the resume-with-reminder approach may make this unnecessary.
