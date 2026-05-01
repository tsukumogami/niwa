# Exploration Decisions: coordinator-loop

## Round 1

- Skill-level fix rejected: Skills should not carry awareness of their invocation context (niwa delegation vs. direct use). Requiring shirabe or other skills to call `niwa_report_progress` breaks the abstraction boundary — the fix must live in niwa.
- Fix location: niwa's delegation path and restart path own the progress reporting contract, not individual skills.
- Stop hook as primary defense: Configure a Claude Code stop hook in the worker's workspace to call `niwa_report_progress` automatically at every turn boundary. No skill or agent awareness required; resets the watchdog as invisible infrastructure.
- Restart behavior target: Resume killed sessions (`claude --resume`) with a reminder injected, rather than spawning a fresh process (`claude -p`). Preserves context, corrects root cause in-flight. Last-resort layer if stop hook didn't fire frequently enough.
- Scope narrowing: wip/ checkpoint pattern and decision protocol enforcement are excluded from the fix scope. wip/ recovery in the bug report was a shirabe coincidence, not a niwa design; decision protocol is by design.
- Failure 3 (decision protocol not enforced) closed: accepted as by-design; no fix needed.
