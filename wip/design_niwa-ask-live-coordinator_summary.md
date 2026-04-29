# Design Summary: niwa-ask-live-coordinator

## Input Context (Phase 0)
**Source:** /explore handoff
**Problem:** `niwa_ask(to='coordinator')` always spawns an ephemeral worker instead of routing to the active coordinator session, silently breaking approval gates and causing deadlocks when a coordinator is blocking on `niwa_await_task` while a worker asks a question.
**Constraints:**
- Response mechanism must be `niwa_finish_task` (not `niwa_send_message`) — this unblocks the worker's `awaitWaiter` channel
- No timeout/fallback-to-spawn; questions queue until the coordinator next polls
- Both `niwa_check_messages` and `niwa_await_task` must be delivery points for questions
- Session auto-registration needs to be addressed (current manual CLI path is fragile)
- Skill content lives in generated Go code (`buildSkillContent()` in channels.go), not markdown

## Current Status
**Phase:** 0 - Setup (Explore Handoff)
**Last Updated:** 2026-04-29
