package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

func TestInstallChannelInfrastructure_EmptyConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.WorkspaceConfig{}
	var written []string
	if err := InstallChannelInfrastructure(cfg, dir, &written); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("expected no files written for empty config, got %v", written)
	}
	// sessions dir should not exist.
	if _, err := os.Stat(filepath.Join(dir, ".niwa", "sessions")); err == nil {
		t.Errorf("sessions dir should not be created for empty channels config")
	}
}

func TestInstallChannelInfrastructure_CreatesArtifacts(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test-ws"},
		Channels: config.ChannelsConfig{
			Mesh: config.ChannelsMeshConfig{
				Roles: map[string]string{"coordinator": "", "worker": "tools/worker"},
			},
		},
	}

	// Write a minimal workspace-context.md so the append step has content to work with.
	ctxPath := filepath.Join(dir, workspaceContextFile)
	if err := os.WriteFile(ctxPath, []byte("# Workspace: test-ws\n\nSome content.\n"), 0o644); err != nil {
		t.Fatalf("writing context file: %v", err)
	}

	var written []string
	if err := InstallChannelInfrastructure(cfg, dir, &written); err != nil {
		t.Fatalf("InstallChannelInfrastructure: %v", err)
	}

	// sessions/ must exist with 0700.
	sessionsDir := filepath.Join(dir, ".niwa", "sessions")
	info, err := os.Stat(sessionsDir)
	if err != nil {
		t.Fatalf("sessions dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("sessions path is not a directory")
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("sessions dir mode: got %o, want 0700", info.Mode().Perm())
	}

	// sessions.json must exist with [] content and 0600 mode.
	sessionsJSON := filepath.Join(sessionsDir, "sessions.json")
	fi, err := os.Stat(sessionsJSON)
	if err != nil {
		t.Fatalf("sessions.json not created: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("sessions.json mode: got %o, want 0600", fi.Mode().Perm())
	}
	data, _ := os.ReadFile(sessionsJSON)
	content := strings.TrimSpace(string(data))
	if content != `{"sessions":[]}` {
		t.Errorf("sessions.json content: got %q, want {\"sessions\":[]}", content)
	}

	// .claude/.mcp.json must exist with 0600 mode and contain niwa mcp-serve.
	mcpJSON := filepath.Join(dir, ".claude", ".mcp.json")
	mfi, err := os.Stat(mcpJSON)
	if err != nil {
		t.Fatalf(".mcp.json not created: %v", err)
	}
	if mfi.Mode().Perm() != 0o600 {
		t.Errorf(".mcp.json mode: got %o, want 0600", mfi.Mode().Perm())
	}
	mcpData, _ := os.ReadFile(mcpJSON)
	mcpStr := string(mcpData)
	if !strings.Contains(mcpStr, "mcp-serve") {
		t.Errorf(".mcp.json does not contain mcp-serve: %s", mcpStr)
	}
	if !strings.Contains(mcpStr, dir) {
		t.Errorf(".mcp.json does not contain instanceRoot %q: %s", dir, mcpStr)
	}
	// Verify it's valid JSON.
	var mcpDoc map[string]any
	if err := json.Unmarshal(mcpData, &mcpDoc); err != nil {
		t.Errorf(".mcp.json is not valid JSON: %v\ncontent: %s", err, mcpStr)
	}

	// workspace-context.md must contain ## Channels.
	ctxData, _ := os.ReadFile(ctxPath)
	if !strings.Contains(string(ctxData), channelsSectionHeader) {
		t.Errorf("workspace-context.md missing ## Channels section\ncontent:\n%s", string(ctxData))
	}

	// sessions.json should appear in writtenFiles (created fresh).
	foundSessions := false
	foundMCP := false
	for _, f := range written {
		if f == sessionsJSON {
			foundSessions = true
		}
		if f == mcpJSON {
			foundMCP = true
		}
	}
	if !foundSessions {
		t.Errorf("sessions.json not in writtenFiles: %v", written)
	}
	if !foundMCP {
		t.Errorf(".mcp.json not in writtenFiles: %v", written)
	}
}

func TestInstallChannelInfrastructure_Idempotent(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "test-ws"},
		Channels: config.ChannelsConfig{
			Mesh: config.ChannelsMeshConfig{
				Roles: map[string]string{"coordinator": ""},
			},
		},
	}

	ctxPath := filepath.Join(dir, workspaceContextFile)
	if err := os.WriteFile(ctxPath, []byte("# Workspace: test-ws\n"), 0o644); err != nil {
		t.Fatalf("writing context file: %v", err)
	}

	// First apply.
	var written1 []string
	if err := InstallChannelInfrastructure(cfg, dir, &written1); err != nil {
		t.Fatalf("first apply: %v", err)
	}

	// Read the sessions.json and .mcp.json after first apply.
	sessionsJSON := filepath.Join(dir, ".niwa", "sessions", "sessions.json")
	mcpJSON := filepath.Join(dir, ".claude", ".mcp.json")

	sessionsData1, _ := os.ReadFile(sessionsJSON)
	mcpData1, _ := os.ReadFile(mcpJSON)
	ctx1, _ := os.ReadFile(ctxPath)

	// Second apply.
	var written2 []string
	if err := InstallChannelInfrastructure(cfg, dir, &written2); err != nil {
		t.Fatalf("second apply: %v", err)
	}

	sessionsData2, _ := os.ReadFile(sessionsJSON)
	mcpData2, _ := os.ReadFile(mcpJSON)
	ctx2, _ := os.ReadFile(ctxPath)

	// sessions.json must not change (not overwritten on second apply).
	if string(sessionsData1) != string(sessionsData2) {
		t.Errorf("sessions.json changed on second apply:\nbefore: %q\nafter:  %q",
			sessionsData1, sessionsData2)
	}
	// .mcp.json is always overwritten (overwrite-safe), but content is identical.
	if string(mcpData1) != string(mcpData2) {
		t.Errorf(".mcp.json content changed:\nbefore: %s\nafter:  %s", mcpData1, mcpData2)
	}
	// workspace-context.md must not duplicate the ## Channels section.
	channelCount := strings.Count(string(ctx2), channelsSectionHeader)
	if channelCount != 1 {
		t.Errorf("## Channels appears %d times in workspace-context.md (want 1):\n%s",
			channelCount, string(ctx2))
	}
	// First ctx and second ctx must be the same.
	if string(ctx1) != string(ctx2) {
		t.Errorf("workspace-context.md changed on second apply:\nbefore:\n%s\nafter:\n%s", ctx1, ctx2)
	}

	// sessions.json must always appear in writtenFiles so cleanRemovedFiles does
	// not delete it on re-apply (even though the file is not re-written when present).
	foundSessions2 := false
	for _, f := range written2 {
		if f == sessionsJSON {
			foundSessions2 = true
		}
	}
	if !foundSessions2 {
		t.Errorf("sessions.json not in writtenFiles on second apply (must remain managed to prevent cleanup)")
	}
}

func TestInstallChannelInfrastructure_SessionsJSONNotOverwrittenWhenPresent(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.WorkspaceConfig{
		Channels: config.ChannelsConfig{
			Mesh: config.ChannelsMeshConfig{
				Roles: map[string]string{"coordinator": ""},
			},
		},
	}

	// Pre-create sessions dir and sessions.json with real data.
	sessionsDir := filepath.Join(dir, ".niwa", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sessionsJSON := filepath.Join(sessionsDir, "sessions.json")
	existingData := `[{"id":"abc","role":"coordinator"}]`
	if err := os.WriteFile(sessionsJSON, []byte(existingData), 0o600); err != nil {
		t.Fatal(err)
	}

	// Write a minimal context file.
	ctxPath := filepath.Join(dir, workspaceContextFile)
	if err := os.WriteFile(ctxPath, []byte("# Workspace\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var written []string
	if err := InstallChannelInfrastructure(cfg, dir, &written); err != nil {
		t.Fatalf("InstallChannelInfrastructure: %v", err)
	}

	// sessions.json must still hold the original data.
	data, _ := os.ReadFile(sessionsJSON)
	if string(data) != existingData {
		t.Errorf("sessions.json was overwritten: got %q, want %q", string(data), existingData)
	}
}

func TestInjectChannelHooks_EmptyConfig(t *testing.T) {
	cfg := &config.WorkspaceConfig{}
	injectChannelHooks(cfg, t.TempDir())
	if len(cfg.Claude.Hooks) != 0 {
		t.Errorf("expected no hooks injected for empty channels config, got %v", cfg.Claude.Hooks)
	}
}

func TestInjectChannelHooks_InjectsHooks(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.WorkspaceConfig{
		Channels: config.ChannelsConfig{
			Mesh: config.ChannelsMeshConfig{
				Roles: map[string]string{"coordinator": ""},
			},
		},
	}
	injectChannelHooks(cfg, dir)

	if _, ok := cfg.Claude.Hooks["session_start"]; !ok {
		t.Error("session_start hook not injected")
	}
	if _, ok := cfg.Claude.Hooks["user_prompt_submit"]; !ok {
		t.Error("user_prompt_submit hook not injected")
	}

	// Scripts must be file paths into .niwa/hooks/, not bare CLI commands.
	startScripts := cfg.Claude.Hooks["session_start"][0].Scripts
	if len(startScripts) == 0 || !filepath.IsAbs(startScripts[0]) {
		t.Errorf("session_start script must be an absolute file path, got %v", startScripts)
	}
	if !strings.Contains(startScripts[0], "mesh-session-start.sh") {
		t.Errorf("session_start script must reference mesh-session-start.sh, got %v", startScripts)
	}
}

func TestInjectChannelHooks_PrependToExisting(t *testing.T) {
	dir := t.TempDir()
	existingEntry := config.HookEntry{Scripts: []string{"existing.sh"}}
	cfg := &config.WorkspaceConfig{
		Claude: config.ClaudeConfig{
			Hooks: config.HooksConfig{
				"session_start": {existingEntry},
			},
		},
		Channels: config.ChannelsConfig{
			Mesh: config.ChannelsMeshConfig{
				Roles: map[string]string{"coordinator": ""},
			},
		},
	}
	injectChannelHooks(cfg, dir)

	entries := cfg.Claude.Hooks["session_start"]
	if len(entries) != 2 {
		t.Fatalf("expected 2 session_start entries, got %d", len(entries))
	}
	// Channel hook must come first and reference the real script file path.
	wantScript := filepath.Join(dir, ".niwa", "hooks", "mesh-session-start.sh")
	if entries[0].Scripts[0] != wantScript {
		t.Errorf("channel hook script: got %q, want %q", entries[0].Scripts[0], wantScript)
	}
	if entries[1].Scripts[0] != "existing.sh" {
		t.Errorf("existing hook not preserved: second entry scripts %v", entries[1].Scripts)
	}
}

func TestBuildMCPJSON(t *testing.T) {
	instanceRoot := "/some/path/with spaces"
	data, err := buildMCPJSON(instanceRoot)
	if err != nil {
		t.Fatalf("buildMCPJSON: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("invalid JSON: %v\ncontent: %s", err, string(data))
	}

	// Verify the structure.
	servers, ok := doc["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("missing mcpServers key")
	}
	niwa, ok := servers["niwa"].(map[string]any)
	if !ok {
		t.Fatalf("missing mcpServers.niwa key")
	}
	cmd, _ := niwa["command"].(string)
	if cmd != "niwa" {
		t.Errorf("command: got %q, want niwa", cmd)
	}
	env, ok := niwa["env"].(map[string]any)
	if !ok {
		t.Fatalf("missing env block")
	}
	got, _ := env["NIWA_INSTANCE_ROOT"].(string)
	if got != instanceRoot {
		t.Errorf("NIWA_INSTANCE_ROOT: got %q, want %q", got, instanceRoot)
	}
}

func TestWriteFileMode_SetsPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	data := []byte("hello\n")
	if err := writeFileMode(path, data, 0o600); err != nil {
		t.Fatalf("writeFileMode: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode: got %o, want 0600", fi.Mode().Perm())
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(data) {
		t.Errorf("content: got %q, want %q", string(got), string(data))
	}
}
