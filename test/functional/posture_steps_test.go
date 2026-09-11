package functional

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
)

// posture_steps_test.go holds the steps that check a workspace's declared
// permission posture from outside the process: what the launched worker's argv
// carries, and what each generated Claude Code settings document says. The
// scenarios take their posture from a config repo or a personal overlay run
// through the real binary, never from a hand-written settings or state file.

// theHostConfigDeclaresGlobalSettings puts the docstring's keys under [global]
// in the sandboxed host config ($XDG_CONFIG_HOME/niwa/config.toml). It edits
// the file in place rather than replacing it, because `niwa init` writes the
// workspace registry into the same file: when a [global] header is already
// there the keys go directly under it, otherwise a [global] table is appended.
func theHostConfigDeclaresGlobalSettings(ctx context.Context, body *godog.DocString) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	dir := filepath.Join(s.homeDir, ".config", "niwa")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ctx, fmt.Errorf("creating host config dir: %w", err)
	}
	path := filepath.Join(dir, "config.toml")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return ctx, fmt.Errorf("reading host config %s: %w", path, err)
	}
	keys := strings.TrimSpace(body.Content)

	lines := strings.Split(string(existing), "\n")
	inserted := false
	for i, line := range lines {
		if strings.TrimSpace(line) == "[global]" {
			rest := append([]string{keys}, lines[i+1:]...)
			lines = append(lines[:i+1], rest...)
			inserted = true
			break
		}
	}
	var out string
	if inserted {
		out = strings.Join(lines, "\n")
	} else {
		out = strings.TrimRight(string(existing), "\n")
		if out != "" {
			out += "\n\n"
		}
		out += "[global]\n" + keys + "\n"
	}
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		return ctx, fmt.Errorf("writing host config %s: %w", path, err)
	}
	return ctx, nil
}

// theLaunchedClaudeWasInvokedWithExactlyTimes counts how often a fragment
// appears in the argv the fake claude recorded on its --bg launch. It is what
// tells "the explicit flag won" apart from "both flags were passed": a worker
// handed --permission-mode twice gets whichever one Claude Code parses last.
func theLaunchedClaudeWasInvokedWithExactlyTimes(ctx context.Context, fragment string, want int) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	argv, err := launchedClaudeArgv(s)
	if err != nil {
		return err
	}
	if got := strings.Count(argv, fragment); got != want {
		return fmt.Errorf("launched claude argv contains %q %d times, want %d:\n%s", fragment, got, want, strings.TrimSpace(argv))
	}
	return nil
}

// validDefaultModes are the permissions.defaultMode values a project settings
// document may carry. niwa must never write bypassPermissions (ignored from a
// project file), auto, or askPermissions (rejected, voiding the whole file).
var validDefaultModes = map[string]bool{
	"default":     true,
	"acceptEdits": true,
	"plan":        true,
	"dontAsk":     true,
}

// readSettingsDocument reads and parses the settings document at path. A
// missing file or one that isn't a JSON object is an error, so an absence
// check can't pass on a document that was never written.
func readSettingsDocument(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading settings document %s: %w", path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("settings document %s doesn't parse as a JSON object: %w\n%s", path, err, data)
	}
	return doc, nil
}

// assertSettingsDefaultMode checks the document's permissions.defaultMode
// against want, where "none" means the key must be absent. Whatever the
// expectation, a present value outside validDefaultModes fails.
func assertSettingsDefaultMode(path, want string) error {
	doc, err := readSettingsDocument(path)
	if err != nil {
		return err
	}
	var (
		mode    any
		present bool
	)
	if perms, ok := doc["permissions"].(map[string]any); ok {
		mode, present = perms["defaultMode"]
	}
	if present {
		str, ok := mode.(string)
		if !ok || !validDefaultModes[str] {
			return fmt.Errorf("%s carries permissions.defaultMode %v, which Claude Code doesn't honor from a project settings file", path, mode)
		}
	}
	if want == "none" {
		if present {
			return fmt.Errorf("%s carries permissions.defaultMode %v, want none", path, mode)
		}
		return nil
	}
	if !present {
		return fmt.Errorf("%s carries no permissions.defaultMode, want %q", path, want)
	}
	if mode != want {
		return fmt.Errorf("%s carries permissions.defaultMode %v, want %q", path, mode, want)
	}
	return nil
}

// The four settings documents a posture reaches. The workspace root and the
// instance root carry .claude/settings.json; a repo and a worktree carry
// .claude/settings.local.json.
func workspaceRootSettingsPath(s *testState) string {
	return filepath.Join(s.workspaceRoot, ".claude", "settings.json")
}

func instanceRootSettingsPath(s *testState, instance string) string {
	return filepath.Join(s.workspaceRoot, instance, ".claude", "settings.json")
}

func repoSettingsPath(s *testState, groupRepo, instance string) string {
	return filepath.Join(s.workspaceRoot, instance, groupRepo, ".claude", "settings.local.json")
}

func lastWorktreeSettingsPath(s *testState) (string, error) {
	if s.lastSessionWorktreePath == "" {
		return "", fmt.Errorf("no worktree recorded; create one first")
	}
	return filepath.Join(s.lastSessionWorktreePath, ".claude", "settings.local.json"), nil
}

func theWorkspaceRootSettingsDocumentHasDefaultMode(ctx context.Context, want string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	return assertSettingsDefaultMode(workspaceRootSettingsPath(s), want)
}

func theInstanceSettingsDocumentHasDefaultMode(ctx context.Context, instance, want string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	return assertSettingsDefaultMode(instanceRootSettingsPath(s, instance), want)
}

func theRepoSettingsDocumentHasDefaultMode(ctx context.Context, groupRepo, instance, want string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	return assertSettingsDefaultMode(repoSettingsPath(s, groupRepo, instance), want)
}

func theLastWorktreeSettingsDocumentHasDefaultMode(ctx context.Context, want string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path, err := lastWorktreeSettingsPath(s)
	if err != nil {
		return err
	}
	return assertSettingsDefaultMode(path, want)
}

// theSettingsDocumentsExistAndParse checks, before any value is compared, that
// every document the matrix reads is on disk and parses: the workspace root,
// the instance root, each named repo (comma-separated "<group>/<repo>"), and
// the last worktree. Without it a "none" cell could pass on a missing file.
func theSettingsDocumentsExistAndParse(ctx context.Context, repos, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	paths := []string{workspaceRootSettingsPath(s), instanceRootSettingsPath(s, instance)}
	for _, r := range strings.Split(repos, ",") {
		paths = append(paths, repoSettingsPath(s, strings.TrimSpace(r), instance))
	}
	wt, err := lastWorktreeSettingsPath(s)
	if err != nil {
		return err
	}
	paths = append(paths, wt)
	for _, p := range paths {
		if _, err := readSettingsDocument(p); err != nil {
			return err
		}
	}
	return nil
}

// iRunNiwaApplyTimesInInstance runs `niwa apply` from the instance directory
// the given number of times, failing on a non-zero exit. Zero is a valid
// count: a Scenario Outline row that must not apply between two steps says so
// in its own column rather than in a separate scenario.
func iRunNiwaApplyTimesInInstance(ctx context.Context, times int, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	dir := filepath.Join(s.workspaceRoot, instance)
	for i := 0; i < times; i++ {
		if err := runNiwa(s, dir, "niwa apply"); err != nil {
			return ctx, err
		}
		if s.exitCode != 0 {
			return ctx, fmt.Errorf("niwa apply in instance %q exit=%d\nstdout:\n%s\nstderr:\n%s", instance, s.exitCode, s.stdout, s.stderr)
		}
	}
	return ctx, nil
}

// registerPostureSteps wires the permission-posture steps into the scenario
// context. Called from initializeScenario.
func registerPostureSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the host config declares global settings:$`, theHostConfigDeclaresGlobalSettings)
	ctx.Step(`^the launched claude was invoked with "([^"]*)" exactly (\d+) times?$`, theLaunchedClaudeWasInvokedWithExactlyTimes)
	ctx.Step(`^the workspace root settings document has permissions\.defaultMode "([^"]*)"$`, theWorkspaceRootSettingsDocumentHasDefaultMode)
	ctx.Step(`^the instance "([^"]*)" settings document has permissions\.defaultMode "([^"]*)"$`, theInstanceSettingsDocumentHasDefaultMode)
	ctx.Step(`^the repo "([^"]*)" settings document in instance "([^"]*)" has permissions\.defaultMode "([^"]*)"$`, theRepoSettingsDocumentHasDefaultMode)
	ctx.Step(`^the last worktree settings document has permissions\.defaultMode "([^"]*)"$`, theLastWorktreeSettingsDocumentHasDefaultMode)
	ctx.Step(`^the settings documents for repos "([^"]*)" in instance "([^"]*)" and the last worktree exist and parse as JSON$`, theSettingsDocumentsExistAndParse)
	ctx.Step(`^I run niwa apply (\d+) times? in instance "([^"]*)"$`, iRunNiwaApplyTimesInInstance)
}
