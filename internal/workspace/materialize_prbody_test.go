package workspace

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// prBodyCommandsFor returns every hook command registered for the given Pascal
// event that is niwa's inline pr-body pass-through — one carrying the
// `shirabe pr-body-hook` marker.
func prBodyCommandsFor(doc settingsHooksDoc, event string) []string {
	var cmds []string
	for _, entry := range doc.Hooks[event] {
		for _, h := range entry.Hooks {
			if strings.Contains(h.Command, "shirabe pr-body-hook") {
				cmds = append(cmds, h.Command)
			}
		}
	}
	return cmds
}

// TestPrBodyHook_InjectedForShirabeAdopter is the core default-on case: a repo
// that installs the shirabe plugin and declares no PreToolUse hook of its own
// materializes niwa's default pr-body PreToolUse hook — matcher Bash, a pure
// pass-through behind the `command -v shirabe` guard. It asserts the
// PreToolUse-specific safety contract: the command must NOT `exec` (a non-zero
// PreToolUse exit blocks the tool call) and must fall back to allow via a
// trailing `|| exit 0`.
func TestPrBodyHook_InjectedForShirabeAdopter(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test-ws"},
		Claude:    config.ClaudeConfig{Plugins: shirabePluginList()},
	}
	doc := readSettingsHooks(t, runWorkSummaryRepo(t, cfg))

	entries := doc.Hooks["PreToolUse"]
	if len(entries) != 1 {
		t.Fatalf("PreToolUse: want exactly 1 entry, got %d: %+v", len(entries), entries)
	}
	if entries[0].Matcher != "Bash" {
		t.Errorf("matcher = %q, want %q", entries[0].Matcher, "Bash")
	}
	if len(entries[0].Hooks) != 1 {
		t.Fatalf("want 1 command, got %d", len(entries[0].Hooks))
	}
	cmd := entries[0].Hooks[0].Command
	if !strings.HasPrefix(cmd, "command -v shirabe >/dev/null 2>&1 || exit 0;") {
		t.Errorf("command = %q, missing fail-safe guard prefix", cmd)
	}
	if !strings.HasSuffix(cmd, "shirabe pr-body-hook 2>/dev/null || exit 0") {
		t.Errorf("command = %q, want trailing pr-body-hook invocation that falls back to allow", cmd)
	}
	if strings.Contains(cmd, "exec ") {
		t.Errorf("command = %q, must NOT exec: a non-zero PreToolUse exit blocks the tool call", cmd)
	}
}

// TestPrBodyHook_AbsentWithoutShirabe asserts the plugin gate: an instance that
// does not install the shirabe plugin receives no pr-body hook.
func TestPrBodyHook_AbsentWithoutShirabe(t *testing.T) {
	other := []string{"somethingelse@mkt"}
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test-ws"},
		Claude:    config.ClaudeConfig{Plugins: &other},
	}
	doc := readSettingsHooks(t, runWorkSummaryRepo(t, cfg))
	if cmds := prBodyCommandsFor(doc, "PreToolUse"); len(cmds) != 0 {
		t.Errorf("expected no pr-body hook without shirabe, got %v", cmds)
	}
}

// TestPrBodyHook_OffSwitchSuppresses asserts the off switch: [claude]
// pr_body_hook = false suppresses the default even for a shirabe adopter.
func TestPrBodyHook_OffSwitchSuppresses(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test-ws"},
		Claude: config.ClaudeConfig{
			Plugins:    shirabePluginList(),
			PrBodyHook: boolPtr(false),
		},
	}
	doc := readSettingsHooks(t, runWorkSummaryRepo(t, cfg))
	if cmds := prBodyCommandsFor(doc, "PreToolUse"); len(cmds) != 0 {
		t.Errorf("off switch should suppress injection, got %v", cmds)
	}
}

// TestPrBodyHook_DefaultOnKeyIsOn asserts an explicit pr_body_hook = true is
// equivalent to the absent-key default: injection stays on.
func TestPrBodyHook_DefaultOnKeyIsOn(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test-ws"},
		Claude: config.ClaudeConfig{
			Plugins:    shirabePluginList(),
			PrBodyHook: boolPtr(true),
		},
	}
	doc := readSettingsHooks(t, runWorkSummaryRepo(t, cfg))
	if cmds := prBodyCommandsFor(doc, "PreToolUse"); len(cmds) != 1 {
		t.Errorf("pr_body_hook = true should inject exactly one, got %v", cmds)
	}
}

// TestPrBodyHook_NoDoubleRegistration is the idempotence guard: a workspace that
// installs shirabe AND still declares its own pr-body PreToolUse hook must not be
// double-registered. The declared script is kept; niwa injects no inline
// duplicate.
func TestPrBodyHook_NoDoubleRegistration(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	dir := filepath.Join(configDir, "hooks", "pre_tool_use")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join("hooks", "pre_tool_use", "pr-body-guard.sh")
	body := "#!/usr/bin/env bash\ncommand -v shirabe >/dev/null 2>&1 || exit 0\nshirabe pr-body-hook 2>/dev/null || exit 0\n"
	if err := os.WriteFile(filepath.Join(configDir, rel), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	repoDir := filepath.Join(tmpDir, "instance", "public", "repo1")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test-ws"},
		Claude: config.ClaudeConfig{
			Plugins: shirabePluginList(),
			Hooks: config.HooksConfig{
				"pre_tool_use": {{Matcher: "Bash", Scripts: []string{rel}}},
			},
		},
	}
	materializers := defaultRepoMaterializers(io.Discard)
	if _, _, err := runRepoMaterializers(materializers, repoMaterializeInputs{
		Cfg:       cfg,
		ConfigDir: configDir,
		RepoName:  "repo1",
		RepoDir:   repoDir,
	}); err != nil {
		t.Fatalf("runRepoMaterializers: %v", err)
	}
	doc := readSettingsHooks(t, repoDir)

	total := 0
	for _, e := range doc.Hooks["PreToolUse"] {
		total += len(e.Hooks)
	}
	if total != 1 {
		t.Errorf("PreToolUse: want exactly 1 registered command (declared only, no inline duplicate), got %d: %+v", total, doc.Hooks["PreToolUse"])
	}
	if cmds := prBodyCommandsFor(doc, "PreToolUse"); len(cmds) != 1 {
		// The single command is the declared script's inline check via the
		// installed hook path; niwa must not add a second inline pass-through.
		if len(cmds) > 1 {
			t.Errorf("niwa injected an inline duplicate alongside the declared hook: %v", cmds)
		}
	}
}

// TestPrBodyHook_ComposesWithExistingPreToolUse asserts the PreToolUse-specific
// composition: a workspace that declares an unrelated PreToolUse Bash hook (e.g.
// a gate-online guard, no pr-body-hook marker) keeps it AND gets niwa's pr-body
// hook appended, rather than one replacing the other.
func TestPrBodyHook_ComposesWithExistingPreToolUse(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	dir := filepath.Join(configDir, "hooks", "pre_tool_use")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join("hooks", "pre_tool_use", "gate-online.sh")
	body := "#!/usr/bin/env bash\n# an unrelated PreToolUse guard\nexit 0\n"
	if err := os.WriteFile(filepath.Join(configDir, rel), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	repoDir := filepath.Join(tmpDir, "instance", "public", "repo1")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test-ws"},
		Claude: config.ClaudeConfig{
			Plugins: shirabePluginList(),
			Hooks: config.HooksConfig{
				"pre_tool_use": {{Matcher: "Bash", Scripts: []string{rel}}},
			},
		},
	}
	materializers := defaultRepoMaterializers(io.Discard)
	if _, _, err := runRepoMaterializers(materializers, repoMaterializeInputs{
		Cfg:       cfg,
		ConfigDir: configDir,
		RepoName:  "repo1",
		RepoDir:   repoDir,
	}); err != nil {
		t.Fatalf("runRepoMaterializers: %v", err)
	}
	doc := readSettingsHooks(t, repoDir)

	// niwa's inline pr-body pass-through must be present...
	if cmds := prBodyCommandsFor(doc, "PreToolUse"); len(cmds) != 1 {
		t.Errorf("want niwa's pr-body hook injected alongside the declared guard, got %v", cmds)
	}
	// ...and the unrelated declared guard must still be registered (total >= 2).
	total := 0
	for _, e := range doc.Hooks["PreToolUse"] {
		total += len(e.Hooks)
	}
	if total < 2 {
		t.Errorf("PreToolUse: want the declared guard AND the pr-body hook (>=2 commands), got %d: %+v", total, doc.Hooks["PreToolUse"])
	}
}
