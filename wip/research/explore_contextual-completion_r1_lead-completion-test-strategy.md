# Lead: What testing strategy exists for shell completion in cobra projects?

## Findings

### The existing niwa functional harness

The godog-based harness in `test/functional/` is well-suited to hosting completion
tests. Key facts gathered from reading the source:

- `test/functional/suite_test.go` builds a per-scenario sandbox rooted at
  `<binDir>/.niwa-test/` and resets it in a `ctx.Before` hook. Each scenario
  gets a fresh `homeDir`, `workspaceRoot`, and `tmpDir`. The sandbox is wiped
  with `os.RemoveAll(sandbox)` before every scenario (suite_test.go:85-113).
- `buildEnv()` in `test/functional/steps_test.go:74-94` strips `HOME`,
  `XDG_CONFIG_HOME`, and `TMPDIR` from the parent environment and re-injects
  sandboxed values. `XDG_CONFIG_HOME` is set to `<homeDir>/.config`, which is
  exactly where `config.GlobalConfigPath()` (internal/config/registry.go:68-78)
  looks for `niwa/config.toml`. This means any scenario that writes to
  `<homeDir>/.config/niwa/config.toml` is feeding the same registry that
  `config.LoadGlobalConfig()` reads at runtime — no special hooks required for
  completion tests to see registry data.
- `runNiwa` (steps_test.go:99-123) already captures stdout, stderr, and exit
  code into per-scenario state and is command-agnostic — it will happily run
  `niwa __complete <args>` as-is because cobra registers `__complete` on every
  root command by default.
- `aWorkspaceExists` (steps_test.go:21-36) creates a workspace directory with
  `.niwa/workspace.toml` but deliberately does **not** write a registry entry.
  Completion tests will need a sibling step `aRegisteredWorkspaceExists` that
  both creates the on-disk workspace and adds it to the global registry.

### How cobra projects test dynamic completion

Cobra's own `completions_test.go` (verified via WebFetch of the upstream repo)
uses two complementary approaches:

1. **Direct function invocation.** Completion functions have the signature
   `func(cmd *cobra.Command, args []string, toComplete string) ([]string, ShellCompDirective)`.
   Tests call them with a constructed `*cobra.Command`, an `args` slice, and a
   `toComplete` prefix, then assert on the returned `[]string` and directive.
   This is fast, does not shell out, and isolates completion logic from argv
   parsing.

2. **`__complete` subcommand invocation.** Cobra exposes a hidden root
   subcommand named `__complete` (constant `ShellCompNoDescRequestCmd`) that
   runs the completion pipeline end-to-end. Its stdout format is:

   ```
   suggestion1
   suggestion2
   :4
   Completion ended with directive: ShellCompDirectiveNoFileComp
   ```

   One suggestion per line, then a line starting with `:` containing the
   integer-encoded `ShellCompDirective`, then a human-readable trailer. Cobra's
   own tests use a helper `executeCommand(rootCmd, ShellCompNoDescRequestCmd, ...)`
   and string-compare the whole blob including the directive line.

Shell-level integration tests (spawning bash, loading the generated completion
script, and sending TAB) are rare in Go CLI projects. Cobra itself does not
do this for dynamic completion; it relies on the two levels above. gh, hugo,
and kubectl follow the same pattern in the files I could locate — completion
logic is unit-tested directly or via `__complete`, with shell-script wrappers
covered by snapshot tests of the generated output rather than live bash
interaction. This matches the niwa harness's existing philosophy: exercise
the binary as a black box, use a sourced bash script only where the behavior
genuinely requires a live shell (the pwd-sentinel trick in
`runWrappedShellWithPATH`, steps_test.go:220-267).

### The directive trailer is a test-writing hazard

`__complete` always appends at least one line after the suggestions: the
`:<directive-int>` line, and optionally a `Completion ended with directive: ...`
line. A naive test that does `strings.Contains(stdout, "alpha")` works, but
`theOutputEquals` (steps_test.go:305-312) will never match a bare suggestion
list — any exact-match assertion must include the trailer. The cleanest fix
is a completion-specific assertion step that splits on `\n`, drops lines
starting with `:` and the `Completion ended` trailer, and compares the
remaining set.

## Implications

### Two-tier testing strategy

**Tier 1 — Unit tests in `internal/cli/completion_test.go`.** Call the
completion functions directly. These tests cover edge cases cheaply:

```go
func TestCompleteWorkspaceNames(t *testing.T) {
    t.Setenv("XDG_CONFIG_HOME", t.TempDir())
    writeGlobalConfig(t, map[string]config.RegistryEntry{
        "alpha": {Root: "/tmp/alpha"},
        "beta":  {Root: "/tmp/beta"},
    })

    cmd := &cobra.Command{}
    got, directive := completeWorkspaceNames(cmd, nil, "a")

    if !slices.Equal(got, []string{"alpha"}) {
        t.Errorf("want [alpha], got %v", got)
    }
    if directive != cobra.ShellCompDirectiveNoFileComp {
        t.Errorf("want NoFileComp, got %v", directive)
    }
}
```

Pros: runs under `go test ./...`, no binary required, directly exercises the
prefix-filter and registry-read logic. Covers malformed config, empty
registry, and toComplete prefix edge cases.

Cons: doesn't prove the completion is actually wired into the `go` command's
`ValidArgsFunction`. That's what Tier 2 catches.

**Tier 2 — Functional tests via `niwa __complete`.** Reuse the existing
harness. A new feature file `test/functional/features/completion.feature` and
a handful of new steps are all that's needed. The existing `iRun` step
already works:

```gherkin
Scenario: go completes workspace names from the registry
  Given a registered workspace "alpha" exists
  And a registered workspace "beta" exists
  When I run "niwa __complete go a"
  Then the exit code is 0
  And the completion output contains "alpha"
  And the completion output does not contain "beta"
```

Pros: catches wiring bugs (completion function attached to wrong flag,
directive lost in middleware, init ordering). Uses the real registry on
disk, so it also proves `XDG_CONFIG_HOME` threading works for the completion
path.

Cons: slower (spawns the binary), less focused (a failure could be
anywhere).

### New step implementations required

Add the following to `test/functional/steps_test.go`:

```go
// aRegisteredWorkspaceExists creates the workspace on disk AND writes an
// entry into the scenario's global config. This is what lets completion
// tests exercise the registry lookup path.
func aRegisteredWorkspaceExists(ctx context.Context, name string) (context.Context, error) {
    s := getState(ctx)
    if s == nil {
        return ctx, fmt.Errorf("no test state")
    }
    if _, err := aWorkspaceExists(ctx, name); err != nil {
        return ctx, err
    }
    cfgPath := filepath.Join(s.homeDir, ".config", "niwa", "config.toml")
    if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
        return ctx, err
    }
    wsRoot := filepath.Join(s.workspaceRoot, name)
    entry := fmt.Sprintf(
        "\n[registry.%s]\nsource = %q\nroot = %q\n",
        name,
        filepath.Join(wsRoot, ".niwa", "workspace.toml"),
        wsRoot,
    )
    f, err := os.OpenFile(cfgPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
    if err != nil {
        return ctx, err
    }
    defer f.Close()
    if _, err := f.WriteString(entry); err != nil {
        return ctx, err
    }
    return ctx, nil
}

// theCompletionOutputContains asserts that one of the suggestion lines
// (not the directive trailer) equals or prefix-matches `text`.
func theCompletionOutputContains(ctx context.Context, text string) error {
    s := getState(ctx)
    for _, line := range completionSuggestions(s.stdout) {
        if line == text {
            return nil
        }
    }
    return fmt.Errorf("expected completion suggestion %q, got:\n%s", text, s.stdout)
}

func theCompletionOutputDoesNotContain(ctx context.Context, text string) error {
    s := getState(ctx)
    for _, line := range completionSuggestions(s.stdout) {
        if line == text {
            return fmt.Errorf("expected completion not to include %q, got:\n%s", text, s.stdout)
        }
    }
    return nil
}

// completionSuggestions parses `niwa __complete` stdout, dropping the
// `:<directive>` line and the `Completion ended with directive:` trailer.
func completionSuggestions(stdout string) []string {
    var out []string
    for _, line := range strings.Split(stdout, "\n") {
        if line == "" {
            continue
        }
        if strings.HasPrefix(line, ":") {
            continue
        }
        if strings.HasPrefix(line, "Completion ended with directive:") {
            continue
        }
        // Descriptions (when ShellCompRequestCmd is used) are tab-separated.
        if idx := strings.IndexByte(line, '\t'); idx >= 0 {
            line = line[:idx]
        }
        out = append(out, line)
    }
    return out
}
```

Then register the new steps in `initializeScenario` (suite_test.go:85):

```go
ctx.Step(`^a registered workspace "([^"]*)" exists$`, aRegisteredWorkspaceExists)
ctx.Step(`^the completion output contains "([^"]*)"$`, theCompletionOutputContains)
ctx.Step(`^the completion output does not contain "([^"]*)"$`, theCompletionOutputDoesNotContain)
```

### Sample feature file

`test/functional/features/completion.feature`:

```gherkin
Feature: Shell completion for workspace, instance, and repo names
  Completion resolves identifiers from runtime state (global registry,
  current instance). The CLI exposes completion via cobra's hidden
  `__complete` subcommand; shell wrappers (bash/zsh/fish) source a
  generated script that calls it under the hood.

  Background:
    Given a clean niwa environment

  Scenario: go completes workspace names from the registry
    Given a registered workspace "alpha" exists
    And a registered workspace "beta" exists
    When I run "niwa __complete go a"
    Then the exit code is 0
    And the completion output contains "alpha"
    And the completion output does not contain "beta"

  Scenario: go with empty prefix returns all workspaces
    Given a registered workspace "alpha" exists
    And a registered workspace "beta" exists
    When I run "niwa __complete go ''"
    Then the exit code is 0
    And the completion output contains "alpha"
    And the completion output contains "beta"

  Scenario: go completes with no suggestions when registry is empty
    When I run "niwa __complete go a"
    Then the exit code is 0
    And the completion output does not contain "alpha"

  Scenario: -w flag completes workspace names
    Given a registered workspace "alpha" exists
    When I run "niwa __complete go -w a"
    Then the exit code is 0
    And the completion output contains "alpha"

  Scenario: repo completion reads current instance
    Given a registered workspace "alpha" exists
    And a workspace instance "alpha/1" with repos "api,web" exists
    When I run "niwa __complete go -r a" from workspace instance "alpha/1"
    Then the exit code is 0
    And the completion output contains "api"
    And the completion output does not contain "web"
```

The last scenario hints at an additional step pair — creating instance
directories with repo subdirs, and running `niwa` from inside one — that
builds on the existing `iRunFromWorkspace` pattern in steps_test.go:133-140.

### What this does NOT cover (deliberately)

- **Live bash completion.** Actually sending a TAB and observing which
  candidates readline surfaces is out of scope; cobra itself does not
  test this, and the return on investment is poor because the generated
  completion script is cobra's responsibility, not niwa's. The
  `__complete` tests cover the logic; the shell-init wrapper (tested
  separately in shell-navigation.feature) covers the eval plumbing.
- **Latency.** A perceptible-latency requirement calls for a benchmark
  in `internal/cli/completion_test.go` using `testing.B`, not a godog
  scenario — godog has no time-bounded assertion primitive, and wall-clock
  assertions in functional suites flake under CI load.

## Surprises

- The existing `buildEnv` already routes `XDG_CONFIG_HOME` correctly, so
  registry-backed completion needs no new sandbox plumbing. I expected to
  have to add a NIWA_CONFIG_HOME-style override; not required.
- Cobra's `__complete` output includes both a `:<int>` line and, with
  descriptions enabled, a `Completion ended with directive: ...` trailer.
  Tests that use `theOutputEquals` (steps_test.go:305) will always fail
  against raw `__complete` output. This is a real pitfall that ships
  with the framework, not niwa-specific.
- There is no public helper in cobra for parsing `__complete` output —
  every project reimplements the split-and-filter. Worth factoring
  `completionSuggestions` into a small internal package if niwa grows
  more than a couple of completion test helpers.

## Open Questions

1. **Descriptions on or off?** `__complete` emits
   `name<TAB>description` lines when descriptions are requested. Does niwa
   want to support descriptions (e.g., workspace path as description)?
   This changes both the completion function implementation and the
   suggestion-parser in tests. Default cobra behavior varies by shell.
2. **Where does completion test setup for instances/repos belong?**
   The existing `aWorkspaceExists` step is a minimal scaffold. Completion
   scenarios covering repo names need instance directories with repo
   subdirs. Extending the step library or adding a fixture loader
   (`Given the "alpha-with-repos" fixture is loaded`) is a judgment call
   that depends on how many scenarios we expect.
3. **Should we snapshot the generated completion script?** A gofmt-style
   golden-file test of `niwa completion bash` output would catch
   accidental changes to the wrapper that cobra generates. Low value
   unless niwa customizes the template.
4. **How do we assert "completion runs fast enough"?** A `testing.B`
   benchmark gates regressions in CI, but a perceptible-latency budget
   (e.g., <50ms) needs a concrete number — someone has to pick it.

## Summary

Cobra's hidden `__complete` subcommand gives niwa a black-box completion
entry point that slots into the existing functional harness with only a
new feature file, a `aRegisteredWorkspaceExists` step that writes to the
already-sandboxed `XDG_CONFIG_HOME`, and a small parser that strips the
`:<directive>` trailer from stdout — paired with a unit-test tier in
`internal/cli/completion_test.go` that calls completion functions
directly. The main implication is that no new sandboxing or shell
scripting is required; completion can ride on the same primitives that
shell-navigation already validated. The biggest open question is
whether niwa wants descriptions in its completion output, because that
decision changes the parser, the completion function signature usage,
and the expected stdout format in every scenario.
