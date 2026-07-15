package cli

import (
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/config"
)

func cfgWithDefaultAgent(def string) *config.WorkspaceConfig {
	return &config.WorkspaceConfig{Workspace: config.WorkspaceMeta{Name: "ws", DefaultAgent: def}}
}

// TestResolveSessionAgent covers the CLI-level resolution: the flag and the
// NIWA_AGENT env override the workspace default_agent, in precedence order
// flag > env > default > claude, and an unknown value from any source errors.
func TestResolveSessionAgent(t *testing.T) {
	tests := []struct {
		name    string
		flag    string
		env     string // "" means unset for this test
		def     string
		want    agent.Agent
		wantErr bool
	}{
		{"no sources defaults to claude", "", "", "", agent.AgentClaude, false},
		{"workspace default codex", "", "", "codex", agent.AgentCodex, false},
		{"env overrides default", "", "codex", "claude", agent.AgentCodex, false},
		{"flag overrides env and default", "claude", "codex", "codex", agent.AgentClaude, false},
		{"flag codex over claude default", "codex", "", "claude", agent.AgentCodex, false},
		{"unknown flag errors", "gemini", "", "", "", true},
		{"unknown env errors", "", "gemini", "", "", true},
		{"unknown default errors", "", "", "gemini", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env == "" {
				t.Setenv("NIWA_AGENT", "")
			} else {
				t.Setenv("NIWA_AGENT", tt.env)
			}
			got, err := resolveSessionAgent(tt.flag, cfgWithDefaultAgent(tt.def))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resolveSessionAgent(%q, def=%q, env=%q) = %q, want error", tt.flag, tt.def, tt.env, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("resolveSessionAgent(%q, def=%q, env=%q) = %q, want %q", tt.flag, tt.def, tt.env, got, tt.want)
			}
		})
	}
}
