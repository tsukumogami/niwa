package functional

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/cucumber/godog"
)

// dispatch_profile_steps_test.go holds the steps that read what a dispatch
// profile put into the launched claude's settings document. They read the
// document the fake claude recorded on its --bg launch, through the same
// helper the session-message steps use.

// launchedClaudeSettingsMap decodes the launch settings document, failing when
// the launch carried none.
func launchedClaudeSettingsMap(ctx context.Context) (map[string]any, string, error) {
	s := getState(ctx)
	if s == nil {
		return nil, "", fmt.Errorf("no test state")
	}
	doc, ok, err := launchedClaudeSettingsDocument(s)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return nil, "", fmt.Errorf("the launch carried no settings document\nstderr:\n%s", s.stderr)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(doc), &m); err != nil {
		return nil, doc, fmt.Errorf("decoding the launch settings document: %w\ndocument: %s", err, doc)
	}
	return m, doc, nil
}

// splitList splits a comma-separated step argument and trims each entry.
func splitList(list string) []string {
	parts := strings.Split(list, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func theLaunchedClaudeSettingsDeniesExactly(ctx context.Context, names string) error {
	m, doc, err := launchedClaudeSettingsMap(ctx)
	if err != nil {
		return err
	}
	deny := []any{}
	for _, n := range splitList(names) {
		deny = append(deny, n)
	}
	want := map[string]any{"deny": deny}
	if !reflect.DeepEqual(m["permissions"], want) {
		return fmt.Errorf("permissions = %#v, want %#v\ndocument: %s", m["permissions"], want, doc)
	}
	return nil
}

func theLaunchedClaudeSettingsDisablesBundledSkills(ctx context.Context) error {
	m, doc, err := launchedClaudeSettingsMap(ctx)
	if err != nil {
		return err
	}
	if m["disableBundledSkills"] != true {
		return fmt.Errorf("disableBundledSkills = %#v, want true\ndocument: %s", m["disableBundledSkills"], doc)
	}
	return nil
}

func theLaunchedClaudeSettingsSetsEnv(ctx context.Context, name, value string) error {
	m, doc, err := launchedClaudeSettingsMap(ctx)
	if err != nil {
		return err
	}
	env, _ := m["env"].(map[string]any)
	if got, ok := env[name]; !ok || got != value {
		return fmt.Errorf("settings env %s = %#v (present %v), want %q\ndocument: %s", name, got, ok, value, doc)
	}
	return nil
}

func theLaunchedClaudeSettingsHasExactlyKeys(ctx context.Context, keys string) error {
	m, doc, err := launchedClaudeSettingsMap(ctx)
	if err != nil {
		return err
	}
	got := make([]string, 0, len(m))
	for k := range m {
		got = append(got, k)
	}
	slices.Sort(got)
	want := splitList(keys)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		return fmt.Errorf("settings keys = %v, want %v\ndocument: %s", got, want, doc)
	}
	return nil
}

func theLaunchedClaudeSettingsEnvLacks(ctx context.Context, name string) error {
	m, doc, err := launchedClaudeSettingsMap(ctx)
	if err != nil {
		return err
	}
	env, _ := m["env"].(map[string]any)
	if v, ok := env[name]; ok {
		return fmt.Errorf("settings env holds %s=%#v, want it absent\ndocument: %s", name, v, doc)
	}
	return nil
}

// registerDispatchProfileSteps wires the dispatch-profile steps into the
// scenario context.
func registerDispatchProfileSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the launched claude settings document denies exactly "([^"]*)"$`, theLaunchedClaudeSettingsDeniesExactly)
	ctx.Step(`^the launched claude settings document has disableBundledSkills true$`, theLaunchedClaudeSettingsDisablesBundledSkills)
	ctx.Step(`^the launched claude settings document sets env "([^"]*)" to "([^"]*)"$`, theLaunchedClaudeSettingsSetsEnv)
	ctx.Step(`^the launched claude settings document has exactly the keys "([^"]*)"$`, theLaunchedClaudeSettingsHasExactlyKeys)
	ctx.Step(`^the launched claude settings document env has no "([^"]*)"$`, theLaunchedClaudeSettingsEnvLacks)
}
