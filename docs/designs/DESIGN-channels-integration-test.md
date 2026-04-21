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

### Decision 1: How should the test wire NIWA_SESSION_ID for the MCP server?

The MCP server spawned by `claude -p` needs `NIWA_SESSION_ID` to locate its inbox and
serve tool calls. Without it, every call to `niwa_check_messages`, `niwa_ask`, or
`niwa_wait` returns "NIWA_SESSION_ID not set; is this session registered?".

The `.claude/.mcp.json` written by `InstallChannelInfrastructure` only bakes in
`NIWA_INSTANCE_ROOT` — the workspace-scoped root. Session identity is dynamic:
each `claude -p` invocation creates a new process with a new session UUID, so it
cannot be written into a static config file. The MCP server must receive it through
the process environment instead.

This means the test must arrange for `NIWA_SESSION_ID` to be set in `claude -p`'s
environment before the process starts, while also ensuring the inbox directory that
UUID corresponds to actually exists.

#### Chosen: Pre-register and set NIWA_SESSION_ID in env

Run `niwa session register --role coordinator` from the test subprocess before
launching `claude -p`. The register command creates the inbox directory and prints
`session_id=<uuid>` to stdout. The test captures that UUID, writes it into
`envOverrides["NIWA_SESSION_ID"]`, and sets `envOverrides["NIWA_SESSION_ROLE"]`.

When `claude -p` starts, the MCP server subprocess inherits the full env and
immediately has a valid session ID pointing at an existing inbox. The `session_start`
hook fires shortly after and re-registers the coordinator with Claude's PID and a
fresh UUID. The MCP server ignores this: it already bound to `NIWA_SESSION_ID` from
env and continues watching the pre-registration inbox. Pre-seeded messages and
goroutine-written replies target that same inbox via meshState, not sessions.json.

#### Alternatives Considered

**Rely on session_start hook only**: Let `claude -p` register the session via the
hook without pre-setting `NIWA_SESSION_ID`. The MCP server starts before the hook
fires, so any tool call that arrives before the hook completes returns "NIWA_SESSION_ID
not set". Even if the hook fires quickly, there is a race window. Rejected because
the MCP server must have `NIWA_SESSION_ID` from the moment it starts — the hook is
too late.

**Modify MCP server to discover session ID by parent PID**: Have the server look up
its own session entry in sessions.json by matching the parent process PID (Claude's
PID). No env var pre-setting needed and no race window. Rejected because this requires
a production code change that is out of scope for test infrastructure work. It may be
the right long-term design, but the pre-registration pattern achieves the same outcome
without changing production code.

### Decision 2: How should the test simulate the worker in the ask/answer scenario?

The `niwa_ask` scenario requires a worker that receives an ask message and writes a
reply. Running a full `claude -p` worker session alongside the coordinator introduces
two live LLM processes communicating through the mesh, which creates non-deterministic
ordering, unpredictable latency, and failure modes that are hard to attribute to
specific components. These properties conflict with the requirement for deterministic,
diagnosable tests.

The existing functional suite already solved a structurally identical problem in
`theCoordinatorAsksWorkerAndReplies`: a Go goroutine polls the worker inbox, detects
the ask message, and writes a hardcoded reply to the coordinator inbox. The coordinator
side runs as a direct JSON-RPC call, not `claude -p`. The new scenario needs the
coordinator to be `claude -p` instead, but the worker-simulation pattern is reusable
without modification.

#### Chosen: Test goroutine simulates worker

A Go goroutine runs concurrently with the coordinator `claude -p` subprocess. It polls
the worker inbox directory every 200 ms for a `question.ask` file. When one appears, it
writes a hardcoded `{"answer":"42"}` directly to the coordinator inbox as `question.answer`
via atomic rename. The coordinator's `niwa_ask` call is blocking and resolves when the
answer file appears.

Only the coordinator runs `claude -p`. The goroutine represents the worker at the
file-system protocol level without involving an LLM. This proves that the coordinator
can use `niwa_ask`, interpret the response, and produce observable output — which is
the actual coverage goal. Routing of the ask message from coordinator to worker still
goes through sessions.json (the `resolveRole` path), so the routing code is exercised
even though the worker itself is simulated.

#### Alternatives Considered

**Two concurrent claude -p sessions**: Start both coordinator and worker as headless
Claude processes. The worker calls `niwa_wait`, then replies to the coordinator's ask.
Rejected because the ordering of two LLM processes is non-deterministic: the worker
might not register before the coordinator sends the ask, reply latency is unbounded, and
a flaky LLM response on either side produces a test failure with no clear root cause.
Proving that two LLM agents can coordinate in real time is a separate concern that
belongs in manual integration testing.

### Decision 3: How should the test supply messages for check/wait scenarios?

The `niwa_check_messages` and `niwa_wait` scenarios require messages to be present in
the coordinator inbox when the coordinator calls those tools. A live round-trip — where
a background process sends messages while the coordinator is running — reintroduces the
same concurrency problems as two concurrent `claude -p` sessions: ordering is not
guaranteed, the sender might not have written all messages before the coordinator scans
the inbox, and diagnosis is harder.

`handleWait` uses `scanExistingForWaiter` which scans the inbox for matching messages
immediately after registering the type-waiter. If messages are already present, they are
returned on the first scan without waiting. This makes pre-seeded messages invisible to
any race condition.

#### Chosen: Pre-seed the coordinator inbox before claude -p starts

Write message files directly to the coordinator inbox directory before launching
`claude -p`. The existing `nMessagesPlacedInCoordinatorInbox` step handles this: it uses
the inbox path from `meshState.coordinatorInbox` (set during the coordinator session
setup step) and writes files via atomic rename, matching the production message delivery
pattern.

When the coordinator calls `niwa_check_messages` or `niwa_wait`, the messages are already
present and returned immediately. The test is deterministic regardless of LLM scheduling
or inbox-watch timing.

#### Alternatives Considered

**Live round-trip via a background sender process**: Run a background subprocess or
goroutine that sends messages to the coordinator inbox after `claude -p` starts. Rejected
because it reintroduces ordering uncertainty: if the sender is delayed, `niwa_wait` times
out before the messages arrive. Pre-seeding eliminates this risk entirely and does not
sacrifice coverage — message delivery through atomic rename is already tested by the
`@critical` MCP scenarios in the same suite.

## Decision Outcome

**Chosen: Decision 1B + Decision 2B + Decision 3B**

### Summary

All three scenarios use `niwa create --channels` on a plain workspace (no `[channels.mesh]`
config section needed) to provision the full channel infrastructure in an isolated sandbox.
Before launching `claude -p`, the test runs `niwa session register` to pre-register the
coordinator and capture its UUID, then sets `NIWA_SESSION_ID` and `NIWA_SESSION_ROLE` in
`envOverrides` so the MCP server inherits them. For the check and wait scenarios, the
coordinator inbox is pre-seeded with message files via atomic rename. For the ask/answer
scenario, a Go goroutine simulates the worker by polling the worker inbox and writing a
hardcoded reply. Only the coordinator runs `claude -p`. Each scenario is tagged
`@channels-e2e` and is automatically skipped when `claude` is not on PATH or
`ANTHROPIC_API_KEY` is unset.

### Rationale

The three decisions reinforce each other. Pre-registration (Decision 1B) gives the MCP
server a stable session identity with no race window, which is the foundation all three
scenarios depend on. Pre-seeding (Decision 3B) removes the need for any concurrent sender
process in the check and wait scenarios, making them fully deterministic. The goroutine
worker (Decision 2B) limits LLM involvement to exactly one process — the coordinator —
which is the component under test. Together, the combination produces fast, diagnosable
scenarios that prove channels infrastructure is loadable by Claude Code and that each MCP
tool returns correct data from within a live session, while keeping the test suite free
of timing dependencies and concurrent LLM coordination.

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
