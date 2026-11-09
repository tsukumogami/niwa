# Handover: coordinator-dispatch-profile

Staging note for the session that continues this unit. It lives under wip/
and is deleted before the pull request merges; nothing committed references
it.

## Where things stand

- Branch: `docs/coordinator-dispatch-profile`. The head sha is the commit
  that adds this file.
- Drafted and committed:
  - `docs/designs/DESIGN-coordinator-dispatch-profile.md`. It settles the
    carrier and the list of safe items with evidence, states the three
    Evidence checks, and covers the coordinator's dispatcher.
  - `docs/plans/PLAN-coordinator-dispatch-profile.md`. Six steps,
    `multi-pr`: steps 1 to 5 in niwa end in a release, and step 6 is in
    shirabe.
- Not done:
  - No `/scope` chain has run. No BRIEF or PRD hop, no `/design` or `/plan`
    child sessions, and no consolidation judgment.
  - No pull request, no CI run, no review round recorded.
- The drafts were written by hand while `/scope` was blocked. The chain should
  treat them as inputs to its hops, not as finished hop output.

## Where /scope stopped

- Session: `scope-coordinator-dispatch-profile`, state `resume_route`, not
  terminal. Opened with `--auto --intent=continue`.
- Leg: `req-c28015cc-af23-407d-a596-717d4c99c314:scope`.
- Block: the `ladder` gate (the resume probe) times out at its 30 s limit.
  The probe runs `skill-session.sh has-work plan coordinator-dispatch-profile`,
  which calls `koto status plan-coordinator-dispatch-profile`. That lookup
  lists every workspace prefix in the cloud-sync bucket.
  - At 11:31 it failed: `listing 08348f6df51641c9/plan-coordinator-dispatch-profile/ failed: attohttpc: Io Error: connection timed out`.
  - At 11:45 it answered correctly (`workflow ... not found`) after 3m40s.
  - The upstream fix is koto#523, with #609 and #610.
- Per the coordinator: no more koto writes on that session from the first
  worker, don't run `koto session index audit`, and don't bypass `/scope` with
  separate `/design` or `/plan` sessions.

## Evidence already gathered

Claude Code 2.1.319, launched from the instance root. The figures are in the
design's Evidence section.

- `claude --bg` turn-0 context: 51.5K plain, 21.2K under the profile document.
- Under the profile, ToolSearch loaded SendMessage, CronCreate, CronList and
  TaskStop. Each one ran.
- Both sessions armed niwa's keep-alive with CronCreate, the profiled one with
  ScheduleWakeup denied.
- With the Skill tool denied, a leading `/shirabe:writing-style` prompt still
  expanded the skill (print mode). Denying Skill cut about 10K on its own.
- `ENABLE_CLAUDEAI_MCP_SERVERS=false` in the settings env dropped the
  claude.ai connector and kept the plugin and workspace MCP servers.

## Open questions

1. Once koto#523 lands, should the continuation re-tick
   `scope-coordinator-dispatch-profile` from `resume_route`, or replace that
   session with a fresh `/scope coordinator-dispatch-profile --auto
   --intent=continue` run on the same leg?
2. Should the chain keep the plan's step 6 (the shirabe dispatcher change) in
   this PLAN, as drafted, or move it to a separate unit?
