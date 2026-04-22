# Handoff: @channels-e2e — production gap identified, rework needed

## Branch / PR

- Branch: `docs/cross-session-communication`
- PR: #71 — "feat(channels): add cross-session communication via filesystem session mesh"

---

## The core problem

The `@channels-e2e` scenarios were written with a pre-registration workaround that
does NOT reflect the intended user experience. The user identified this as a
production gap that needs to be fixed before the tests can be written correctly.

**Expected user flow (nothing more):**
1. `niwa create --channels` — provisions `.claude/.mcp.json`, hooks, sessions dir
2. `cd <instance-root> && claude` — Claude starts, loads MCP server via `.mcp.json`
3. `session_start` hook fires — `niwa session register` runs automatically, registers
   coordinator, creates inbox
4. MCP tools (`niwa_check_messages`, `niwa_ask`, `niwa_wait`) just work

**What's actually broken:** The MCP server starts (step 2) before the hook fires
(step 3). By the time `NIWA_SESSION_ID` exists, the server is already running and
has no way to learn about it. Result: every MCP tool call returns
`"NIWA_SESSION_ID not set; is this session registered?"`.

**The workaround in the current tests** (`iSetUpCoordinatorSessionForInstance`)
manually runs `niwa session register` before `claude -p`, captures the UUID, and
injects `NIWA_SESSION_ID` into the process environment. This masks the broken
production flow rather than fixing it.

---

## What needs to be implemented

### Step 1 — Confirm PID is stored in session entries

Check whether `niwa session register` writes the registering process's PID into
`sessions.json`:

```bash
grep -n "Pid\|pid\|PID" internal/mcp/types.go internal/cli/session_register.go
```

`SessionEntry` in `internal/mcp/types.go` needs a `pid int` field. If absent,
add it and write `os.Getpid()` during registration in `session_register.go`.

The self-discovery in step 2 depends on this field.

### Step 2 — MCP server self-discovers session via parent PID

In `internal/mcp/server.go`, at startup, if `NIWA_SESSION_ID` is not set:

1. Get `parentPID = os.Getppid()` (this is Claude's PID)
2. Poll `<instanceRoot>/.niwa/sessions/sessions.json` every 200 ms for up to 10 s
3. When an entry appears with `pid == parentPID`, use that entry's `session_id`
4. If no entry appears within 10 s, log a warning and continue (tools will return
   a helpful error rather than panicking)

This is "Option C" from `docs/designs/DESIGN-channels-integration-test.md` —
which was incorrectly rejected as "out of scope". It is the correct production
behavior and belongs in the implementation.

Relevant files:
- `internal/mcp/server.go` — server startup, add self-discovery call
- `internal/mcp/types.go` — add `Pid int` to `SessionEntry` if missing
- `internal/cli/session_register.go` — write `os.Getpid()` to entry if missing
- `internal/mcp/server_test.go` — add unit test for self-discovery path

### Step 3 — Rework the @channels-e2e scenarios

Once self-discovery works, `iSetUpCoordinatorSessionForInstance` is no longer
needed as a pre-launch step. The scenarios should look like:

```gherkin
@channels-e2e
Scenario: headless coordinator reads messages via niwa_check_messages
  Given a clean niwa environment
  And claude is available
  And a local git server is set up
  And a config repo "headless-check-ws" exists with body:
    """
    [workspace]
    name = "headless-check-ws"
    """
  When I run niwa init from config repo "headless-check-ws"
  Then the exit code is 0
  When I run "niwa create --channels headless-check-ws"
  Then the exit code is 0
  And 1 "task.result" messages will be placed in the coordinator inbox for instance "headless-check-ws"
  When I run claude -p from instance root "headless-check-ws" with prompt:
    """
    Use the niwa_check_messages tool to check your inbox. Find the message type
    of the first message and output exactly: FOUND:<type>
    """
  Then the exit code is 0
  And the output contains "found:task.result"
```

**The pre-seeding problem:** With no pre-registration, the coordinator inbox
doesn't exist until the `session_start` hook fires inside `claude -p`. The test
can't write messages to it in advance. Solution: the "will be placed" step starts
a goroutine that watches `sessions.json` for a coordinator entry to appear,
then writes the messages to its inbox. The coordinator's MCP call finds them on
the scan (same as pre-seeded — `scanExistingForWaiter` works identically).

New step to add in `steps_test.go`:

```go
// Starts a goroutine that watches sessions.json for a coordinator entry,
// then writes n messages of msgType to its inbox via atomic rename.
// The goroutine runs concurrently with the subsequent claude -p invocation.
func nMessagesWillBePlacedInCoordinatorInboxForInstance(
    ctx context.Context, n int, msgType, instanceName string,
) (context.Context, error)
```

**For the ask scenario:** The worker still needs explicit pre-registration because
`niwa_ask` routes via sessions.json — the worker entry must exist before the
coordinator sends the ask. `iSetUpWorkerSessionForInstance` stays. The ask
scenario becomes:

```gherkin
When I run "niwa create --channels headless-ask-ws"
Then the exit code is 0
When I set up worker session for instance "headless-ask-ws"
When I run claude -p from instance root "headless-ask-ws" with simulated worker reply and prompt: ...
```

(No coordinator pre-registration — the MCP server self-discovers it.)

### Step 4 — Update the design doc

`docs/designs/DESIGN-channels-integration-test.md` Decision 1 must be rewritten:

- **Chosen** → "Self-discovery via parent PID" (was: Option C, rejected)
- **Rejected** → "Pre-register and set NIWA_SESSION_ID in env" (was: Option B, chosen)
- Rejection rationale for pre-registration: "requires test setup that real users
  never do; masks a broken production flow rather than fixing it"
- Rejection rationale for "rely on session_start hook only": still rejected —
  the server needs the ID before the first tool call, not just eventually

Also update the frontmatter `decision:` and `rationale:` fields and the
`## Decision Outcome` section.

### Step 5 — Update the guide

`docs/guides/functional-testing.md` section "Setting up sessions for headless
coordinator tests" currently describes the pre-registration pattern. Update it
to describe the self-discovery flow: no setup step needed for the coordinator;
the worker still uses `iSetUpWorkerSessionForInstance` when `niwa_ask` is tested.

---

## Files to change (summary)

| File | Change |
|------|--------|
| `internal/mcp/types.go` | Add `Pid int` to `SessionEntry` if missing |
| `internal/cli/session_register.go` | Write `os.Getpid()` to entry if missing |
| `internal/mcp/server.go` | Self-discover session ID via parent PID when NIWA_SESSION_ID absent |
| `internal/mcp/server_test.go` | Unit test for self-discovery path |
| `docs/designs/DESIGN-channels-integration-test.md` | Invert Decision 1 chosen/rejected |
| `test/functional/steps_test.go` | Add goroutine-watcher step; remove/repurpose pre-registration |
| `test/functional/suite_test.go` | Update step registrations |
| `test/functional/features/mesh.feature` | Rework @channels-e2e scenarios (lines 408–475) |
| `docs/guides/functional-testing.md` | Update headless session setup section |

---

## Current state of @channels-e2e scenarios (to replace)

The three scenarios currently at lines 408–475 of `mesh.feature` use
`iSetUpCoordinatorSessionForInstance` — that step pre-registers the coordinator
and sets `NIWA_SESSION_ID` in env before `claude -p` starts. These scenarios
need to be rewritten once self-discovery is implemented.

The step functions added in the last session:
- `iSetUpCoordinatorSessionForInstance` — will be removed/repurposed
- `iSetUpWorkerSessionForInstance` — stays (worker must be pre-registered for routing)
- `iRunClaudePFromInstanceRootWithSimulatedWorkerReply` — stays, but the goroutine
  inside it should also be updated to not assume a pre-known coordinator inbox path

---

## Resuming prompt

> We are on branch `docs/cross-session-communication` (PR #71). Read
> `wip/handoff-channels-e2e.md` — it has the full context.
>
> Short version: the `@channels-e2e` scenarios were written with a
> pre-registration workaround. The user wants the tests to reflect the real
> production UX: `niwa create --channels`, then `claude` from the instance root,
> with no manual session setup. The production gap is that the MCP server doesn't
> know its `NIWA_SESSION_ID` because it starts before the `session_start` hook
> fires.
>
> The fix is to implement MCP server self-discovery: when `NIWA_SESSION_ID` is
> absent, poll `sessions.json` for an entry with `pid == os.Getppid()` (Claude's
> PID). Start by checking whether `SessionEntry` in `internal/mcp/types.go`
> already has a `Pid` field — that's the prerequisite. Then implement
> self-discovery in `internal/mcp/server.go`, add a unit test, rework the
> `@channels-e2e` scenarios to remove pre-registration (replacing pre-seeding
> with a goroutine watcher), update the design doc's Decision 1, and run
> `make test-functional NIWA_TEST_TAGS=channels-e2e` to validate.
