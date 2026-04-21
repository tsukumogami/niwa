# Design Summary: channels-integration-test

## Input Context (Phase 0)
**Source:** Freeform topic (from user request)
**Problem:** No end-to-end test proves that a live `claude -p` session can load the MCP tools provisioned by `niwa create --channels` and exchange messages via them.
**Constraints:**
- Must be deterministic (no two concurrent LLM sessions racing)
- Must not require permanent workspace.toml changes
- Must be tagged separately from @critical to avoid mandatory API usage in CI
- Must reuse existing test step patterns (meshState, runClaudeP, callMCPTool)

## Key Decisions
- Pre-register coordinator session and set NIWA_SESSION_ID in env before claude -p
- Pre-seed inbox for check/wait scenarios (Scenarios 1 and 3)
- Use test goroutine to simulate worker for ask/answer scenario (Scenario 2)
- session_start hook re-registration is harmless: MCP server keeps watching pre-registration inbox

## Current Status
**Phase:** 6 - Complete
**Last Updated:** 2026-04-21
