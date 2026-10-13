package functional

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/cucumber/godog"
)

// The Claude trust scenarios read and seed <homeDir>/.claude.json, the file
// niwa resolves when CLAUDE_CONFIG_DIR is unset; buildEnv strips that variable
// and points HOME at the scenario sandbox, so these steps and the binary see
// the same file and never the developer's own.

// registerClaudeTrustSteps installs the instance-trust steps. It is called
// from suite_test.go's scenario initializer.
func registerClaudeTrustSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the Claude config in HOME has a trusted entry for "([^"]*)"$`, theClaudeConfigHasTrustedEntryFor)
	ctx.Step(`^the Claude config in HOME still has a trusted entry for "([^"]*)"$`, theClaudeConfigStillHasTrustedEntryFor)
	ctx.Step(`^the Claude config in HOME trusts instance "([^"]*)"$`, theClaudeConfigTrustsInstance)
	ctx.Step(`^the Claude config in HOME has no entry for instance "([^"]*)"$`, theClaudeConfigHasNoEntryForInstance)
	ctx.Step(`^the Claude config in HOME keeps an untrusted entry for instance "([^"]*)"$`, theClaudeConfigKeepsUntrustedEntryForInstance)
	ctx.Step(`^instance "([^"]*)" records Claude trust with mode "([^"]*)"$`, instanceRecordsClaudeTrust)
	ctx.Step(`^instance "([^"]*)" records no Claude trust$`, instanceRecordsNoClaudeTrust)
	ctx.Step(`^the Claude config in HOME trusts the dispatch instance, recorded with mode "([^"]*)"$`, theClaudeConfigTrustsTheDispatchInstance)
	ctx.Step(`^the Claude config in HOME trusts repo "([^"]*)" of instance "([^"]*)"$`, theClaudeConfigTrustsRepo)
	ctx.Step(`^the Claude config in HOME has no entry for repo "([^"]*)" of instance "([^"]*)"$`, theClaudeConfigHasNoEntryForRepo)
	ctx.Step(`^the Claude config in HOME keeps an untrusted entry for repo "([^"]*)" of instance "([^"]*)"$`, theClaudeConfigKeepsUntrustedEntryForRepo)
	ctx.Step(`^instance "([^"]*)" records Claude trust for repo "([^"]*)" with mode "([^"]*)"$`, instanceRecordsRepoClaudeTrust)
	ctx.Step(`^instance "([^"]*)" records no Claude trust for repos$`, instanceRecordsNoRepoClaudeTrust)
}

// repoTrustKey is the projects key Claude Code computes for a session started
// in the repository rel ("<group>/<repo>") of instance. niwa clones the
// repository as a plain directory, so only the workspace root can hold a
// symlink.
func repoTrustKey(s *testState, instance, rel string) (string, error) {
	key, err := instanceTrustKey(s, instance)
	if err != nil {
		return "", err
	}
	return filepath.Join(key, filepath.FromSlash(rel)), nil
}

func theClaudeConfigTrustsRepo(ctx context.Context, rel, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	key, err := repoTrustKey(s, instance, rel)
	if err != nil {
		return err
	}
	projects, err := claudeProjects(s)
	if err != nil {
		return err
	}
	if e := projects[key]; e["hasTrustDialogAccepted"] != true {
		return fmt.Errorf("no trusted entry keyed %q in %s; projects: %v", key, claudeConfigPath(s), projects)
	}
	return nil
}

func theClaudeConfigHasNoEntryForRepo(ctx context.Context, rel, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	key, err := repoTrustKey(s, instance, rel)
	if err != nil {
		return err
	}
	projects, err := claudeProjects(s)
	if err != nil {
		return err
	}
	if e, ok := projects[key]; ok {
		return fmt.Errorf("entry keyed %q still in %s: %v", key, claudeConfigPath(s), e)
	}
	return nil
}

func theClaudeConfigKeepsUntrustedEntryForRepo(ctx context.Context, rel, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	key, err := repoTrustKey(s, instance, rel)
	if err != nil {
		return err
	}
	projects, err := claudeProjects(s)
	if err != nil {
		return err
	}
	e, ok := projects[key]
	if !ok {
		return fmt.Errorf("the entry keyed %q is gone; the off switch removes only the flag", key)
	}
	if _, has := e["hasTrustDialogAccepted"]; has {
		return fmt.Errorf("entry keyed %q still carries the trust flag: %v", key, e)
	}
	return nil
}

// recordedRepoClaudeTrust reads the claude_trust_repos records from the
// instance state.
func recordedRepoClaudeTrust(s *testState, instance string) (map[string]map[string]string, error) {
	path := filepath.Join(s.workspaceRoot, instance, ".niwa", "instance.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var state struct {
		ClaudeTrustRepos map[string]map[string]string `json:"claude_trust_repos"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return state.ClaudeTrustRepos, nil
}

func instanceRecordsRepoClaudeTrust(ctx context.Context, instance, rel, mode string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	key, err := repoTrustKey(s, instance, rel)
	if err != nil {
		return err
	}
	records, err := recordedRepoClaudeTrust(s, instance)
	if err != nil {
		return err
	}
	got := records[rel]
	want := map[string]string{
		"config_file": claudeConfigPath(s),
		"key":         key,
		"mode":        mode,
		"source":      "workspace",
	}
	for k, v := range want {
		if got[k] != v {
			return fmt.Errorf("claude_trust_repos[%q] = %v, want %v", rel, got, want)
		}
	}
	return nil
}

func instanceRecordsNoRepoClaudeTrust(ctx context.Context, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	got, err := recordedRepoClaudeTrust(s, instance)
	if err != nil {
		return err
	}
	if got != nil {
		return fmt.Errorf("claude_trust_repos = %v, want no records", got)
	}
	return nil
}

// theClaudeConfigTrustsTheDispatchInstance checks the instance the last
// dispatch provisioned, whose name carries a random suffix, is trusted and
// recorded. A dispatch step must have recorded the instance path first.
func theClaudeConfigTrustsTheDispatchInstance(ctx context.Context, mode string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastDispatchInstancePath == "" {
		return fmt.Errorf("no dispatch instance path recorded")
	}
	name := filepath.Base(s.lastDispatchInstancePath)
	if err := theClaudeConfigTrustsInstance(ctx, name); err != nil {
		return err
	}
	return instanceRecordsClaudeTrust(ctx, name, mode)
}

// claudeConfigPath is the sandboxed $HOME/.claude.json.
func claudeConfigPath(s *testState) string {
	return filepath.Join(s.homeDir, ".claude.json")
}

// instanceTrustKey is the projects key Claude Code computes for the named
// instance: the instance root with every symlink resolved (on macOS the
// sandbox sits under /var, a link to /private/var). The workspace root is
// resolved rather than the instance itself, so the key is computable after a
// destroy removed the instance.
func instanceTrustKey(s *testState, instance string) (string, error) {
	root, err := filepath.EvalSymlinks(s.workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("resolving the workspace root: %w", err)
	}
	return filepath.Join(root, instance), nil
}

// claudeProjects decodes the projects object of the sandboxed Claude config.
// A missing file has no projects.
func claudeProjects(s *testState) (map[string]map[string]any, error) {
	data, err := os.ReadFile(claudeConfigPath(s))
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	var doc struct {
		Projects map[string]map[string]any `json:"projects"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w\n%s", claudeConfigPath(s), err, data)
	}
	if doc.Projects == nil {
		doc.Projects = map[string]map[string]any{}
	}
	return doc.Projects, nil
}

// theClaudeConfigHasTrustedEntryFor seeds a hand-made trusted entry, the kind
// a developer's own answer to Claude Code's trust prompt leaves, for path.
func theClaudeConfigHasTrustedEntryFor(ctx context.Context, path string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	doc := map[string]any{
		"numStartups": 4,
		"projects": map[string]any{
			path: map[string]any{"hasTrustDialogAccepted": true, "allowedTools": []string{"Read"}},
		},
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return ctx, err
	}
	return ctx, os.WriteFile(claudeConfigPath(s), data, 0o600)
}

func theClaudeConfigStillHasTrustedEntryFor(ctx context.Context, path string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	projects, err := claudeProjects(s)
	if err != nil {
		return err
	}
	e, ok := projects[path]
	if !ok || e["hasTrustDialogAccepted"] != true || e["allowedTools"] == nil {
		return fmt.Errorf("entry for %q = %v, want the hand-made trusted entry intact", path, e)
	}
	return nil
}

func theClaudeConfigTrustsInstance(ctx context.Context, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	key, err := instanceTrustKey(s, instance)
	if err != nil {
		return err
	}
	projects, err := claudeProjects(s)
	if err != nil {
		return err
	}
	if e := projects[key]; e["hasTrustDialogAccepted"] != true {
		return fmt.Errorf("no trusted entry keyed %q in %s; projects: %v", key, claudeConfigPath(s), projects)
	}
	return nil
}

func theClaudeConfigHasNoEntryForInstance(ctx context.Context, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	key, err := instanceTrustKey(s, instance)
	if err != nil {
		return err
	}
	projects, err := claudeProjects(s)
	if err != nil {
		return err
	}
	if e, ok := projects[key]; ok {
		return fmt.Errorf("entry keyed %q still in %s: %v", key, claudeConfigPath(s), e)
	}
	return nil
}

func theClaudeConfigKeepsUntrustedEntryForInstance(ctx context.Context, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	key, err := instanceTrustKey(s, instance)
	if err != nil {
		return err
	}
	projects, err := claudeProjects(s)
	if err != nil {
		return err
	}
	e, ok := projects[key]
	if !ok {
		return fmt.Errorf("the entry keyed %q is gone; the off switch removes only the flag", key)
	}
	if _, has := e["hasTrustDialogAccepted"]; has {
		return fmt.Errorf("entry keyed %q still carries the trust flag: %v", key, e)
	}
	return nil
}

// recordedClaudeTrust reads the claude_trust record from the instance state.
func recordedClaudeTrust(s *testState, instance string) (map[string]string, error) {
	path := filepath.Join(s.workspaceRoot, instance, ".niwa", "instance.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var state struct {
		ClaudeTrust map[string]string `json:"claude_trust"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return state.ClaudeTrust, nil
}

func instanceRecordsClaudeTrust(ctx context.Context, instance, mode string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	key, err := instanceTrustKey(s, instance)
	if err != nil {
		return err
	}
	got, err := recordedClaudeTrust(s, instance)
	if err != nil {
		return err
	}
	want := map[string]string{
		"config_file": claudeConfigPath(s),
		"key":         key,
		"mode":        mode,
		"source":      "workspace",
	}
	for k, v := range want {
		if got[k] != v {
			return fmt.Errorf("claude_trust = %v, want %v", got, want)
		}
	}
	return nil
}

func instanceRecordsNoClaudeTrust(ctx context.Context, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	got, err := recordedClaudeTrust(s, instance)
	if err != nil {
		return err
	}
	if got != nil {
		return fmt.Errorf("claude_trust = %v, want no record", got)
	}
	return nil
}
