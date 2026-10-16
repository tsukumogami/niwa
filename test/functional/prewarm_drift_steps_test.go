package functional

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
)

// prewarmFakeClaudeScript builds a fake `claude` that stands in for the plugin
// pre-warm shell-out during provisioning. It mimics the real binary's
// scope-dependent settings write so the #179 regression is observable end to end:
//
//   - `plugin marketplace add <repo>` succeeds (the on-disk clone the race fix needs).
//   - `plugin marketplace add <dir>` (an absolute path) also registers the directory
//     in $HOME/.claude/plugins/known_marketplaces.json under the name its
//     .claude-plugin/marketplace.json declares, as the real binary does. The name is
//     read from a manifest that starts with {"name":"...", which is how scenarios
//     stage it.
//   - `plugin install <plugin> --scope project` REserializes the project
//     settings.json -- the niwa-managed, fingerprinted file -- which is exactly what
//     caused the false "modified outside niwa" drift. Here it appends a newline, which
//     changes the file's content hash while leaving it valid JSON.
//   - `plugin install <plugin> --scope local` writes settings.local.json instead and
//     never touches settings.json, so the managed file's hash is preserved.
//
// Every install records the scope it was invoked with to $HOME/prewarm-install-scope
// so a scenario can assert niwa issued `--scope local` (the fix) and not
// `--scope project` (the regression). Every `plugin` invocation is also appended,
// one line each, to $HOME/prewarm-calls, so a scenario can assert the order of the
// calls. Any other invocation exits 0 so an unrelated claude call during
// provisioning never fails the run.
func prewarmFakeClaudeScript() string {
	return `#!/bin/sh
if [ "$1" = "plugin" ]; then
  shift
  printf '%s\n' "$*" >> "$HOME/prewarm-calls"
  case "$1" in
    marketplace)
      case "$3" in
        /*)
          name=$(sed -n 's/^{"name":"\([^"]*\)".*/\1/p' "$3/.claude-plugin/marketplace.json")
          mkdir -p "$HOME/.claude/plugins"
          printf '{"%s":{"source":{"source":"directory","path":"%s"},"installLocation":"%s"}}\n' "$name" "$3" "$3" > "$HOME/.claude/plugins/known_marketplaces.json"
          ;;
      esac
      exit 0
      ;;
    install)
      scope=""
      while [ "$#" -gt 0 ]; do
        if [ "$1" = "--scope" ]; then
          scope="$2"
        fi
        shift
      done
      printf '%s' "$scope" > "$HOME/prewarm-install-scope"
      if [ "$scope" = "project" ]; then
        # Mimic the real binary re-serializing the managed settings.json: change
        # its bytes (and thus its content hash) while keeping valid JSON.
        printf '\n' >> ".claude/settings.json"
      elif [ "$scope" = "local" ]; then
        printf '{"enabledPlugins":{}}\n' > ".claude/settings.local.json"
      fi
      exit 0
      ;;
  esac
fi
exit 0
`
}

// aFakeClaudeForPrewarm installs the pre-warm fake claude on PATH for every niwa
// subprocess in the scenario.
func aFakeClaudeForPrewarm(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	binDir := filepath.Join(s.homeDir, "fake-claude-bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return ctx, fmt.Errorf("mkdir fake-claude-bin: %w", err)
	}
	scriptPath := filepath.Join(binDir, "claude")
	if err := os.WriteFile(scriptPath, []byte(prewarmFakeClaudeScript()), 0o755); err != nil {
		return ctx, fmt.Errorf("writing fake claude script: %w", err)
	}
	s.pathPrefix = binDir
	return ctx, nil
}

// theRecordedPrewarmInstallScopeIs asserts the scope niwa passed to the fake
// claude's `plugin install`, proving the pre-warm ran and which scope it used.
func theRecordedPrewarmInstallScopeIs(ctx context.Context, want string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	data, err := os.ReadFile(filepath.Join(s.homeDir, "prewarm-install-scope"))
	if err != nil {
		return ctx, fmt.Errorf("reading recorded install scope (did pre-warm run?): %w", err)
	}
	if got := strings.TrimSpace(string(data)); got != want {
		return ctx, fmt.Errorf("pre-warm install scope = %q, want %q", got, want)
	}
	return ctx, nil
}

// prewarmCalls returns the `claude plugin` calls the fake recorded, one per line.
func prewarmCalls(ctx context.Context) ([]string, error) {
	s := getState(ctx)
	if s == nil {
		return nil, fmt.Errorf("no test state")
	}
	data, err := os.ReadFile(filepath.Join(s.homeDir, "prewarm-calls"))
	if err != nil {
		return nil, fmt.Errorf("reading recorded pre-warm calls (did pre-warm run?): %w", err)
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n"), nil
}

// thePrewarmCallLogHasBefore asserts that the first recorded call containing
// first comes before the first recorded call containing second, and that both
// were made.
func thePrewarmCallLogHasBefore(ctx context.Context, first, second string) (context.Context, error) {
	calls, err := prewarmCalls(ctx)
	if err != nil {
		return ctx, err
	}
	i, j := -1, -1
	for n, c := range calls {
		if i < 0 && strings.Contains(c, first) {
			i = n
		}
		if j < 0 && strings.Contains(c, second) {
			j = n
		}
	}
	if i < 0 || j < 0 || i > j {
		return ctx, fmt.Errorf("want a call with %q before a call with %q; calls:\n%s", first, second, strings.Join(calls, "\n"))
	}
	return ctx, nil
}

// thePrewarmCallLogHasCount asserts how many recorded calls contain sub.
func thePrewarmCallLogHasCount(ctx context.Context, want int, sub string) (context.Context, error) {
	calls, err := prewarmCalls(ctx)
	if err != nil {
		return ctx, err
	}
	got := 0
	for _, c := range calls {
		if strings.Contains(c, sub) {
			got++
		}
	}
	if got != want {
		return ctx, fmt.Errorf("%d calls contain %q, want %d; calls:\n%s", got, sub, want, strings.Join(calls, "\n"))
	}
	return ctx, nil
}

func registerPrewarmDriftSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^a fake claude for plugin pre-warming$`, aFakeClaudeForPrewarm)
	ctx.Step(`^the recorded pre-warm install scope is "([^"]*)"$`, theRecordedPrewarmInstallScopeIs)
	ctx.Step(`^the pre-warm made a call with "([^"]*)" before a call with "([^"]*)"$`, thePrewarmCallLogHasBefore)
	ctx.Step(`^the pre-warm made (\d+) calls? with "([^"]*)"$`, thePrewarmCallLogHasCount)
}
