package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/secret"
)

func TestHooksMaterializerName(t *testing.T) {
	m := &HooksMaterializer{}
	if got := m.Name(); got != "hooks" {
		t.Errorf("Name() = %q, want %q", got, "hooks")
	}
}

func TestHooksMaterializerEmptyHooks(t *testing.T) {
	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Claude: config.ClaudeConfig{},
		},
	}

	m := &HooksMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("expected no files written, got %d", len(written))
	}
	if ctx.InstalledHooks != nil {
		t.Errorf("expected nil InstalledHooks, got %v", ctx.InstalledHooks)
	}
}

func TestHooksMaterializerSingleEvent(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	hooksDir := filepath.Join(configDir, "hooks", "pre_tool_use")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}

	scriptContent := "#!/bin/bash\necho pre_tool_use\n"
	scriptPath := filepath.Join(hooksDir, "check.sh")
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0o644); err != nil {
		t.Fatal(err)
	}

	repoDir := filepath.Join(tmpDir, "repos", "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Config: &config.WorkspaceConfig{
			Workspace: config.WorkspaceMeta{Name: "test"},
		},
		Effective: EffectiveConfig{
			Claude: config.ClaudeConfig{
				Hooks: config.HooksConfig{
					"pre_tool_use": {{Scripts: []string{"hooks/pre_tool_use/check.sh"}}},
				},
			},
		},
		RepoName:  "myrepo",
		RepoDir:   repoDir,
		ConfigDir: configDir,
	}

	m := &HooksMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file written, got %d", len(written))
	}

	expectedTarget := filepath.Join(repoDir, ".claude", "hooks", "pre_tool_use", "check.local.sh")
	if written[0] != expectedTarget {
		t.Errorf("written[0] = %q, want %q", written[0], expectedTarget)
	}

	// Verify file content.
	data, err := os.ReadFile(expectedTarget)
	if err != nil {
		t.Fatalf("reading installed hook: %v", err)
	}
	if string(data) != scriptContent {
		t.Errorf("hook content = %q, want %q", string(data), scriptContent)
	}

	// Verify executable permission.
	info, err := os.Stat(expectedTarget)
	if err != nil {
		t.Fatalf("stat installed hook: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("hook should be executable, got mode %v", info.Mode())
	}

	// Verify InstalledHooks was populated.
	if ctx.InstalledHooks == nil {
		t.Fatal("InstalledHooks should be set")
	}
	installedEntries, ok := ctx.InstalledHooks["pre_tool_use"]
	if !ok {
		t.Fatal("InstalledHooks should have pre_tool_use key")
	}
	if len(installedEntries) != 1 || len(installedEntries[0].Paths) != 1 || installedEntries[0].Paths[0] != expectedTarget {
		t.Errorf("InstalledHooks[pre_tool_use] = %v, want [{Paths: [%s]}]", installedEntries, expectedTarget)
	}
}

func TestHooksMaterializerMultipleEvents(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")

	// Create scripts for two events.
	for _, event := range []string{"pre_tool_use", "stop"} {
		dir := filepath.Join(configDir, "hooks", event)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		script := filepath.Join(dir, "run.sh")
		if err := os.WriteFile(script, []byte("#!/bin/bash\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	repoDir := filepath.Join(tmpDir, "repos", "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Config: &config.WorkspaceConfig{
			Workspace: config.WorkspaceMeta{Name: "test"},
		},
		Effective: EffectiveConfig{
			Claude: config.ClaudeConfig{
				Hooks: config.HooksConfig{
					"pre_tool_use": {{Scripts: []string{"hooks/pre_tool_use/run.sh"}}},
					"stop":         {{Scripts: []string{"hooks/stop/run.sh"}}},
				},
			},
		},
		RepoName:  "myrepo",
		RepoDir:   repoDir,
		ConfigDir: configDir,
	}

	m := &HooksMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 2 {
		t.Fatalf("expected 2 files written, got %d", len(written))
	}

	// Verify both events have entries in InstalledHooks.
	if len(ctx.InstalledHooks) != 2 {
		t.Errorf("expected 2 events in InstalledHooks, got %d", len(ctx.InstalledHooks))
	}
	for _, event := range []string{"pre_tool_use", "stop"} {
		if _, ok := ctx.InstalledHooks[event]; !ok {
			t.Errorf("InstalledHooks missing event %q", event)
		}
	}
}

func TestHooksMaterializerMultipleScriptsPerEvent(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	eventDir := filepath.Join(configDir, "hooks", "pre_tool_use")
	if err := os.MkdirAll(eventDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"check.sh", "validate.sh"} {
		if err := os.WriteFile(filepath.Join(eventDir, name), []byte("#!/bin/bash\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	repoDir := filepath.Join(tmpDir, "repos", "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Config: &config.WorkspaceConfig{
			Workspace: config.WorkspaceMeta{Name: "test"},
		},
		Effective: EffectiveConfig{
			Claude: config.ClaudeConfig{
				Hooks: config.HooksConfig{
					"pre_tool_use": {{Scripts: []string{
						"hooks/pre_tool_use/check.sh",
						"hooks/pre_tool_use/validate.sh",
					}}},
				},
			},
		},
		RepoName:  "myrepo",
		RepoDir:   repoDir,
		ConfigDir: configDir,
	}

	m := &HooksMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 2 {
		t.Fatalf("expected 2 files written, got %d", len(written))
	}

	hookEntries := ctx.InstalledHooks["pre_tool_use"]
	if len(hookEntries) != 1 || len(hookEntries[0].Paths) != 2 {
		t.Errorf("expected 1 entry with 2 scripts for pre_tool_use, got %v", hookEntries)
	}
}

func TestHooksMaterializerContainmentReject(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write a script outside configDir.
	outsideDir := filepath.Join(tmpDir, "outside")
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "evil.sh"), []byte("#!/bin/bash\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repoDir := filepath.Join(tmpDir, "repos", "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Config: &config.WorkspaceConfig{
			Workspace: config.WorkspaceMeta{Name: "test"},
		},
		Effective: EffectiveConfig{
			Claude: config.ClaudeConfig{
				Hooks: config.HooksConfig{
					"pre_tool_use": {{Scripts: []string{"../outside/evil.sh"}}},
				},
			},
		},
		RepoName:  "myrepo",
		RepoDir:   repoDir,
		ConfigDir: configDir,
	}

	m := &HooksMaterializer{}
	_, err := m.Materialize(ctx)
	if err == nil {
		t.Fatal("expected error for path escaping configDir, got nil")
	}
}

func TestHooksMaterializerMissingSource(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}

	repoDir := filepath.Join(tmpDir, "repos", "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Config: &config.WorkspaceConfig{
			Workspace: config.WorkspaceMeta{Name: "test"},
		},
		Effective: EffectiveConfig{
			Claude: config.ClaudeConfig{
				Hooks: config.HooksConfig{
					"pre_tool_use": {{Scripts: []string{"hooks/nonexistent.sh"}}},
				},
			},
		},
		RepoName:  "myrepo",
		RepoDir:   repoDir,
		ConfigDir: configDir,
	}

	m := &HooksMaterializer{}
	_, err := m.Materialize(ctx)
	if err == nil {
		t.Fatal("expected error for missing source script, got nil")
	}
}

func TestMaterializerInterface(t *testing.T) {
	// Verify HooksMaterializer satisfies the Materializer interface.
	var _ Materializer = &HooksMaterializer{}
	// Verify SettingsMaterializer satisfies the Materializer interface.
	var _ Materializer = &SettingsMaterializer{}
	// Verify EnvMaterializer satisfies the Materializer interface.
	var _ Materializer = &EnvMaterializer{}
}

func TestSettingsMaterializerName(t *testing.T) {
	m := &SettingsMaterializer{}
	if got := m.Name(); got != "settings" {
		t.Errorf("Name() = %q, want %q", got, "settings")
	}
}

func TestSettingsMaterializerNoopWhenEmpty(t *testing.T) {
	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Claude: config.ClaudeConfig{},
		},
	}

	m := &SettingsMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if written != nil {
		t.Errorf("expected nil, got %v", written)
	}
}

func TestSettingsMaterializerPermissionsOnly(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Claude: config.ClaudeConfig{
				Settings: config.SettingsConfig{"permissions": config.MaybeSecret{Plain: "bypass"}},
			},
		},
		RepoDir: repoDir,
	}

	m := &SettingsMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file written, got %d", len(written))
	}

	expectedPath := filepath.Join(repoDir, ".claude", "settings.local.json")
	if written[0] != expectedPath {
		t.Errorf("written[0] = %q, want %q", written[0], expectedPath)
	}

	data, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("reading settings file: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing settings JSON: %v", err)
	}

	perms, ok := doc["permissions"].(map[string]any)
	if !ok {
		t.Fatal("expected permissions key in output")
	}
	if perms["defaultMode"] != "bypassPermissions" {
		t.Errorf("defaultMode = %v, want %q", perms["defaultMode"], "bypassPermissions")
	}

	if _, ok := doc["hooks"]; ok {
		t.Error("hooks key should not be present when no hooks installed")
	}
}

func TestSettingsMaterializerAskPermissions(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Claude: config.ClaudeConfig{
				Settings: config.SettingsConfig{"permissions": config.MaybeSecret{Plain: "ask"}},
			},
		},
		RepoDir: repoDir,
	}

	m := &SettingsMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file, got %d", len(written))
	}

	data, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatalf("reading settings file: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing JSON: %v", err)
	}

	perms := doc["permissions"].(map[string]any)
	if perms["defaultMode"] != "askPermissions" {
		t.Errorf("defaultMode = %v, want %q", perms["defaultMode"], "askPermissions")
	}
}

func TestSettingsMaterializerUnknownPermissions(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Claude: config.ClaudeConfig{
				Settings: config.SettingsConfig{"permissions": config.MaybeSecret{Plain: "invalid"}},
			},
		},
		RepoDir: repoDir,
	}

	m := &SettingsMaterializer{}
	_, err := m.Materialize(ctx)
	if err == nil {
		t.Fatal("expected error for unknown permissions value, got nil")
	}
}

func TestSettingsMaterializerHooksOnly(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Claude: config.ClaudeConfig{},
		},
		RepoDir: repoDir,
		InstalledHooks: map[string][]InstalledHookEntry{
			"pre_tool_use": {{Paths: []string{filepath.Join(repoDir, ".claude", "hooks", "pre_tool_use", "gate.local.sh")}}},
		},
	}

	m := &SettingsMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file, got %d", len(written))
	}

	data, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatalf("reading settings file: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing JSON: %v", err)
	}

	if _, ok := doc["permissions"]; ok {
		t.Error("permissions key should not be present when no settings")
	}

	hooksDoc, ok := doc["hooks"].(map[string]any)
	if !ok {
		t.Fatal("expected hooks key in output")
	}

	entries, ok := hooksDoc["PreToolUse"].([]any)
	if !ok {
		t.Fatal("expected PreToolUse key in hooks")
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 hook entry, got %d", len(entries))
	}

	entry := entries[0].(map[string]any)
	hooksList := entry["hooks"].([]any)
	if len(hooksList) != 1 {
		t.Fatalf("expected 1 hook command, got %d", len(hooksList))
	}
	hookCmd := hooksList[0].(map[string]any)
	if hookCmd["type"] != "command" {
		t.Errorf("type = %v, want %q", hookCmd["type"], "command")
	}
	if hookCmd["command"] != ".claude/hooks/pre_tool_use/gate.local.sh" {
		t.Errorf("command = %v, want %q", hookCmd["command"], ".claude/hooks/pre_tool_use/gate.local.sh")
	}
}

func TestSettingsMaterializerSettingsAndHooks(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Claude: config.ClaudeConfig{
				Settings: config.SettingsConfig{"permissions": config.MaybeSecret{Plain: "bypass"}},
			},
		},
		RepoDir: repoDir,
		InstalledHooks: map[string][]InstalledHookEntry{
			"pre_tool_use": {{Paths: []string{filepath.Join(repoDir, ".claude", "hooks", "pre_tool_use", "gate.local.sh")}}},
			"stop":         {{Paths: []string{filepath.Join(repoDir, ".claude", "hooks", "stop", "stop.local.sh")}}},
		},
	}

	m := &SettingsMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file, got %d", len(written))
	}

	data, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatalf("reading settings file: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing JSON: %v", err)
	}

	// Verify permissions.
	perms := doc["permissions"].(map[string]any)
	if perms["defaultMode"] != "bypassPermissions" {
		t.Errorf("defaultMode = %v, want %q", perms["defaultMode"], "bypassPermissions")
	}

	// Verify hooks.
	hooksDoc := doc["hooks"].(map[string]any)
	for _, tc := range []struct {
		event   string
		command string
	}{
		{"PreToolUse", ".claude/hooks/pre_tool_use/gate.local.sh"},
		{"Stop", ".claude/hooks/stop/stop.local.sh"},
	} {
		entries, ok := hooksDoc[tc.event].([]any)
		if !ok {
			t.Errorf("missing hook event %q", tc.event)
			continue
		}
		if len(entries) != 1 {
			t.Errorf("%s: expected 1 entry, got %d", tc.event, len(entries))
			continue
		}
		entry := entries[0].(map[string]any)
		hooksList := entry["hooks"].([]any)
		if len(hooksList) != 1 {
			t.Errorf("%s: expected 1 hook command, got %d", tc.event, len(hooksList))
			continue
		}
		hookCmd := hooksList[0].(map[string]any)
		if hookCmd["command"] != tc.command {
			t.Errorf("%s command = %v, want %q", tc.event, hookCmd["command"], tc.command)
		}
	}
}

func TestSettingsMaterializerMultipleHooksPerEvent(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Claude: config.ClaudeConfig{},
		},
		RepoDir: repoDir,
		InstalledHooks: map[string][]InstalledHookEntry{
			"pre_tool_use": {{Paths: []string{
				filepath.Join(repoDir, ".claude", "hooks", "pre_tool_use", "gate.local.sh"),
				filepath.Join(repoDir, ".claude", "hooks", "pre_tool_use", "validate.local.sh"),
			}}},
		},
	}

	m := &SettingsMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file, got %d", len(written))
	}

	data, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatalf("reading settings file: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing JSON: %v", err)
	}

	hooksDoc := doc["hooks"].(map[string]any)
	entries := hooksDoc["PreToolUse"].([]any)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry for PreToolUse, got %d", len(entries))
	}
	entry := entries[0].(map[string]any)
	hooksList := entry["hooks"].([]any)
	if len(hooksList) != 2 {
		t.Fatalf("expected 2 hook commands for PreToolUse, got %d", len(hooksList))
	}
}

func TestSnakeToPascal(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"pre_tool_use", "PreToolUse"},
		{"stop", "Stop"},
		{"post_tool_use", "PostToolUse"},
		{"some_new_event", "SomeNewEvent"},
	}
	for _, tc := range tests {
		if got := snakeToPascal(tc.input); got != tc.want {
			t.Errorf("snakeToPascal(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestSettingsMaterializerInlineEnvOnly(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Claude: config.ClaudeConfig{
				Env: config.ClaudeEnvConfig{
					Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"GH_TOKEN": config.MaybeSecret{Plain: "ghp_test123"}, "API_TOKEN": config.MaybeSecret{Plain: "api_test456"}}},
				},
			},
		},
		RepoDir: repoDir,
	}

	m := &SettingsMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file written, got %d", len(written))
	}

	data, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatalf("reading settings file: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing settings JSON: %v", err)
	}

	envBlock, ok := doc["env"].(map[string]any)
	if !ok {
		t.Fatal("expected env key in output")
	}
	if envBlock["GH_TOKEN"] != "ghp_test123" {
		t.Errorf("GH_TOKEN = %v, want %q", envBlock["GH_TOKEN"], "ghp_test123")
	}
	if envBlock["API_TOKEN"] != "api_test456" {
		t.Errorf("API_TOKEN = %v, want %q", envBlock["API_TOKEN"], "api_test456")
	}
}

func TestSettingsMaterializerPromote(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	configDir := filepath.Join(tmpDir, "config")
	envDir := filepath.Join(configDir, "env")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write an env file with the token to promote.
	envFile := filepath.Join(envDir, "workspace.env")
	if err := os.WriteFile(envFile, []byte("GH_TOKEN=ghp_from_env\nOTHER=not_promoted\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Claude: config.ClaudeConfig{
				Env: config.ClaudeEnvConfig{
					Promote: []string{"GH_TOKEN"},
				},
			},
			Env: config.EnvConfig{
				Files: []string{"env/workspace.env"},
			},
		},
		RepoDir:   repoDir,
		ConfigDir: configDir,
		RepoName:  "testrepo",
	}

	m := &SettingsMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file, got %d", len(written))
	}

	data, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatalf("reading settings file: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing JSON: %v", err)
	}

	envBlock, ok := doc["env"].(map[string]any)
	if !ok {
		t.Fatal("expected env key")
	}
	if envBlock["GH_TOKEN"] != "ghp_from_env" {
		t.Errorf("GH_TOKEN = %v, want ghp_from_env", envBlock["GH_TOKEN"])
	}
	if _, ok := envBlock["OTHER"]; ok {
		t.Error("OTHER should not be in settings env (not promoted)")
	}
}

func TestSettingsMaterializerPromoteMissing(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Claude: config.ClaudeConfig{
				Env: config.ClaudeEnvConfig{
					Promote: []string{"MISSING_KEY"},
				},
			},
		},
		RepoDir:   repoDir,
		ConfigDir: configDir,
		RepoName:  "testrepo",
	}

	m := &SettingsMaterializer{}
	_, err := m.Materialize(ctx)
	if err == nil {
		t.Fatal("expected error for missing promoted key")
	}
	if !strings.Contains(err.Error(), "promoted key \"MISSING_KEY\" not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSettingsMaterializerPromoteInlineOverride(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	configDir := filepath.Join(tmpDir, "config")
	envDir := filepath.Join(configDir, "env")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(envDir, 0o755); err != nil {
		t.Fatal(err)
	}

	envFile := filepath.Join(envDir, "workspace.env")
	if err := os.WriteFile(envFile, []byte("GH_TOKEN=ghp_from_env\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Claude: config.ClaudeConfig{
				Env: config.ClaudeEnvConfig{
					Promote: []string{"GH_TOKEN"},
					Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"GH_TOKEN": config.MaybeSecret{Plain: "ghp_inline_wins"}}},
				},
			},
			Env: config.EnvConfig{
				Files: []string{"env/workspace.env"},
			},
		},
		RepoDir:   repoDir,
		ConfigDir: configDir,
		RepoName:  "testrepo",
	}

	m := &SettingsMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatalf("reading settings: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing JSON: %v", err)
	}

	envBlock := doc["env"].(map[string]any)
	if envBlock["GH_TOKEN"] != "ghp_inline_wins" {
		t.Errorf("GH_TOKEN = %v, want ghp_inline_wins (inline should win over promoted)", envBlock["GH_TOKEN"])
	}
}

func TestSettingsMaterializerAllBlocks(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Claude: config.ClaudeConfig{
				Settings: config.SettingsConfig{"permissions": config.MaybeSecret{Plain: "bypass"}},
				Env:      config.ClaudeEnvConfig{Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"GH_TOKEN": config.MaybeSecret{Plain: "ghp_test"}}}},
			},
		},
		RepoDir: repoDir,
		InstalledHooks: map[string][]InstalledHookEntry{
			"stop": {{Paths: []string{filepath.Join(repoDir, ".claude", "hooks", "stop", "continue.sh")}}},
		},
	}

	m := &SettingsMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file, got %d", len(written))
	}

	data, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatalf("reading settings file: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing JSON: %v", err)
	}

	if _, ok := doc["permissions"]; !ok {
		t.Error("expected permissions key")
	}
	if _, ok := doc["hooks"]; !ok {
		t.Error("expected hooks key")
	}
	envBlock, ok := doc["env"].(map[string]any)
	if !ok {
		t.Fatal("expected env key")
	}
	if envBlock["GH_TOKEN"] != "ghp_test" {
		t.Errorf("GH_TOKEN = %v, want %q", envBlock["GH_TOKEN"], "ghp_test")
	}
}

func TestEnvMaterializerName(t *testing.T) {
	m := &EnvMaterializer{}
	if got := m.Name(); got != "env" {
		t.Errorf("Name() = %q, want %q", got, "env")
	}
}

func TestEnvMaterializerNoopWhenEmpty(t *testing.T) {
	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Env: config.EnvConfig{},
		},
	}

	m := &EnvMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if written != nil {
		t.Errorf("expected nil, got %v", written)
	}
}

func TestEnvMaterializerExplicitFiles(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}

	envContent := "# comment\nFOO=bar\nBAZ=qux\n"
	if err := os.WriteFile(filepath.Join(configDir, "workspace.env"), []byte(envContent), 0o644); err != nil {
		t.Fatal(err)
	}

	repoDir := filepath.Join(tmpDir, "repos", "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Env: config.EnvConfig{
				Files: []string{"workspace.env"},
			},
		},
		RepoName:  "myrepo",
		RepoDir:   repoDir,
		ConfigDir: configDir,
	}

	m := &EnvMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file written, got %d", len(written))
	}

	expectedPath := filepath.Join(repoDir, ".local.env")
	if written[0] != expectedPath {
		t.Errorf("written[0] = %q, want %q", written[0], expectedPath)
	}

	data, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("reading env file: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "# Generated by niwa - do not edit manually") {
		t.Error("missing header comment")
	}
	if !strings.Contains(content, "FOO=bar\n") {
		t.Error("missing FOO=bar")
	}
	if !strings.Contains(content, "BAZ=qux\n") {
		t.Error("missing BAZ=qux")
	}
}

func TestEnvMaterializerInlineVarsOnly(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Env: config.EnvConfig{
				Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"KEY": config.MaybeSecret{Plain: "value"}}},
			},
		},
		RepoName:  "myrepo",
		RepoDir:   repoDir,
		ConfigDir: tmpDir,
	}

	m := &EnvMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file, got %d", len(written))
	}

	data, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatalf("reading env file: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "KEY=value\n") {
		t.Errorf("expected KEY=value, got:\n%s", content)
	}
}

func TestEnvMaterializerVarsOverrideFileVars(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}

	envContent := "FOO=from_file\nBAR=from_file\n"
	if err := os.WriteFile(filepath.Join(configDir, "base.env"), []byte(envContent), 0o644); err != nil {
		t.Fatal(err)
	}

	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Env: config.EnvConfig{
				Files: []string{"base.env"},
				Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"FOO": config.MaybeSecret{Plain: "from_vars"}}},
			},
		},
		RepoName:  "myrepo",
		RepoDir:   repoDir,
		ConfigDir: configDir,
	}

	m := &EnvMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatalf("reading env file: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "FOO=from_vars\n") {
		t.Errorf("expected FOO=from_vars (inline wins), got:\n%s", content)
	}
	if !strings.Contains(content, "BAR=from_file\n") {
		t.Errorf("expected BAR=from_file, got:\n%s", content)
	}
}

func TestEnvMaterializerDiscoveredWorkspaceFile(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// The discovered workspace file lives in configDir.
	discoveredPath := filepath.Join(configDir, "workspace.env")
	if err := os.WriteFile(discoveredPath, []byte("DISCOVERED=yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Env: config.EnvConfig{}, // No explicit files.
		},
		RepoName:  "myrepo",
		RepoDir:   repoDir,
		ConfigDir: configDir,
		DiscoveredEnv: &DiscoveredEnv{
			WorkspaceFile: "workspace.env",
		},
	}

	m := &EnvMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file, got %d", len(written))
	}

	data, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatalf("reading env file: %v", err)
	}

	if !strings.Contains(string(data), "DISCOVERED=yes\n") {
		t.Errorf("expected discovered var, got:\n%s", string(data))
	}
}

func TestEnvMaterializerDiscoveredWorkspaceFileIgnoredWhenExplicitFiles(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write both files.
	if err := os.WriteFile(filepath.Join(configDir, "explicit.env"), []byte("EXPLICIT=yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "workspace.env"), []byte("DISCOVERED=yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Env: config.EnvConfig{
				Files: []string{"explicit.env"}, // Explicit files present.
			},
		},
		RepoName:  "myrepo",
		RepoDir:   repoDir,
		ConfigDir: configDir,
		DiscoveredEnv: &DiscoveredEnv{
			WorkspaceFile: "workspace.env",
		},
	}

	m := &EnvMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatalf("reading env file: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "EXPLICIT=yes\n") {
		t.Error("expected EXPLICIT=yes from explicit file")
	}
	if strings.Contains(content, "DISCOVERED=yes") {
		t.Error("discovered workspace file should be ignored when explicit files are set")
	}
}

func TestEnvMaterializerRepoDiscoveredOverlay(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(configDir, "base.env"), []byte("FOO=base\nBAR=base\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write a repo-specific discovered env file (can be anywhere).
	repoEnvPath := filepath.Join(tmpDir, "repo-envs", "myrepo.env")
	if err := os.MkdirAll(filepath.Dir(repoEnvPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(repoEnvPath, []byte("FOO=repo_override\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Env: config.EnvConfig{
				Files: []string{"base.env"},
			},
		},
		RepoName:  "myrepo",
		RepoDir:   repoDir,
		ConfigDir: configDir,
		DiscoveredEnv: &DiscoveredEnv{
			RepoFiles: map[string]string{"myrepo": repoEnvPath},
		},
	}

	m := &EnvMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatalf("reading env file: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "FOO=repo_override\n") {
		t.Errorf("expected FOO=repo_override (repo discovered wins), got:\n%s", content)
	}
	if !strings.Contains(content, "BAR=base\n") {
		t.Errorf("expected BAR=base, got:\n%s", content)
	}
}

func TestEnvMaterializerContainmentReject(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write a file outside configDir.
	outsideDir := filepath.Join(tmpDir, "outside")
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "evil.env"), []byte("SECRET=bad\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Env: config.EnvConfig{
				Files: []string{"../outside/evil.env"},
			},
		},
		RepoName:  "myrepo",
		RepoDir:   repoDir,
		ConfigDir: configDir,
	}

	m := &EnvMaterializer{}
	_, err := m.Materialize(ctx)
	if err == nil {
		t.Fatal("expected error for path escaping configDir, got nil")
	}
}

func TestEnvMaterializerMissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}

	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Env: config.EnvConfig{
				Files: []string{"nonexistent.env"},
			},
		},
		RepoName:  "myrepo",
		RepoDir:   repoDir,
		ConfigDir: configDir,
	}

	m := &EnvMaterializer{}
	_, err := m.Materialize(ctx)
	if err == nil {
		t.Fatal("expected error for missing env file, got nil")
	}
}

func TestEnvMaterializerSkipsCommentsAndBlanks(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}

	envContent := "# comment line\n\nFOO=bar\n# another comment\nBAZ=qux\n\n"
	if err := os.WriteFile(filepath.Join(configDir, "test.env"), []byte(envContent), 0o644); err != nil {
		t.Fatal(err)
	}

	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Env: config.EnvConfig{
				Files: []string{"test.env"},
			},
		},
		RepoName:  "myrepo",
		RepoDir:   repoDir,
		ConfigDir: configDir,
	}

	m := &EnvMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatalf("reading env file: %v", err)
	}

	content := string(data)
	// Should have the header, FOO, and BAZ only.
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) != 3 { // header + FOO + BAZ
		t.Errorf("expected 3 lines (header + 2 vars), got %d: %v", len(lines), lines)
	}
}

func TestEnvMaterializerSortedKeys(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Env: config.EnvConfig{
				Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"ZEBRA": config.MaybeSecret{Plain: "z"}, "ALPHA": config.MaybeSecret{Plain: "a"}, "MIKE": config.MaybeSecret{Plain: "m"}}},
			},
		},
		RepoName:  "myrepo",
		RepoDir:   repoDir,
		ConfigDir: tmpDir,
	}

	m := &EnvMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatalf("reading env file: %v", err)
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	// lines[0] is header, lines[1..3] are vars
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d: %v", len(lines), lines)
	}
	if lines[1] != "ALPHA=a" {
		t.Errorf("line 1 = %q, want %q", lines[1], "ALPHA=a")
	}
	if lines[2] != "MIKE=m" {
		t.Errorf("line 2 = %q, want %q", lines[2], "MIKE=m")
	}
	if lines[3] != "ZEBRA=z" {
		t.Errorf("line 3 = %q, want %q", lines[3], "ZEBRA=z")
	}
}

// --- localRename tests ---

func TestLocalRename(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"design.md", "design.local.md"},
		{"config.json", "config.local.json"},
		{"script.sh", "script.local.sh"},
		{"Makefile", "Makefile.local"},
		{".eslintrc", ".eslintrc.local"},
		{"archive.tar.gz", "archive.tar.local.gz"},
	}
	for _, tc := range tests {
		if got := localRename(tc.input); got != tc.want {
			t.Errorf("localRename(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// --- FilesMaterializer tests ---

func TestFilesMaterializerName(t *testing.T) {
	m := &FilesMaterializer{}
	if got := m.Name(); got != "files" {
		t.Errorf("Name() = %q, want %q", got, "files")
	}
}

func TestFilesMaterializerNoop(t *testing.T) {
	ctx := &MaterializeContext{
		Effective: EffectiveConfig{},
	}
	m := &FilesMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if written != nil {
		t.Errorf("expected nil, got %v", written)
	}
}

func TestFilesMaterializerSingleFileDirDest(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	repoDir := filepath.Join(tmpDir, "repo")
	os.MkdirAll(filepath.Join(configDir, "extensions"), 0o755)
	os.MkdirAll(repoDir, 0o755)

	os.WriteFile(filepath.Join(configDir, "extensions", "design.md"), []byte("# Design"), 0o644)

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Files: map[string]string{
				"extensions/design.md": ".claude/shirabe-extensions/",
			},
		},
		ConfigDir: configDir,
		RepoDir:   repoDir,
	}

	m := &FilesMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file, got %d", len(written))
	}

	expectedPath := filepath.Join(repoDir, ".claude", "shirabe-extensions", "design.local.md")
	if written[0] != expectedPath {
		t.Errorf("written = %q, want %q", written[0], expectedPath)
	}

	data, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	if string(data) != "# Design" {
		t.Errorf("content = %q, want %q", string(data), "# Design")
	}
}

func TestFilesMaterializerExplicitDest(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	repoDir := filepath.Join(tmpDir, "repo")
	os.MkdirAll(configDir, 0o755)
	os.MkdirAll(repoDir, 0o755)

	os.WriteFile(filepath.Join(configDir, "myconfig.json"), []byte(`{"key": "val"}`), 0o644)

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Files: map[string]string{
				// Destination lacks ".local"; the materializer injects it
				// before the extension so every written file matches the
				// *.local* gitignore pattern.
				"myconfig.json": ".tool/config.json",
			},
		},
		ConfigDir: configDir,
		RepoDir:   repoDir,
	}

	m := &FilesMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedPath := filepath.Join(repoDir, ".tool", "config.local.json")
	if written[0] != expectedPath {
		t.Errorf("written = %q, want %q", written[0], expectedPath)
	}
}

func TestFilesMaterializerPathTraversal(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	repoDir := filepath.Join(tmpDir, "repo")
	os.MkdirAll(configDir, 0o755)
	os.MkdirAll(repoDir, 0o755)

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Files: map[string]string{
				"../../../etc/passwd": ".claude/",
			},
		},
		ConfigDir: configDir,
		RepoDir:   repoDir,
	}

	m := &FilesMaterializer{}
	_, err := m.Materialize(ctx)
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestFilesMaterializerDirSource(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	repoDir := filepath.Join(tmpDir, "repo")
	os.MkdirAll(filepath.Join(configDir, "commands", "sub"), 0o755)
	os.MkdirAll(repoDir, 0o755)

	os.WriteFile(filepath.Join(configDir, "commands", "hello.sh"), []byte("#!/bin/bash"), 0o644)
	os.WriteFile(filepath.Join(configDir, "commands", "sub", "nested.md"), []byte("# Nested"), 0o644)

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Files: map[string]string{
				"commands/": ".claude/commands/",
			},
		},
		ConfigDir: configDir,
		RepoDir:   repoDir,
	}

	m := &FilesMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 2 {
		t.Fatalf("expected 2 files, got %d: %v", len(written), written)
	}

	// Check .local renaming on directory files.
	expected := map[string]bool{
		filepath.Join(repoDir, ".claude", "commands", "hello.local.sh"):       true,
		filepath.Join(repoDir, ".claude", "commands", "sub", "nested.local.md"): true,
	}
	for _, w := range written {
		if !expected[w] {
			t.Errorf("unexpected written file: %q", w)
		}
	}
}

func TestFilesMaterializerEmptyValueSkipped(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	repoDir := filepath.Join(tmpDir, "repo")
	os.MkdirAll(configDir, 0o755)
	os.MkdirAll(repoDir, 0o755)

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Files: map[string]string{
				"removed.md": "",
			},
		},
		ConfigDir: configDir,
		RepoDir:   repoDir,
	}

	m := &FilesMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("expected 0 files, got %d", len(written))
	}
}

// --- buildSettingsDoc tests ---

func TestBuildSettingsDocPlugins(t *testing.T) {
	doc, err := buildSettingsDoc(BuildSettingsConfig{
		Plugins: []string{"plugin-a@marketplace", "plugin-b@marketplace"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	plugins, ok := doc["enabledPlugins"].(map[string]any)
	if !ok {
		t.Fatal("expected enabledPlugins key in output")
	}
	if plugins["plugin-a@marketplace"] != true {
		t.Error("expected plugin-a@marketplace = true")
	}
	if plugins["plugin-b@marketplace"] != true {
		t.Error("expected plugin-b@marketplace = true")
	}
}

func TestBuildSettingsDocEmptyPlugins(t *testing.T) {
	doc, err := buildSettingsDoc(BuildSettingsConfig{
		Settings: config.SettingsConfig{"permissions": config.MaybeSecret{Plain: "bypass"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := doc["enabledPlugins"]; ok {
		t.Error("enabledPlugins should not be present when plugins list is empty")
	}
}

func TestBuildSettingsDocGitHubMarketplace(t *testing.T) {
	doc, err := buildSettingsDoc(BuildSettingsConfig{
		Marketplaces: []string{"tsukumogami/shirabe"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mkts, ok := doc["extraKnownMarketplaces"].(map[string]any)
	if !ok {
		t.Fatal("expected extraKnownMarketplaces key")
	}

	entry, ok := mkts["shirabe"].(map[string]any)
	if !ok {
		t.Fatal("expected shirabe entry")
	}

	source := entry["source"].(map[string]any)
	if source["source"] != "github" {
		t.Errorf("source type = %v, want github", source["source"])
	}
	if source["repo"] != "tsukumogami/shirabe" {
		t.Errorf("repo = %v, want tsukumogami/shirabe", source["repo"])
	}
}

func TestBuildSettingsDocRepoMarketplace(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "tools")
	pluginDir := filepath.Join(repoDir, ".claude-plugin")
	os.MkdirAll(pluginDir, 0o755)
	os.WriteFile(filepath.Join(pluginDir, "marketplace.json"), []byte("{}"), 0o644)

	repoIndex := map[string]string{"tools": repoDir}

	doc, err := buildSettingsDoc(BuildSettingsConfig{
		Marketplaces: []string{"repo:tools/.claude-plugin/marketplace.json"},
		RepoIndex:    repoIndex,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mkts, ok := doc["extraKnownMarketplaces"].(map[string]any)
	if !ok {
		t.Fatal("expected extraKnownMarketplaces key")
	}

	entry, ok := mkts["tools"].(map[string]any)
	if !ok {
		t.Fatal("expected tools entry")
	}

	source := entry["source"].(map[string]any)
	if source["source"] != "directory" {
		t.Errorf("source type = %v, want directory", source["source"])
	}
	if source["path"] != repoDir {
		t.Errorf("path = %v, want %v", source["path"], repoDir)
	}
}

func TestBuildSettingsDocRepoMarketplaceMissingRepo(t *testing.T) {
	repoIndex := map[string]string{"other": "/tmp/other"}

	_, err := buildSettingsDoc(BuildSettingsConfig{
		Marketplaces: []string{"repo:tools/.claude-plugin/marketplace.json"},
		RepoIndex:    repoIndex,
	})
	if err == nil {
		t.Fatal("expected error for missing repo in index")
	}
	if !strings.Contains(err.Error(), "not managed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBuildSettingsDocIncludeGitInstructions(t *testing.T) {
	f := false
	doc, err := buildSettingsDoc(BuildSettingsConfig{
		Settings:               config.SettingsConfig{"permissions": config.MaybeSecret{Plain: "bypass"}},
		IncludeGitInstructions: &f,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	val, ok := doc["includeGitInstructions"]
	if !ok {
		t.Fatal("expected includeGitInstructions key")
	}
	if val != false {
		t.Errorf("includeGitInstructions = %v, want false", val)
	}
}

func TestBuildSettingsDocNoIncludeGitInstructionsWhenNil(t *testing.T) {
	doc, err := buildSettingsDoc(BuildSettingsConfig{
		Settings: config.SettingsConfig{"permissions": config.MaybeSecret{Plain: "bypass"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := doc["includeGitInstructions"]; ok {
		t.Error("includeGitInstructions should not be present when nil")
	}
}

func TestSettingsMaterializerWithPlugins(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	os.MkdirAll(repoDir, 0o755)

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Claude: config.ClaudeConfig{
				Settings: config.SettingsConfig{"permissions": config.MaybeSecret{Plain: "bypass"}},
			},
			Plugins: []string{"my-plugin@marketplace"},
		},
		RepoDir: repoDir,
	}

	m := &SettingsMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file, got %d", len(written))
	}

	data, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatalf("reading settings: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing JSON: %v", err)
	}

	plugins := doc["enabledPlugins"].(map[string]any)
	if plugins["my-plugin@marketplace"] != true {
		t.Error("expected my-plugin@marketplace = true")
	}
}

func TestSettingsMaterializerNoopWithEmptyPlugins(t *testing.T) {
	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Claude:  config.ClaudeConfig{},
			Plugins: []string{},
		},
	}

	m := &SettingsMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if written != nil {
		t.Errorf("expected nil, got %v", written)
	}
}

func TestSettingsMaterializerPluginsOnlyTriggersWrite(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	os.MkdirAll(repoDir, 0o755)

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Claude:  config.ClaudeConfig{},
			Plugins: []string{"my-plugin@marketplace"},
		},
		RepoDir: repoDir,
	}

	m := &SettingsMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file, got %d", len(written))
	}

	data, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatalf("reading settings: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing JSON: %v", err)
	}

	if _, ok := doc["enabledPlugins"]; !ok {
		t.Error("expected enabledPlugins key in output")
	}
}

// --- Issue 6: materialization hardening (0o600, .local infix) ---

// TestEnvMaterializerWritesMode0600 asserts the env materializer
// writes .local.env with 0o600 permissions regardless of whether any
// secret backs the file. This covers both the vault and non-vault
// cases: the mode change is strictly safer for non-vault users too.
func TestEnvMaterializerWritesMode0600(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Env: config.EnvConfig{
				Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{
					"KEY": {Plain: "value"},
				}},
			},
		},
		RepoDir:   repoDir,
		ConfigDir: tmpDir,
	}

	m := &EnvMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file, got %d", len(written))
	}

	info, err := os.Stat(written[0])
	if err != nil {
		t.Fatalf("stat env file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("env file mode = %o, want 0o600", got)
	}
}

// TestSettingsMaterializerWritesMode0600 asserts the settings
// materializer writes .claude/settings.local.json with 0o600
// permissions. Applies regardless of whether settings carry secrets.
func TestSettingsMaterializerWritesMode0600(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Claude: config.ClaudeConfig{
				Settings: config.SettingsConfig{"permissions": config.MaybeSecret{Plain: "bypass"}},
			},
		},
		RepoDir: repoDir,
	}

	m := &SettingsMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file, got %d", len(written))
	}

	info, err := os.Stat(written[0])
	if err != nil {
		t.Fatalf("stat settings file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("settings file mode = %o, want 0o600", got)
	}
}

// TestFilesMaterializerWritesMode0600 asserts the files materializer
// writes with 0o600 even when nothing in the source content looks
// sensitive. The 0o600 invariant is unconditional.
func TestFilesMaterializerWritesMode0600(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "settings.json"), []byte(`{"k":"v"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Files: map[string]string{
				"settings.json": ".tool/settings.json",
			},
		},
		ConfigDir: configDir,
		RepoDir:   repoDir,
	}

	m := &FilesMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file, got %d", len(written))
	}

	info, err := os.Stat(written[0])
	if err != nil {
		t.Fatalf("stat materialized file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("files materializer mode = %o, want 0o600", got)
	}
}

// TestFilesMaterializerInjectsLocalInfix covers the [files] entry
// whose destination lacks ".local". The materializer injects the
// infix before the extension so the output matches the *.local*
// gitignore pattern.
func TestFilesMaterializerInjectsLocalInfix(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "foo.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Files: map[string]string{
				"foo.json": ".config/foo.json",
			},
		},
		ConfigDir: configDir,
		RepoDir:   repoDir,
	}

	m := &FilesMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file, got %d", len(written))
	}

	want := filepath.Join(repoDir, ".config", "foo.local.json")
	if written[0] != want {
		t.Errorf("written = %q, want %q (.local injected)", written[0], want)
	}
}

// TestFilesMaterializerPreservesExistingLocal covers the case where
// the user-written destination already contains ".local". The
// materializer must not double-inject.
func TestFilesMaterializerPreservesExistingLocal(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "foo.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Files: map[string]string{
				"foo.json": ".config/foo.local.json",
			},
		},
		ConfigDir: configDir,
		RepoDir:   repoDir,
	}

	m := &FilesMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := filepath.Join(repoDir, ".config", "foo.local.json")
	if written[0] != want {
		t.Errorf("written = %q, want %q (no double infix)", written[0], want)
	}
}

// TestEnvMaterializerRevealsResolvedSecret ensures the env
// materializer writes the plaintext bytes of a resolved secret into
// the output file (via reveal.UnsafeReveal). Before Issue 6 the
// materializer consumed MaybeSecret.String, which would redact to
// "***"; the integration-style coverage lives under TestApplyResolvesVaultSecretEndToEnd.
func TestEnvMaterializerRevealsResolvedSecret(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	plaintext := "super-secret-token-value"
	v := secret.New([]byte(plaintext), secret.Origin{Key: "API_TOKEN"})

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Env: config.EnvConfig{
				Secrets: config.EnvVarsTable{Values: map[string]config.MaybeSecret{
					"API_TOKEN": {Secret: v},
				}},
			},
		},
		RepoDir:   repoDir,
		ConfigDir: tmpDir,
	}

	m := &EnvMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file, got %d", len(written))
	}

	data, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatalf("reading env file: %v", err)
	}
	content := string(data)
	want := "API_TOKEN=" + plaintext + "\n"
	if !strings.Contains(content, want) {
		t.Errorf("env file should contain %q, got:\n%s", want, content)
	}
	if strings.Contains(content, "***") {
		t.Errorf("env file must not contain redacted placeholder, got:\n%s", content)
	}
}

// TestSettingsMaterializerRevealsResolvedEnvSecret asserts promoted
// secrets land as plaintext in .claude/settings.local.json after the
// resolver has populated MaybeSecret.Secret.
func TestSettingsMaterializerRevealsResolvedEnvSecret(t *testing.T) {
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	plaintext := "inline-secret-value"
	v := secret.New([]byte(plaintext), secret.Origin{Key: "GH_TOKEN"})

	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Claude: config.ClaudeConfig{
				Env: config.ClaudeEnvConfig{
					Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{
						"GH_TOKEN": {Secret: v},
					}},
				},
			},
		},
		RepoDir: repoDir,
	}

	m := &SettingsMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file, got %d", len(written))
	}

	data, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatalf("reading settings file: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing settings JSON: %v", err)
	}
	envBlock, ok := doc["env"].(map[string]any)
	if !ok {
		t.Fatal("expected env key in settings doc")
	}
	if envBlock["GH_TOKEN"] != plaintext {
		t.Errorf("GH_TOKEN = %v, want %q", envBlock["GH_TOKEN"], plaintext)
	}
}
