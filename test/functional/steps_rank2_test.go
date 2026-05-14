package functional

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
)

// theStderrContains asserts that the most recent command's stderr
// contains the given substring. Used by the rank-2 deprecation
// scenario to verify the one-time notice fires on the first apply
// AND does NOT fire on subsequent applies.
func theStderrContains(ctx context.Context, substr string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if !strings.Contains(s.stderr, substr) {
		return ctx, fmt.Errorf("stderr does not contain %q\nstderr was:\n%s", substr, s.stderr)
	}
	return ctx, nil
}

// theStderrDoesNotContain asserts the most recent command's stderr
// does NOT contain the given substring.
func theStderrDoesNotContain(ctx context.Context, substr string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if strings.Contains(s.stderr, substr) {
		return ctx, fmt.Errorf("stderr should not contain %q but did\nstderr was:\n%s", substr, s.stderr)
	}
	return ctx, nil
}

// theFileExistsInHome asserts that the named file exists in the
// scenario-sandboxed $HOME. Used to verify the niwa Claude Code
// plugin was auto-installed at
// ~/.claude/plugins/marketplaces/niwa/manifest.json after the
// rank-2 notice fires.
func theFileExistsInHome(ctx context.Context, path string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	full := filepath.Join(s.homeDir, path)
	if _, err := os.Stat(full); err != nil {
		return ctx, fmt.Errorf("file %q in HOME (%s): %w", path, full, err)
	}
	return ctx, nil
}

// iRunNiwaInitFromConfigRepoWithNoInstallPlugins runs
// `niwa init --from <url> --no-install-plugins` from workspaceRoot.
// Used by the rank-2 opt-out scenario to verify the deprecation
// notice still fires while the auto-install is suppressed.
func iRunNiwaInitFromConfigRepoWithNoInstallPlugins(ctx context.Context, name string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	url, ok := s.repoURLs[name]
	if !ok {
		return ctx, fmt.Errorf("no URL stored for config repo %q", name)
	}
	return ctx, runNiwa(s, s.workspaceRoot, "niwa init --from "+url+" --no-install-plugins")
}

// aRank2ConfigRepoExistsWithBody creates a bare repo that places
// workspace.toml at the source repo root (the deprecated rank-2
// layout). Used by tests that intentionally exercise the rank-2
// deprecation notice path.
func aRank2ConfigRepoExistsWithBody(ctx context.Context, name string, body *godog.DocString) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	content := body.Content
	for repoName, repoURL := range s.repoURLs {
		content = strings.ReplaceAll(content, "{repo:"+repoName+"}", repoURL)
	}
	url, err := s.gitServer.ConfigRepoRank2(name, content)
	if err != nil {
		return ctx, fmt.Errorf("creating rank-2 config repo %q: %w", name, err)
	}
	s.repoURLs[name] = url
	return ctx, nil
}

// registerRank2Steps installs the rank-2 / plugin-install steps.
// Called from suite_test.go's InitializeScenario hook.
func registerRank2Steps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the stderr contains "([^"]*)"$`, theStderrContains)
	ctx.Step(`^the stderr does not contain "([^"]*)"$`, theStderrDoesNotContain)
	ctx.Step(`^the file "([^"]*)" exists in HOME$`, theFileExistsInHome)
	ctx.Step(`^I run niwa init from config repo "([^"]*)" with --no-install-plugins$`, iRunNiwaInitFromConfigRepoWithNoInstallPlugins)
	ctx.Step(`^a rank-2 config repo "([^"]*)" exists with body:$`, aRank2ConfigRepoExistsWithBody)
}
