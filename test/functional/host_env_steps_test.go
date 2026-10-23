package functional

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/cucumber/godog"
)

// registerHostEnvSteps wires the host-local env layer steps: exact reads of a
// repo settings document's session env block, where a substring check on the
// file could not tell a key's absence from its value appearing elsewhere.
// Called from initializeScenario.
func registerHostEnvSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the repo "([^"]*)" settings document in instance "([^"]*)" sets env "([^"]*)" to "([^"]*)"$`, theRepoSettingsDocumentSetsEnv)
	ctx.Step(`^the repo "([^"]*)" settings document in instance "([^"]*)" sets no env "([^"]*)"$`, theRepoSettingsDocumentSetsNoEnv)
}

// repoSettingsEnv reads the env block of a repo's settings document.
func repoSettingsEnv(ctx context.Context, groupRepo, instance string) (map[string]any, error) {
	s := getState(ctx)
	if s == nil {
		return nil, fmt.Errorf("no test state")
	}
	path := repoSettingsPath(s, groupRepo, instance)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	env, _ := doc["env"].(map[string]any)
	return env, nil
}

func theRepoSettingsDocumentSetsEnv(ctx context.Context, groupRepo, instance, key, want string) error {
	env, err := repoSettingsEnv(ctx, groupRepo, instance)
	if err != nil {
		return err
	}
	got, ok := env[key]
	if !ok {
		return fmt.Errorf("settings env has no %s; keys: %v", key, keysOf(env))
	}
	if got != want {
		return fmt.Errorf("settings env %s = %v, want %q", key, got, want)
	}
	return nil
}

func theRepoSettingsDocumentSetsNoEnv(ctx context.Context, groupRepo, instance, key string) error {
	env, err := repoSettingsEnv(ctx, groupRepo, instance)
	if err != nil {
		return err
	}
	if _, ok := env[key]; ok {
		return fmt.Errorf("settings env sets %s; want it absent", key)
	}
	return nil
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
