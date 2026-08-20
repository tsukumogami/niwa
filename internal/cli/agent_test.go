package cli

import (
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/config"
)

func cfgWithDefaultAgent(def string) *config.WorkspaceConfig {
	return &config.WorkspaceConfig{Workspace: config.WorkspaceMeta{Name: "ws", DefaultAgent: def}}
}

func hostCfgWithDefaultAgent(def string) *config.GlobalConfig {
	return &config.GlobalConfig{Global: config.GlobalSettings{DefaultAgent: def}}
}

// TestResolveSessionAgent covers the CLI-level resolution: the flag and the
// NIWA_AGENT env override the workspace default_agent, which in turn overrides
// the host default_agent, in precedence order flag > env > workspace > host >
// claude, and an unknown value from any source errors.
func TestResolveSessionAgent(t *testing.T) {
	tests := []struct {
		name    string
		flag    string
		env     string // "" means unset for this test
		def     string
		hostDef string
		want    agent.Agent
		wantErr bool
	}{
		{"no sources defaults to claude", "", "", "", "", agent.AgentClaude, false},
		{"workspace default codex", "", "", "codex", "", agent.AgentCodex, false},
		{"env overrides default", "", "codex", "claude", "", agent.AgentCodex, false},
		{"flag overrides env and default", "claude", "codex", "codex", "", agent.AgentClaude, false},
		{"flag codex over claude default", "codex", "", "claude", "", agent.AgentCodex, false},
		{"unknown flag errors", "gemini", "", "", "", "", true},
		{"unknown env errors", "", "gemini", "", "", "", true},
		{"unknown default errors", "", "", "gemini", "", "", true},

		{"host default answers when nothing else does", "", "", "", "codex", agent.AgentCodex, false},
		{"workspace default outranks host default", "", "", "claude", "codex", agent.AgentClaude, false},
		{"env outranks host default", "", "claude", "", "codex", agent.AgentClaude, false},
		{"flag outranks host default", "claude", "", "", "codex", agent.AgentClaude, false},
		{"unknown host default errors", "", "", "", "gemini", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env == "" {
				t.Setenv("NIWA_AGENT", "")
			} else {
				t.Setenv("NIWA_AGENT", tt.env)
			}
			got, err := resolveSessionAgent(tt.flag, cfgWithDefaultAgent(tt.def), hostCfgWithDefaultAgent(tt.hostDef))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resolveSessionAgent(%q, def=%q, host=%q, env=%q) = %q, want error", tt.flag, tt.def, tt.hostDef, tt.env, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("resolveSessionAgent(%q, def=%q, host=%q, env=%q) = %q, want %q", tt.flag, tt.def, tt.hostDef, tt.env, got, tt.want)
			}
		})
	}
}

// TestResolveSessionAgentNilConfigs pins the nil-tolerance both config
// arguments carry. A config niwa could not load is "that source is unset", not
// a panic and not a failed command: dispatch resolves against the sources it
// does have and leaves a broken config for the provisioning path to report.
func TestResolveSessionAgentNilConfigs(t *testing.T) {
	t.Setenv("NIWA_AGENT", "")
	got, err := resolveSessionAgent("", nil, nil)
	if err != nil {
		t.Fatalf("resolveSessionAgent with nil configs: %v", err)
	}
	if got != agent.AgentClaude {
		t.Fatalf("resolveSessionAgent with nil configs = %q, want %q", got, agent.AgentClaude)
	}

	t.Setenv("NIWA_AGENT", "codex")
	got, err = resolveSessionAgent("", nil, nil)
	if err != nil {
		t.Fatalf("resolveSessionAgent with nil configs and NIWA_AGENT: %v", err)
	}
	if got != agent.AgentCodex {
		t.Fatalf("resolveSessionAgent with nil configs and NIWA_AGENT=codex = %q, want %q", got, agent.AgentCodex)
	}
}
