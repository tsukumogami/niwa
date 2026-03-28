package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
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
					"pre_tool_use": {"hooks/pre_tool_use/check.sh"},
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

	expectedTarget := filepath.Join(repoDir, ".claude", "hooks", "pre_tool_use", "check.sh")
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
	paths, ok := ctx.InstalledHooks["pre_tool_use"]
	if !ok {
		t.Fatal("InstalledHooks should have pre_tool_use key")
	}
	if len(paths) != 1 || paths[0] != expectedTarget {
		t.Errorf("InstalledHooks[pre_tool_use] = %v, want [%s]", paths, expectedTarget)
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
					"pre_tool_use": {"hooks/pre_tool_use/run.sh"},
					"stop":         {"hooks/stop/run.sh"},
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
					"pre_tool_use": {
						"hooks/pre_tool_use/check.sh",
						"hooks/pre_tool_use/validate.sh",
					},
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

	paths := ctx.InstalledHooks["pre_tool_use"]
	if len(paths) != 2 {
		t.Errorf("expected 2 scripts for pre_tool_use, got %d", len(paths))
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
					"pre_tool_use": {"../outside/evil.sh"},
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
					"pre_tool_use": {"hooks/nonexistent.sh"},
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
				Settings: config.SettingsConfig{"permissions": "bypass"},
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
				Settings: config.SettingsConfig{"permissions": "ask"},
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
				Settings: config.SettingsConfig{"permissions": "invalid"},
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
		InstalledHooks: map[string][]string{
			"pre_tool_use": {filepath.Join(repoDir, ".claude", "hooks", "pre_tool_use", "gate.sh")},
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
	if entry["type"] != "command" {
		t.Errorf("type = %v, want %q", entry["type"], "command")
	}
	if entry["command"] != ".claude/hooks/pre_tool_use/gate.sh" {
		t.Errorf("command = %v, want %q", entry["command"], ".claude/hooks/pre_tool_use/gate.sh")
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
				Settings: config.SettingsConfig{"permissions": "bypass"},
			},
		},
		RepoDir: repoDir,
		InstalledHooks: map[string][]string{
			"pre_tool_use": {filepath.Join(repoDir, ".claude", "hooks", "pre_tool_use", "gate.sh")},
			"stop":         {filepath.Join(repoDir, ".claude", "hooks", "stop", "stop.sh")},
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
		{"PreToolUse", ".claude/hooks/pre_tool_use/gate.sh"},
		{"Stop", ".claude/hooks/stop/stop.sh"},
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
		if entry["command"] != tc.command {
			t.Errorf("%s command = %v, want %q", tc.event, entry["command"], tc.command)
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
		InstalledHooks: map[string][]string{
			"pre_tool_use": {
				filepath.Join(repoDir, ".claude", "hooks", "pre_tool_use", "gate.sh"),
				filepath.Join(repoDir, ".claude", "hooks", "pre_tool_use", "validate.sh"),
			},
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
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries for PreToolUse, got %d", len(entries))
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
