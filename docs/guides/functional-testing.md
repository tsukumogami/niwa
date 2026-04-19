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
