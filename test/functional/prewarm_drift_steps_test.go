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
//   - `plugin install <plugin> --scope project` REserializes the project
//     settings.json -- the niwa-managed, fingerprinted file -- which is exactly what
//     caused the false "modified outside niwa" drift. Here it appends a newline, which
//     changes the file's content hash while leaving it valid JSON.
//   - `plugin install <plugin> --scope local` writes settings.local.json instead and
//     never touches settings.json, so the managed file's hash is preserved.
//
// Every install records the scope it was invoked with to $HOME/prewarm-install-scope
// so a scenario can assert niwa issued `--scope local` (the fix) and not
// `--scope project` (the regression). Any other invocation exits 0 so an unrelated
// claude call during provisioning never fails the run.
func prewarmFakeClaudeScript() string {
	return `#!/bin/sh
if [ "$1" = "plugin" ]; then
  shift
  case "$1" in
    marketplace)
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

func registerPrewarmDriftSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^a fake claude for plugin pre-warming$`, aFakeClaudeForPrewarm)
	ctx.Step(`^the recorded pre-warm install scope is "([^"]*)"$`, theRecordedPrewarmInstallScopeIs)
}
