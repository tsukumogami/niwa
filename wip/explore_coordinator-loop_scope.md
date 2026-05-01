# Explore Scope: coordinator-loop

## Visibility

Public

## Core Question

A coordinator delegation workflow failed on a long-running shirabe task (explore+PRD+design) on niwa 0.9.4. Three distinct failure modes appeared: stall watchdog kills before in-flight `niwa_ask` calls could fire, `niwa_ask` to a completed role looping back to the sender, and the decision protocol in the delegation body having no enforcement mechanism. We need to classify which behaviors are expected under current design, which are bugs, and what changes (if any) should be made.

## Context

Bug report from a live coordinator session delegating a shirabe explore+PRD+design workflow to a worker. The worker ran for ~50 minutes across 4 attempts (3 stall-killed, 1 completed). The stall watchdog fired at 15-minute intervals; each kill happened while the agent was deep in workflow work with no `niwa_report_progress` calls. The restart+resume mechanism worked correctly (wip/ file state persisted across restarts). The coordinator loop only received a single `question_pending` event — the final completion handoff — never any in-flight decisions.

Specific data points from the report: session files were 1.0–1.3 MB for killed attempts, 390 KB for the successful final attempt. Worker had `restart_count: 3, max_restarts: 3` at completion. The loopback-to-sender behavior on `niwa_ask` to a terminated role was observed post-task.

## In Scope

- Stall watchdog threshold and configurability
- `niwa_report_progress` expectations for workers
- `niwa_ask` routing when target role has no live session
- Task restart/resume semantics and max_restarts default
- Existing GitHub issues tracking these failure modes
- niwa 0.9.4 changelog for relevant behavioral changes

## Out of Scope

- Shirabe workflow internals (phase structure, research agents)
- Changes to `niwa_delegate` or `niwa_await_task` caller APIs
- General coordinator orchestration patterns
- Enforcement of decision protocols at the application level

## Round 2 Research Leads

7. **How does shirabe configure its stop hooks, and can a niwa stop hook coexist?**
   Shirabe is known to use Claude Code stop hooks for its own purposes. We need to understand what stop hooks shirabe installs, how Claude Code allows multiple hooks to be configured (array vs. single entry, last-writer-wins vs. merged), and whether niwa adding its own stop hook would conflict with or override shirabe's hooks.

8. **What does `claude --resume` do technically, and how would a reminder be injected on resume after a stall kill?**
   On stall kill, we want to resume the killed session rather than spawn fresh. What exactly does `--resume` preserve? Can content be appended or injected into a resumed session (e.g., as an initial user message or system prompt)? Should the injected content ask the agent to call `niwa_report_progress` immediately before resuming work?

9. **Does niwa capture the worker's Claude Code session ID, and is it available when the watchdog fires?**
   The resume-with-reminder approach requires knowing the session ID of the killed process. Does the daemon record or have access to the session ID of a spawned worker? Is it stored in task state? If not, how would niwa retrieve it at restart time?

## Research Leads

1. **What is the stall watchdog threshold and is it configurable?**
   The report shows kills at exactly 15-minute intervals. Is this hardcoded in the daemon? Is there a per-task or per-workspace override? Understanding whether this is a tunable default or a hard constant determines whether the fix is configuration or a code change.

2. **Is `niwa_report_progress` expected to be called by workers, and does niwa document this requirement?**
   The report suspects the root cause is absent progress reporting in shirabe skills. If the daemon docs or code make clear that workers must report progress to avoid stall detection, then the shirabe gap is expected behavior — and the fix lives in shirabe, not niwa.

3. **What happens to `niwa_ask` when the target role has no live session — is the loopback-to-sender intended?**
   Post-task, a `niwa_ask` to a terminated role returned the message to the coordinator's own inbox. Was this a deliberate design choice (some fallback routing) or an unhandled edge case? Check code, design docs, and any existing issues.

4. **How does the restart-and-resume mechanism work, and did it function correctly here?**
   Sessions 1–3 wrote wip/ artifacts; session 4 saw them and completed without re-running the workflow. The file size data (1–1.3 MB killed vs. 390 KB final) supports that this worked as designed. Verify against the intended restart/resume design and check whether `--resume` semantics align with what the coordinator expected.

5. **Are there open GitHub issues in niwa tracking any of these failure modes?**
   Check the niwa repo for issues tagged with stall watchdog, niwa_ask routing, or coordinator loop behavior. This tells us what's already known and prevents duplicating known tracking.

6. **What does the niwa 0.9.4 changelog say about coordinator/worker interaction?**
   Version context: did anything in 0.9.4 change the stall threshold, restart limits, or ask routing that could have introduced or exposed these behaviors?
