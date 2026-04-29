# Design summary: await-timeout-context

## Input context (Phase 0)
**Source:** GitHub issue tsukumogami/niwa#88
**Problem:** niwa_await_task timeout responses and terminal results omit fields
that are already in TaskState, leaving coordinators without the context they need
to reason about in-flight and completed tasks.
**Constraints:** No new MCP tools; no changes to state.json schema; no parsing of
transitions.log (that adds complexity disproportionate to the gain); progress body
must remain non-persisted (security guarantee).

## Current status
**Phase:** 0 - Setup
**Last updated:** 2026-04-29
