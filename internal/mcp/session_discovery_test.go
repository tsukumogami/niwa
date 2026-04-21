package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverClaudeSessionID_Tier1_EnvVar(t *testing.T) {
	t.Setenv("CLAUDE_SESSION_ID", "valid-session-id-abc1")

	got := DiscoverClaudeSessionID(t.TempDir(), t.TempDir())
	if got != "valid-session-id-abc1" {
		t.Errorf("got %q, want %q", got, "valid-session-id-abc1")
	}
}

func TestDiscoverClaudeSessionID_Tier1_InvalidEnvVar_FallsThrough(t *testing.T) {
	t.Setenv("CLAUDE_SESSION_ID", "bad!")

	// Should fall through all tiers and return "" since no fixtures exist.
	got := DiscoverClaudeSessionID(t.TempDir(), t.TempDir())
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestDiscoverClaudeSessionID_Tier2_PPIDWalk(t *testing.T) {
	homeDir := t.TempDir()
	cwd := t.TempDir()

	// The test process's PPID is what DiscoverClaudeSessionID will look up
	// at the first iteration of the walk: pid = os.Getpid() → ppid = os.Getppid().
	ppid := os.Getppid()
	sessionsDir := filepath.Join(homeDir, ".claude", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	sf := claudeSessionFile{SessionID: "tier2-session-id-abcdef", CWD: cwd}
	data, _ := json.Marshal(sf)
	path := filepath.Join(sessionsDir, fmt.Sprintf("%d.json", ppid))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	got := DiscoverClaudeSessionID(homeDir, cwd)
	if got != "tier2-session-id-abcdef" {
		t.Errorf("got %q, want %q", got, "tier2-session-id-abcdef")
	}
}

func TestDiscoverClaudeSessionID_Tier2_CwdMismatch_FallsThrough(t *testing.T) {
	homeDir := t.TempDir()
	cwd := t.TempDir()

	ppid := os.Getppid()
	sessionsDir := filepath.Join(homeDir, ".claude", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write file with wrong cwd.
	sf := claudeSessionFile{SessionID: "tier2-session-id-abcdef", CWD: "/wrong/path"}
	data, _ := json.Marshal(sf)
	path := filepath.Join(sessionsDir, fmt.Sprintf("%d.json", ppid))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	// No tier 3 fixtures, so should return "".
	got := DiscoverClaudeSessionID(homeDir, cwd)
	if got != "" {
		t.Errorf("expected empty (cwd mismatch), got %q", got)
	}
}

func TestDiscoverClaudeSessionID_Tier3_ProjectScan(t *testing.T) {
	homeDir := t.TempDir()
	cwd := t.TempDir()

	encoded := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(cwd))
	projectDir := filepath.Join(homeDir, ".claude", "projects", encoded)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	sessionID := "tier3sessionidabcdef"
	jsonlPath := filepath.Join(projectDir, sessionID+".jsonl")
	if err := os.WriteFile(jsonlPath, []byte(`{"event":"test"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	got := DiscoverClaudeSessionID(homeDir, cwd)
	if got != sessionID {
		t.Errorf("got %q, want %q", got, sessionID)
	}
}

func TestDiscoverClaudeSessionID_NoFixture_ReturnsEmpty(t *testing.T) {
	got := DiscoverClaudeSessionID(t.TempDir(), t.TempDir())
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}
