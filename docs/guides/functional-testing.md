# Functional Testing

The functional test suite (`test/functional/`) runs the compiled `niwa`
binary end-to-end using [godog](https://github.com/cucumber/godog)
(Cucumber-style BDD). These tests catch integration regressions that unit
tests cannot: command wiring, config parsing across the full stack, and
behaviours that depend on git, the filesystem, and the process environment
acting together.

## When to add a functional test

Add a `@critical` scenario whenever you ship a user-facing CLI command
or fix a regression in the init → create → apply workflow. Unit tests
verify correctness of individual functions; functional tests verify
that the CLI behaves correctly when invoked as a black box.

Rule of thumb: if you had to manually run `niwa <command>` to verify
your change works, write a scenario so the next person doesn't have to.

## Running the tests

```
make test-functional          # full suite
make test-functional-critical # only @critical scenarios (faster)
```

Both targets build the binary first. Set `NIWA_TEST_BINARY` and
`NIWA_TEST_TAGS` to run the suite directly with `go test` if needed.

## Structure

```
test/functional/
  features/          # Gherkin .feature files — one file per area
  suite_test.go      # godog entry point, Before hook, step registration
  steps_test.go      # step implementations
  localrepo_test.go  # localGitServer — offline bare-repo test helper
```

### The sandbox

The Before hook creates a fresh sandbox for every scenario:

- `homeDir` — sandboxed `$HOME` (holds `.config/niwa/`, `.bashrc`, etc.)
- `tmpDir` — sandboxed `$TMPDIR`
- `workspaceRoot` — where `niwa init` is run from and where instances land;
  placed under `os.TempDir()` (not inside the repo) so `CheckInitConflicts`
  never fires on a developer machine that has a niwa workspace ancestor

The binary runs with `HOME`, `XDG_CONFIG_HOME`, and `TMPDIR` all pointing
into the sandbox so nothing leaks between scenarios or into real state.

## Testing commands that need a remote

Use `localGitServer` to create real bare git repos as fake remotes:

```go
// In a step function:
url, err := s.gitServer.ConfigRepo("myws", tomlContent)
// url is now file:///path/to/myws.git
```

Three helpers:

| Method | Creates | Use for |
|--------|---------|---------|
| `Repo(name)` | empty bare repo | source repos to clone |
| `ConfigRepo(name, toml)` | bare repo with `workspace.toml` | `niwa init --from` target |
| `OverlayRepo(name, toml)` | bare repo with `workspace-overlay.toml` | convention overlay discovery |

Store URLs in state via `s.repoURLs[name] = url`. Reference them in
workspace.toml bodies using the `{repo:<name>}` placeholder — the
`aConfigRepoExistsWithBody` step interpolates these before creating
the config repo.

### Convention overlay discovery

`DeriveOverlayURL` supports `file://` URLs, so the convention overlay
path (config repo → `<name>-overlay` repo) works in tests without any
special setup: create a `ConfigRepo("myws", ...)` and an
`OverlayRepo("myws-overlay", ...)`, then run `niwa init --from <myws URL>`.
`niwa init` will discover and clone the overlay automatically.

## Anatomy of a @critical scenario

```gherkin
@critical
Scenario: brief description of what regresses if this breaks
  Given a clean niwa environment
  And a local git server is set up
  And a source repo "myapp" exists
  And a config repo "myws" exists with body:
    """
    [workspace]
    name = "myws"

    [groups.tools]

    [repos.myapp]
    url = "{repo:myapp}"
    group = "tools"
    """
  When I run niwa init from config repo "myws"
  Then the exit code is 0
  When I run "niwa create myws"
  Then the exit code is 0
  And the instance "myws" exists
  And the repo "tools/myapp" exists in instance "myws"
```

Key points:
- `a local git server is set up` — no-op step; makes the scenario readable
- Source repos must be defined before the config repo that references them
  (URL interpolation only substitutes already-stored URLs)
- Groups used by explicit repos must be declared in `[groups.<name>]`
- `the instance "<name>" exists` checks `workspaceRoot/<name>/`
- `the repo "<group>/<repo>" exists in instance "<name>"` checks
  `workspaceRoot/<name>/<group>/<repo>/`

## Adding a new step

1. Implement the function in `steps_test.go`
2. Register it in `initializeScenario` in `suite_test.go`
3. Keep step functions short — delegate to helpers, not the other way around

## Testing the session mesh

Mesh scenarios test the cross-session communication feature: session registration,
message routing, daemon lifecycle, and the blocking MCP tools (`niwa_ask`,
`niwa_wait`). They live in `features/mesh.feature`.

### Setting up the sessions directory

Mesh tests that don't go through `niwa create` need an isolated instance root with a
pre-populated sessions directory. Use the built-in step:

```gherkin
Given a clean niwa environment
And NIWA_INSTANCE_ROOT is set to a temp directory
```

This creates a fresh directory under the scenario's `tmpDir`, sets `NIWA_INSTANCE_ROOT`
in `envOverrides`, and initialises `meshState` in the scenario context. All subsequent
mesh steps read the instance root from there.

### Registering sessions

`niwa session register` is the entry point for sessions joining the mesh. The step
runs the binary with `NIWA_SESSION_ROLE` set, captures the printed `session_id=<uuid>`
line, and stores the UUID in `meshState.sessionIDs[role]` for use in later assertions.

```gherkin
When I run "niwa session register" as role "coordinator"
Then the exit code is 0
And a sessions.json entry exists for role "coordinator"
And the coordinator inbox directory exists
```

### Faking Claude session ID discovery

`niwa session register` discovers the Claude session ID by reading
`~/.claude/sessions/<pid>.json`. In tests, the sandboxed `$HOME` is an isolated
directory (`s.homeDir`), so you can write a fixture file there without touching the
real user state:

```gherkin
And a Claude session file exists for the parent process with session ID "test-claude-session-abc1" and matching cwd
When I run "niwa session register" as role "coordinator"
Then the sessions.json entry for role "coordinator" has claude_session_id "test-claude-session-abc1"
```

The step writes a JSON file at `<homeDir>/.claude/sessions/<test-pid>.json` with the
given `sessionId` and either the real cwd (matching) or a dummy path (mismatched).
The `niwa session register` subprocess reads its grandparent PID (the test process)
and finds the file.

To assert that discovery failed gracefully:

```gherkin
And the sessions.json entry for role "coordinator" has no claude_session_id
And the error output contains "could not discover Claude session ID"
```

### Calling MCP tools

The `callMCPTool` helper in `steps_test.go` starts `niwa mcp-serve` as a subprocess,
runs an MCP initialize + tools/call exchange over stdin/stdout, and returns the JSON
response. Per-scenario step functions wrap it with role-specific env vars:

```go
// In a step function:
out, err := callMCPTool(s, sessionID, sessionRole, "niwa_check_messages", "{}")
```

The function sets `NIWA_INSTANCE_ROOT`, `NIWA_SESSION_ID`, `NIWA_SESSION_ROLE`, and
`NIWA_INBOX_DIR` so the server knows which session it's serving.

### Testing niwa_ask (blocking round-trip)

The `theCoordinatorAsksWorkerAndReplies` step exercises the full blocking path:

1. Starts `niwa mcp-serve` for the coordinator in a subprocess
2. Sends a `niwa_ask(to="worker", ...)` call
3. In a goroutine, polls the worker inbox for the `question.ask` file
4. Writes a `question.answer` reply to the coordinator inbox
5. Asserts the `mcp-serve` output contains the answer body

```gherkin
When the coordinator asks the worker a question and the worker replies
Then the ask response contains the answer
```

For the timeout path, use:

```gherkin
When the coordinator calls niwa_ask with timeout 2 seconds and no reply
Then the output contains "ASK_TIMEOUT"
```

### Testing niwa_wait (message accumulation)

The `nMessagesPlacedInCoordinatorInbox` step writes N pre-formed message files
directly to the inbox directory via atomic rename (bypassing MCP). The subsequent
`niwa_wait` call reads them from the already-populated inbox:

```gherkin
When 2 "task.result" messages are placed in the coordinator inbox
When the coordinator calls niwa_wait for "task.result" messages with count 2
Then the output contains "task.result"
And the output contains "2 message"
```

### Testing daemon lifecycle

Daemon scenarios go through `niwa create` (which provisions infrastructure and spawns
the daemon) and then check `daemon.pid`:

```gherkin
And the file ".niwa/daemon.pid" exists in instance "daemon-ws"
```

To assert the daemon isn't respawned on a second apply:

```gherkin
When I remember the daemon PID for instance "daemon2-ws"
When I run "niwa apply daemon2-ws"
And the daemon PID for instance "daemon2-ws" has not changed
```

To test self-exit on instance removal:

```gherkin
When I remove the sessions directory from instance "selfstop-ws"
Then the daemon for instance "selfstop-ws" eventually stops
```

The assertion polls `daemon.pid` for up to 10 seconds, checking whether the daemon's
PID is still alive.

### Step reference (mesh)

| Step | Function | Notes |
|------|----------|-------|
| `NIWA_INSTANCE_ROOT is set to a temp directory` | `niwaInstanceRootIsSetToATempDirectory` | Creates mesh-instance dir, sets env, initialises meshState |
| `I run "niwa session register" as role "<role>"` | `iRunNiwaSessionRegisterAsRole` | Captures session UUID from output |
| `a sessions.json entry exists for role "<role>"` | `aSessionsJSONEntryExistsForRole` | Checks for `"role":"<role>"` in sessions.json |
| `the coordinator inbox directory exists` | (inline) | Delegates to `theInboxDirectoryExistsForRole("coordinator")` |
| `a Claude session file exists for the parent process with session ID "<id>" and matching cwd` | `aClaudeSessionFileExistsForParentProcessWithMatchingCwd` | Writes `<homeDir>/.claude/sessions/<pid>.json` |
| `a Claude session file exists for the parent process with session ID "<id>" and mismatched cwd` | `aClaudeSessionFileExistsForParentProcessWithMismatchedCwd` | Same, but with dummy cwd |
| `the sessions.json entry for role "<role>" has claude_session_id "<id>"` | `theSessionsJSONEntryForRoleHasClaudeSessionID` | Checks sessions.json content |
| `the sessions.json entry for role "<role>" has no claude_session_id` | `theSessionsJSONEntryForRoleHasNoClaudeSessionID` | Asserts absence of `claude_session_id` key |
| `the worker session sends a "<type>" message to "<role>" with body "<body>"` | `theWorkerSessionSendsAMessageToWithBody` | Calls `niwa_send_message` via `callMCPTool` |
| `the coordinator inbox contains <n> message` | `theCoordinatorInboxContainsNMessages` | Counts `.json` files in inbox, excluding subdirs |
| `the coordinator session checks messages` | `theCoordinatorSessionChecksMessages` | Calls `niwa_check_messages` via `callMCPTool` |
| `the coordinator asks the worker a question and the worker replies` | `theCoordinatorAsksWorkerAndReplies` | Full blocking round-trip |
| `the ask response contains the answer` | `theAskResponseContainsAnswer` | Checks stored MCP output |
| `the coordinator calls niwa_ask with timeout <n> seconds and no reply` | `theCoordinatorCallsAskWithTimeout` | Expects ASK_TIMEOUT |
| `<n> "<type>" messages are placed in the coordinator inbox` | `nMessagesPlacedInCoordinatorInbox` | Writes files directly to inbox, atomic rename |
| `the coordinator calls niwa_wait for "<type>" messages with count <n>` | `theCoordinatorCallsWait` | Calls `niwa_wait` via `callMCPTool` |
| `the coordinator sends a message with invalid type "<type>"` | `coordinatorSendsWithInvalidType` | Expects `isError` in response |
| `I remember the daemon PID for instance "<name>"` | `iRememberDaemonPIDForInstance` | Stores first line of daemon.pid in context |
| `the daemon PID for instance "<name>" has not changed` | `theDaemonPIDForInstanceHasNotChanged` | Compares with stored PID |
| `I remove the sessions directory from instance "<name>"` | `iRemoveSessionsDirFromInstance` | `os.RemoveAll` on `.niwa/sessions` |
| `the daemon for instance "<name>" eventually stops` | `theDaemonForInstanceEventuallyStops` | Polls for up to 10 seconds |
