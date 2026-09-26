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
  features/            # Gherkin .feature files — one file per area
  suite_test.go        # TestMain, godog entry point, Before hook, step registration
  steps_test.go        # step implementations
  localrepo_test.go    # localGitServer — offline bare-repo test helper
  gitfixture_test.go   # the only place the fixture shells out to git
```

### The sandbox

`TestMain` allocates one sandbox root per test process with
`os.MkdirTemp("", "niwa-func-")`, and the Before hook carves a fresh
`scenario-*` directory out of it for each scenario. Nothing is ever wiped and
nothing lives at a predictable path, so two suites running at once can't see —
or delete — each other's files.

Everything a scenario touches is a child of its own sandbox:

- `homeDir` — sandboxed `$HOME` (holds `.config/niwa/`, `.bashrc`, etc.)
- `tmpDir` — sandboxed `$TMPDIR`
- `workspaceRoot` — where `niwa init` is run from and where instances land
- `gitserver/` — the bare repos `localGitServer` serves over `file://`

The sandbox root lives under the system temp dir, never inside the checkout.
That keeps `CheckInitConflicts` quiet on a developer machine whose repo has a
niwa workspace ancestor, and — more to the point — it means a scenario can't
write into, or delete out of, the working tree it was built from. `TestMain`
asserts at startup that no ancestor of the sandbox root contains a `.git`, and
refuses to run if one does.

The binary runs with `HOME`, `XDG_CONFIG_HOME`, and `TMPDIR` all pointing
into the sandbox so nothing leaks between scenarios or into real state.

The sandbox root is removed when the process exits. Set
`NIWA_TEST_KEEP_SANDBOX=1` to keep it instead — the path is printed to stderr
at the end of the run, and the After hook prints the sandbox of each scenario
that failed.

The Makefile's `test-functional*` and `test-install` targets take a `flock` on
`.functional-test.lock` so a second run in the same checkout fails fast rather
than interleaving. (`flock` is util-linux; where it's absent the targets run
unguarded, which is safe on its own since each process gets its own sandbox.)

### Shelling out to git

Fixture code calls git through the helpers in `gitfixture_test.go` —
`fixtureGit`, `fixtureGitWorkTree`, `fixtureGitCommit`, `fixtureGitBare` — and
never through `exec.Command("git", ...)` directly.

The reason is that neither `git -C <dir>` nor `cmd.Dir` bounds anything. Both
only tell git where to *start* looking for a repository; if that directory has
been removed, or was never a repository, git keeps walking up until it finds
one. In a fixture that runs `git add -A`, `git commit`, and `git push`, the
repository it eventually finds is the checkout you're working in. That has
happened here: a suite committed a developer's working tree onto `main` and
pushed it.

The helpers refuse to run outside the process sandbox, set
`GIT_CEILING_DIRECTORIES` so discovery can't climb past it, and pin `GIT_DIR`
(plus `GIT_WORK_TREE`, where there is one) for commands that operate on an
ambient repository. They also point `GIT_CONFIG_GLOBAL` and `GIT_CONFIG_SYSTEM`
at `/dev/null` so whoever's running the suite can't change its behaviour from
their `~/.gitconfig`. A plain `exec.Command("git", ...)` in a new step gets
none of that and reopens the hole.

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

### Testing the GitHub tarball + ETag path

`localGitServer` covers any non-GitHub-specific behavior because it
serves real `file://` git repos. For tests that need to exercise the
GitHub-specific code paths (REST tarball endpoint, `If-None-Match`
ETag drift checks, `Accept: application/vnd.github.sha` HEAD
requests, 301 rename redirects), use `tarballFakeServer` instead.

`tarballFakeServer` wraps `httptest.NewServer` to mimic the GitHub
REST API endpoints `internal/github/fetch.go` consumes. Configure
responses per `(owner, repo, ref)` tuple:

```go
srv := newTarballFakeServer()
defer srv.Close()
srv.SetTarball("org", "repo", "HEAD", map[string]string{
    "wrap/":               "",
    "wrap/workspace.toml": `name = "demo"`,
})
srv.SetCommit("org", "repo", "HEAD", "1234567890abcdef...")
```

Wire it into the niwa binary by setting `NIWA_GITHUB_API_URL` to the
server's URL before the niwa subprocess starts. The fake records
every request so tests can assert "the second apply made zero
tarball requests" or "the If-None-Match header was sent." See
`test/functional/tarball_fake_server.go` and the integration test
in `tarball_fake_server_test.go` for the API.

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

## Testing the worktree commands

Worktree-lifecycle scenarios live in `features/worktree.feature` (create,
apply, destroy, list) and `features/session_attach.feature` (attach, detach,
the deprecation-alias notice). They drive the compiled binary against a real
git repo cloned into a sandbox instance, so they cover the actual `git worktree
add`, content install, and branch-deletion paths.

The attach scenarios that need a real Claude Code transcript gate on `claude`
being on PATH; without it they skip rather than fail, so `@critical` CI stays
fast and offline.

For the user-facing description of the command surface, see
[worktree.md](worktree.md).
