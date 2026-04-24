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

## Testing the mesh

Mesh scenarios exercise the cross-session communication feature: task
delegation, worker spawn, the 11-tool MCP surface, restart / watchdog
behavior, and crash recovery. They live in `features/mesh.feature`.

The harness pairs a **literal-path spawn override** with a **scripted worker
fake** so every acceptance criterion runs deterministically in seconds, with
no live Claude involved. Two residual `@channels-e2e` scenarios cover what
the harness can't reach: MCP-config loadability by Claude Code and
bootstrap-prompt effectiveness.

For the user-facing description of the mesh, see
[cross-session-communication.md](cross-session-communication.md).

### The spawn-command override

`NIWA_WORKER_SPAWN_COMMAND` is a literal path that substitutes for the
resolved `claude` binary. The daemon composes argv, env, CWD, and
process-group behavior identically whether it launches real `claude` or the
override — so the scripted fake exercises the real spawn path.

```go
// In a step function:
os.Setenv("NIWA_WORKER_SPAWN_COMMAND", workerFakeBinary)
```

The override is env-only. `workspace.toml` parses with an explicit error if
you try to set it there (regression-tested), so a poisoned clone can't turn
it into arbitrary code execution at apply time.

### The scripted worker fake

The fake lives at `test/functional/worker_fake/` and compiles to a standalone
binary. On launch it:

1. Reads `NIWA_INSTANCE_ROOT`, `NIWA_SESSION_ROLE`, and `NIWA_TASK_ID` from
   env.
2. Starts `niwa mcp-serve` as a stdio subprocess (mimicking `claude -p`).
3. Executes the scenario named by `NIWA_FAKE_SCENARIO`.

Each scenario drives real MCP tools (`niwa_check_messages`,
`niwa_report_progress`, `niwa_finish_task`, ...) — it does not shortcut by
writing to the filesystem. Authorization-path calls retry on
`NOT_TASK_PARTY` for up to 2 seconds to cover the `worker.pid == 0` backfill
window after spawn.

Add a scenario by extending the `main.go` switch:

```go
switch os.Getenv("NIWA_FAKE_SCENARIO") {
case "completes-normally":
    reportProgress(mcp, taskID, "working")
    finishTask(mcp, taskID, "completed", map[string]any{"ok": true})
case "exits-without-finishing":
    // Worker exits without calling niwa_finish_task — daemon classifies
    // as unexpected exit, consumes a retry slot.
    return
...
}
```

Use the step helper to wire a scenario:

```gherkin
When the daemon spawns the worker fake with scenario "completes-normally"
Then the task "<task-id>" reaches state "completed" within 5 seconds
```

`runWithFakeWorker(scenario)` sets `NIWA_WORKER_SPAWN_COMMAND` to the
compiled fake path and `NIWA_FAKE_SCENARIO` to the selector.

### Timing overrides

The daemon reads integer-second env vars for every operational threshold, so
tests can drive minutes-long paths in seconds.

| Env var | Default | What it controls |
|---------|---------|------------------|
| `NIWA_RETRY_BACKOFF_SECONDS` | `30,60,90` | Comma-separated backoffs between restart attempts |
| `NIWA_STALL_WATCHDOG_SECONDS` | `900` | Stalled-progress threshold (15 min) |
| `NIWA_SIGTERM_GRACE_SECONDS` | `5` | Grace between SIGTERM and SIGKILL |
| `NIWA_DESTROY_GRACE_SECONDS` | `5` | `niwa destroy` daemon-shutdown grace |

Use the `setTimingOverrides` helper:

```go
setTimingOverrides(s, map[string]string{
    "NIWA_RETRY_BACKOFF_SECONDS":   "1,2,3",
    "NIWA_STALL_WATCHDOG_SECONDS":  "2",
    "NIWA_SIGTERM_GRACE_SECONDS":   "1",
})
```

With `NIWA_RETRY_BACKOFF_SECONDS=1,2,3`, the restart-cap scenario measures
three restarts at roughly 1s, 2s, 3s from `state.json` transition timestamps
— a full restart-cap + abandonment path completes in ~6 seconds instead of
~3 minutes.

### Daemon pause hooks

Two env-gated hooks let tests interrupt the daemon at the consumption-rename
boundary, making race-window acceptance criteria deterministic:

| Env var | Pause point |
|---------|-------------|
| `NIWA_TEST_PAUSE_BEFORE_CLAIM=1` | Daemon sees envelope; blocks before the atomic rename into `inbox/in-progress/`. |
| `NIWA_TEST_PAUSE_AFTER_CLAIM=1` | Daemon completes the rename but blocks before `exec.Command`. |

When active, the daemon creates a marker file under `.niwa/.test/` (either
`paused_before_claim` or `paused_after_claim`) and blocks until the marker is
removed.

Race-window example — `niwa_cancel_task` after the envelope is claimed:

```gherkin
Given NIWA_TEST_PAUSE_AFTER_CLAIM is set
And the coordinator delegates a task to "web"
When the daemon marker ".niwa/.test/paused_after_claim" appears within 1 second
Then the envelope is in "inbox/in-progress/"
When the coordinator calls niwa_cancel_task
Then the response is '{"status":"too_late"}'
When I remove the daemon marker ".niwa/.test/paused_after_claim"
Then the daemon proceeds with the spawn
```

Use the `pauseDaemonAt(hook)` helper — it sets the env var, waits for the
marker, and returns a release function that removes the marker when called:

```go
release := pauseDaemonAt(s, "after_claim")
// ... run assertions while the daemon is paused ...
release()
```

Symmetric structure covers the update and cancel-before-claim windows with
`NIWA_TEST_PAUSE_BEFORE_CLAIM`.

### `@critical` budget

All `@critical` mesh scenarios must complete under `make test-functional-critical`
in under 60 seconds total wall-clock, with each scenario under 10 seconds.
Timing overrides are the budget's primary enforcement mechanism.

### Daemon-log hygiene

A regression test greps `.niwa/daemon.log` after a full scenario run and
asserts no envelope bodies, result / reason payloads, or progress bodies
appear in the log. Only IDs, roles, types, and 200-char summaries are
permitted.

## Testing with headless Claude sessions

Scenarios tagged `@channels-e2e` drive actual `claude -p` invocations to
cover the two surfaces the scripted fake can't reach: MCP-config
loadability by Claude Code itself, and bootstrap-prompt effectiveness. They
require:

- `claude` on PATH
- `ANTHROPIC_API_KEY` set

Both are checked at the start of each scenario by the `claude is available`
step, which returns `godog.ErrPending` (skip) if either is absent. This
keeps `@critical` CI fast while allowing optional end-to-end validation.

Run with:

```
make test-functional NIWA_TEST_TAGS=@channels-e2e
```

Two scenarios ship in this tag:

- **MCP-config loadability.** `niwa create --channels`, then invoke real
  `claude -p` from the instance root with an anchored prompt; assert the
  session emits a numeric value returned by `niwa_check_messages`.
- **Bootstrap-prompt effectiveness.** Queue a task envelope and let the
  daemon spawn real `claude -p` (no `NIWA_WORKER_SPAWN_COMMAND`). Assert
  `state.json.state == "completed"` within 120 seconds. The assertion is on
  persisted state, not LLM text — tolerant of wording drift.

Prompts are anchored for deterministic matching and documented in
feature-file comments.
