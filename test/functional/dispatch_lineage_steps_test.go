package functional

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"

	"github.com/tsukumogami/niwa/internal/otelprecedence"
)

// The lineage scenarios check the resource attributes a dispatched worker
// ends up with. The fake claude records its OTEL_RESOURCE_ATTRIBUTES at
// $HOME/dispatch-launch-otel beside the argv it already records; with the
// seeded user settings file, those are every layer the worker's value comes
// from, and otelprecedence works the value out under each order Claude Code
// could apply them in.

// lineageAttributeNames are the attributes a dispatch that passes --brief and
// --skill from a calling session sets. They are restated here, not imported:
// this list is the contract the scenarios hold niwa to.
var lineageAttributeNames = []string{
	"niwa.dispatch.id",
	"niwa.dispatch.slug",
	"niwa.parent.session.id",
	"niwa.requested.skill",
	"niwa.brief.id",
	"niwa.workspace",
}

func registerDispatchLineageSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the user's Claude settings under a temporary config directory hold resource attributes "([^"]*)"$`, userClaudeSettingsHoldResourceAttributes)
	ctx.Step(`^the dispatching shell exports resource attributes "([^"]*)"$`, dispatchingShellExportsResourceAttributes)
	ctx.Step(`^the dispatching session is "([^"]*)"$`, theDispatchingSessionIs)
	ctx.Step(`^a brief file "([^"]*)" in the workspace root$`, aBriefFileInTheWorkspaceRoot)
	ctx.Step(`^under every precedence order the worker's resource attributes hold "([^"]*)" and every niwa attribute$`, underEveryPrecedenceOrderTheWorkersAttributesHold)
}

// lineageSettingsPath is where the seeded user settings file lives.
func lineageSettingsPath(s *testState) string {
	return filepath.Join(s.tmpDir, "claude-config", "settings.json")
}

func userClaudeSettingsHoldResourceAttributes(ctx context.Context, value string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	path := lineageSettingsPath(s)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ctx, err
	}
	doc := fmt.Sprintf(`{"env":{"OTEL_RESOURCE_ATTRIBUTES":%q}}`, value)
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		return ctx, err
	}
	s.envOverrides["CLAUDE_CONFIG_DIR"] = filepath.Dir(path)
	return ctx, nil
}

func dispatchingShellExportsResourceAttributes(ctx context.Context, value string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	s.envOverrides[otelprecedence.Variable] = value
	return ctx, nil
}

func theDispatchingSessionIs(ctx context.Context, id string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	s.envOverrides["CLAUDE_CODE_SESSION_ID"] = id
	return ctx, nil
}

func aBriefFileInTheWorkspaceRoot(ctx context.Context, name string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	return ctx, os.WriteFile(filepath.Join(s.workspaceRoot, name), []byte("the brief\n"), 0o644)
}

func underEveryPrecedenceOrderTheWorkersAttributesHold(ctx context.Context, seeded string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	argv, err := os.ReadFile(filepath.Join(s.homeDir, "dispatch-launch-argv-elements"))
	if err != nil {
		return ctx, fmt.Errorf("reading the recorded launch argv: %w", err)
	}
	launch := otelprecedence.Launch{}
	for _, el := range bytes.Split(bytes.TrimSuffix(argv, []byte{0}), []byte{0}) {
		launch.Argv = append(launch.Argv, string(el))
	}
	if env, err := os.ReadFile(filepath.Join(s.homeDir, "dispatch-launch-otel")); err == nil {
		launch.Env = []string{otelprecedence.Variable + "=" + string(env)}
	}
	launch.UserSettings, launch.UserSettingsSet = otelprecedence.UserSettingsValue(lineageSettingsPath(s))

	var failures []string
	for _, mode := range otelprecedence.Modes {
		value, _ := launch.Effective(mode)
		entries := otelprecedence.Entries(value)
		for k, v := range otelprecedence.Entries(seeded) {
			if entries[k] != v {
				failures = append(failures, fmt.Sprintf("%s: %s=%s (have %q)", mode, k, v, entries[k]))
			}
		}
		for _, name := range lineageAttributeNames {
			if entries[name] == "" {
				failures = append(failures, fmt.Sprintf("%s: %s missing", mode, name))
			}
		}
	}
	if len(failures) > 0 {
		return ctx, fmt.Errorf("the worker's resource attributes fall short:\n  %s", strings.Join(failures, "\n  "))
	}
	return ctx, nil
}
