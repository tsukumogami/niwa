# Handoff: @channels-e2e functional test validation

## Branch / PR

- Branch: `docs/cross-session-communication`
- PR: #71 — "feat(channels): add cross-session communication via filesystem session mesh"

## Where things stand

All code, tests, and docs are written and pushed. The only remaining task is to **run the `@channels-e2e` scenarios manually on a machine where `claude` is available and `ANTHROPIC_API_KEY` is set**, then fix any failures.

Three new `@channels-e2e` Gherkin scenarios live at the bottom of `test/functional/features/mesh.feature` (lines 408–475):

| Scenario | MCP tool | Assertion |
|----------|----------|-----------|
| headless coordinator reads messages via niwa_check_messages | `niwa_check_messages` | output contains `"found:task.result"` |
| headless coordinator completes ask round-trip with simulated worker | `niwa_ask` | output contains `"answer:42"` |
| headless coordinator collects task results via niwa_wait | `niwa_wait` | output contains `"collected:2"` |

The three supporting step functions were added to `test/functional/steps_test.go`:
- `iSetUpCoordinatorSessionForInstance` — pre-registers coordinator, sets NIWA_SESSION_ID in envOverrides
- `iSetUpWorkerSessionForInstance` — pre-registers worker, stores UUID in meshState
- `iRunClaudePFromInstanceRootWithSimulatedWorkerReply` — runs coordinator `claude -p` with goroutine worker simulation

## How to run

```
# From the niwa/ repo root:
make test-functional NIWA_TEST_TAGS=channels-e2e
```

Requires:
- `claude` on PATH
- `ANTHROPIC_API_KEY` set

To run only the @critical suite (fast, no API key needed):
```
make test-functional-critical
```

## What to do if a scenario fails

1. Read the godog output carefully — it shows the exact step that failed and the stdout/stderr from the `claude -p` invocation.
2. Common failure modes:
   - **MCP server not finding session**: check `NIWA_SESSION_ID` is set in env at the point `claude -p` starts (look at `iSetUpCoordinatorSessionForInstance` in steps_test.go)
   - **Wrong output format**: the prompts tell Claude to output `FOUND:<type>`, `ANSWER:<value>`, `COLLECTED:<n>`. If Claude rephrases, tighten the prompt wording.
   - **Assertion case mismatch**: `runClaudeP` lowercases stdout, so assertions must be lowercase (they are: `"found:task.result"`, `"answer:42"`, `"collected:2"`).
   - **niwa_ask timeout**: the goroutine polls every 200ms and the ask has a 120s deadline; if the scenario times out entirely, the coordinator's `claude -p` probably hung.
3. Fix the step function or scenario prompt, commit to the same branch, and re-run.

## After tests pass

The PR is ready for review/merge. No wip/ cleanup needed (this file should be deleted before merge — CI enforces empty wip/ on merge).

## Key design decisions (for context)

See `docs/designs/DESIGN-channels-integration-test.md` for the full rationale. Quick summary:

- **Pre-registration**: `niwa session register` runs before `claude -p` so `NIWA_SESSION_ID` is in env when the MCP server starts. The `session_start` hook re-registers with a new UUID but the MCP server ignores it — it already bound to the pre-registration inbox from env.
- **Goroutine worker**: only the coordinator runs `claude -p`; the worker is simulated by a Go goroutine polling the worker inbox and writing a hardcoded `{"answer":"42"}` reply. Non-determinism from two concurrent LLM sessions is avoided.
- **Pre-seeded inbox**: for the check/wait scenarios, messages are written before `claude -p` starts; `scanExistingForWaiter` finds them on the first scan.
