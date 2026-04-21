---
status: Proposed
problem: |
  The channels feature has no end-to-end tests that exercise actual coordination
  through live headless Claude sessions. All existing tests either verify filesystem
  artifacts (sessions.json, .mcp.json) or call the MCP server directly via
  JSON-RPC without involving Claude Code. Whether the MCP tools are genuinely
  accessible from `claude -p` — and whether a coordinator session can act on
  messages received through those tools — is untested.
decision: |
  Add three @channels-e2e Gherkin scenarios that drive real `claude -p` sessions
  as the coordinator. Each scenario provisions a plain workspace with
  `niwa create --channels`, pre-registers the coordinator session to obtain a
  stable NIWA_SESSION_ID, seeds the coordinator inbox or a simulated worker,
  and asserts on the text the coordinator emits to stdout. A new
  iSetUpMeshSessionsForInstance step handles session pre-registration and env
  wiring in a single reusable operation.
rationale: |
  Pre-seeding the coordinator inbox (rather than running two concurrent claude -p
  processes) gives deterministic, fast tests with no race conditions. A goroutine
  simulates the worker in the ask/answer scenario, matching the existing
  theCoordinatorAsksWorkerAndReplies pattern. Using NIWA_SESSION_ID in env
  before claude -p starts ensures the MCP server watches the correct inbox even
  if the session_start hook re-registers the coordinator under a new UUID.
---

# DESIGN: End-to-end channels integration test scenarios

## Status

Proposed

## Context and Problem Statement

The channels feature ships three layers of verification:

- Unit tests: `channels_test.go`, `channels.go` channel provisioning logic
- Functional tests: `@critical` scenarios in `mesh.feature` verify that
  `niwa create --channels` provisions `.niwa/sessions/`, `.claude/.mcp.json`,
  hooks, and the workspace context file; that session registration and message
  routing work via direct MCP JSON-RPC calls; and that the daemon lifecycle
  is correct.

What none of these tests prove is that a **live Claude session** launched with
`claude -p` can actually find and use the MCP tools that channels provisioning
writes. The gap matters because:

1. `.claude/.mcp.json` must be in a location Claude Code searches.
2. The MCP server must start correctly with `NIWA_INSTANCE_ROOT` baked in.
3. `niwa_check_messages`, `niwa_wait`, and `niwa_ask` must be callable from
   within a headless session and return meaningful output.
4. The coordinator must be able to act on the content of received messages.

A regression in any of these — wrong MCP path, broken JSON, server crash on
startup — would not be caught by the existing tests.

## Decision Drivers

- Tests must be deterministic and not depend on timing of two concurrent
  `claude -p` processes.
- Tests must run without requiring any permanent workspace.toml changes
  (`niwa create --channels` is sufficient).
- Tests should not cost API credits on every CI run; they belong under a
  separate `@channels-e2e` tag.
- The Go implementation should reuse existing helpers (`runClaudeP`,
  `callMCPTool`, meshState) and follow the patterns in `steps_test.go`.
- New Gherkin steps should be self-contained so each scenario reads naturally
  without assuming prior step context.

## Considered Options

### Session setup: Pre-register vs rely on session_start hook

**Option A — Rely on session_start hook only**

Let `claude -p` register the session via the hook. Don't set `NIWA_SESSION_ID`
before launching Claude. The MCP server starts without knowing its inbox, so
`niwa_check_messages` immediately returns `"NIWA_SESSION_ID not set"`.

Verdict: unusable. The MCP server must have `NIWA_SESSION_ID` to serve any
meaningful tool call.

**Option B — Pre-register and set NIWA_SESSION_ID in env (chosen)**

Run `niwa session register` from the test, capture the UUID, set
`NIWA_SESSION_ID=<uuid>` in envOverrides before `claude -p` starts. The MCP
server (subprocess of `claude`) inherits this env var and watches the correct
inbox. When the `session_start` hook fires and re-registers with Claude's PID
(pruning the dead subprocess entry), the MCP server continues using the
pre-registration inbox.

Pre-seeded messages in that inbox are still visible to the server. The test
goroutine in Scenario 2 writes the worker reply directly to that inbox dir,
bypassing sessions.json routing. This works because the goroutine has the
inbox path from meshState, not from sessions.json.

**Option C — Modify MCP server to discover session ID by parent PID**

Have the server look up its own session entry from sessions.json by matching
its parent process PID (the Claude Code PID). No env var pre-setting needed.

Verdict: cleaner long-term but requires a production code change that is out
of scope for this test infrastructure work.

### Worker simulation: Concurrent claude -p vs test goroutine

**Option A — Two concurrent claude -p sessions**

Start worker and coordinator in separate goroutines, both running `claude -p`.
The worker waits for a message via `niwa_wait`, then replies.

Verdict: non-deterministic. Two LLM sessions exchange messages with no
guaranteed ordering. Test runtime is unpredictable and failure modes are hard
to diagnose. Coordination protocol reliability belongs in a separate manual
integration test.

**Option B — Test goroutine simulates worker (chosen)**

The goroutine polls the worker inbox for the ask message, writes a hardcoded
reply directly to the coordinator inbox, then exits. Only the coordinator runs
`claude -p`. This matches `theCoordinatorAsksWorkerAndReplies` in the existing
suite.

Deterministic: the reply is written as soon as the ask appears, with no LLM
latency on the worker side. The test proves the coordinator's ability to use
`niwa_ask` and process the response, which is the goal.

### Inbox seeding: Pre-seed vs live round-trip for check/wait scenarios

**Option A — Live round-trip (two sessions)**

Run a background `claude -p` worker that sends a message, then coordinator
reads it. Same concurrency problems as above.

**Option B — Pre-seed inbox (chosen)**

Write message files directly to the coordinator inbox before `claude -p`
starts. The coordinator's `niwa_check_messages` or `niwa_wait` call finds
them immediately. Test is deterministic and fast.

## Decision Outcome

Three `@channels-e2e` scenarios in `test/functional/features/mesh.feature`:

| Scenario | MCP tool tested | Worker side | Assert |
|----------|----------------|-------------|--------|
| S1 — check messages | `niwa_check_messages` | Pre-seeded inbox | output contains `"task.result"` |
| S2 — ask/answer | `niwa_ask` | Test goroutine replies | output contains `"42"` |
| S3 — wait/collect | `niwa_wait` | Pre-seeded inbox (2 msgs) | output contains `"2"` |

All three use `niwa create --channels` on a plain workspace (no `[channels.mesh]`
in config). Each scenario is tagged `@channels-e2e` and is skipped automatically
when `claude` is not on PATH or `ANTHROPIC_API_KEY` is unset (via the existing
`claudeIsAvailable` step).

## Solution Architecture

### Session setup step

A new step `iSetUpCoordinatorSessionForInstance(ctx, instanceName)` centralises
all pre-launch wiring:

1. Resolves `instanceDir = workspaceRoot/<instanceName>`
2. Sets `s.envOverrides["NIWA_INSTANCE_ROOT"] = instanceDir`
3. Runs `niwa session register --role coordinator` (subprocess; exits immediately)
4. Parses `session_id=<uuid>` from stdout
5. Creates `<instanceDir>/.niwa/sessions/<uuid>/inbox/` (done by register)
6. Sets `s.envOverrides["NIWA_SESSION_ID"] = <uuid>` and `NIWA_SESSION_ROLE = "coordinator"`
7. Initialises `meshState{coordinatorID: <uuid>, instanceRoot: instanceDir}`

After this step, `runClaudeP(s, instanceDir, prompt)` produces a `claude -p`
process whose MCP server inherits all three env vars and watches the correct inbox.

For Scenario 2 (ask/answer), a separate `iSetUpWorkerSessionForInstance(ctx, instanceName)`
step registers the worker, stores its UUID in meshState, and leaves envOverrides
pointing at the coordinator (so subsequent `claude -p` runs as coordinator).

### Inbox pre-seeding (Scenarios 1 and 3)

The existing `nMessagesPlacedInCoordinatorInbox` step already writes message files
to `meshState.coordinatorInbox`. No new infrastructure needed.

### Simulated worker goroutine (Scenario 2)

A new step `iRunClaudePFromInstanceRootWithSimulatedWorkerReply` wraps the
coordinator `claude -p` launch and the worker simulation in a single operation:

```
1. Start claude -p for coordinator via cmd.Start() (not cmd.Run())
2. Write the JSON-RPC input to stdin pipe
3. Goroutine: poll workerInboxDir for question.ask message
4. Goroutine: write question.answer reply to coordinatorInboxDir
5. Goroutine: close stdin pipe after writing reply
6. cmd.Wait() — coordinator processes the answer, prints result, exits
7. Store stdout/stderr/exitCode in testState
```

The coordinator inbox and worker inbox dirs come from meshState. This is
structurally identical to `theCoordinatorAsksWorkerAndReplies` in `steps_test.go`,
except the coordinator side uses `claude -p` instead of raw JSON-RPC.

### Env var propagation through MCP subprocess

The `.claude/.mcp.json` written by `InstallChannelInfrastructure` sets only
`NIWA_INSTANCE_ROOT` in the `env` section. Claude Code supplements, not replaces,
the MCP server's environment with those vars. Because `NIWA_SESSION_ID`,
`NIWA_SESSION_ROLE`, and `NIWA_INBOX_DIR` are inherited from the `claude -p`
process env, the server receives all required vars without any `.mcp.json` changes.

### session_start hook re-registration

When `claude -p` starts, `mesh-session-start.sh` runs `niwa session register`.
It finds the coordinator entry from pre-registration (PID of the test subprocess,
which has exited), prunes it as stale, and registers a new coordinator entry with
Claude's PID and a new UUID. Sessions.json now has coordinator → new-UUID.

This new UUID does NOT affect the MCP server, which already has `NIWA_SESSION_ID=pre-uuid`
from env and watches `<pre-uuid>/inbox/`. Incoming messages pre-seeded or written
by the test goroutine go to `<pre-uuid>/inbox/` directly, so the MCP server sees them.

For Scenario 2, `niwa_ask` sends to "worker" by reading sessions.json. The worker
entry was registered later (test process PID, alive), so it's still present. The ask
message lands in `<worker-uuid>/inbox/`, which the test goroutine polls. ✓

## Implementation Approach

### Phase 1 — New step functions in steps_test.go

1. `iSetUpCoordinatorSessionForInstance(ctx, instanceName)` — described above
2. `iSetUpWorkerSessionForInstance(ctx, instanceName)` — registers worker role,
   stores UUID in meshState, does NOT change envOverrides (coordinator env stays set)
3. `iRunClaudePFromInstanceRootWithSimulatedWorkerReply(ctx, instanceName, prompt)` —
   wraps coordinator `claude -p` + goroutine worker, like `theCoordinatorAsksWorkerAndReplies`

### Phase 2 — Step registrations in suite_test.go

Register three new steps:
```go
ctx.Step(`^I set up coordinator session for instance "([^"]*)"$`, iSetUpCoordinatorSessionForInstance)
ctx.Step(`^I set up worker session for instance "([^"]*)"$`, iSetUpWorkerSessionForInstance)
ctx.Step(`^I run claude -p from instance root "([^"]*)" with simulated worker reply and prompt:$`,
    iRunClaudePFromInstanceRootWithSimulatedWorkerReply)
```

### Phase 3 — Gherkin scenarios in mesh.feature

Three `@channels-e2e` scenarios, each self-contained with their own workspace,
`niwa init`, `niwa create --channels`, and session setup.

### Phase 4 — Update functional testing guide

Add a "Testing with headless Claude sessions" section to
`docs/guides/functional-testing.md` explaining the `@channels-e2e` tag, the
session setup steps, and when pre-seeding vs goroutine worker applies.

## Security Considerations

These are test-only additions. No production code changes. The new step functions
run in the sandboxed test environment (isolated `homeDir`, `tmpDir`,
`workspaceRoot`). Pre-registered sessions use UUIDs generated by `mcp.NewSessionID()`
with no secret material. Direct inbox writes use the same atomic rename pattern
as production. No new attack surface is introduced.

## Consequences

**Positive**

- Closes the coverage gap: proves channels infrastructure is loadable by Claude Code
- Validates `niwa_check_messages`, `niwa_ask`, `niwa_wait` return correct data from
  within a `claude -p` session
- Uses existing helpers (`runClaudeP`, `callMCPTool`, `meshState`) — minimal new code

**Negative**

- Requires `ANTHROPIC_API_KEY` and `claude` on PATH to run (mitigated by
  `claudeIsAvailable` guard and `@channels-e2e` tag)
- Test duration: ~30–60 s per scenario (LLM latency); separate tag keeps CI fast
- Non-deterministic LLM output: prompts are designed for binary assertions
  ("output X when you receive Y") to minimise flakiness
- Session_start hook creates a second coordinator entry in sessions.json each run;
  this is cosmetic and does not affect test correctness

**Mitigations**

- `@channels-e2e` scenarios run only when `NIWA_TEST_TAGS=channels-e2e` or
  `@channels-e2e` is explicitly included; they do not run with `@critical`
- Prompt wording anchors the assertion string so minor LLM rephrasing does not
  break the test
- `runClaudeP` already lowercases stdout, so assertions use lowercase strings
