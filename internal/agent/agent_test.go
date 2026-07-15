package agent

import "testing"

func TestParseAgent(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    Agent
		wantErr bool
	}{
		{"empty defaults to claude", "", AgentClaude, false},
		{"claude", "claude", AgentClaude, false},
		{"codex", "codex", AgentCodex, false},
		{"unknown", "gemini", "", true},
		{"case-sensitive rejects Claude", "Claude", "", true},
		{"whitespace is not trimmed", " codex", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseAgent(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseAgent(%q) = %q, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseAgent(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("ParseAgent(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseAgentUnknownNamesAcceptedSet(t *testing.T) {
	_, err := ParseAgent("nope")
	if err == nil {
		t.Fatal("expected error for unknown agent")
	}
	msg := err.Error()
	for _, want := range []string{"claude", "codex"} {
		if !contains(msg, want) {
			t.Fatalf("error %q does not name accepted value %q", msg, want)
		}
	}
}

func TestRootContextFileName(t *testing.T) {
	tests := []struct {
		agent Agent
		want  string
	}{
		{AgentClaude, "CLAUDE.md"},
		{AgentCodex, "AGENTS.md"},
		{Agent(""), "CLAUDE.md"}, // zero value == claude (fail-safe)
	}
	for _, tt := range tests {
		if got := tt.agent.RootContextFileName(); got != tt.want {
			t.Fatalf("Agent(%q).RootContextFileName() = %q, want %q", tt.agent, got, tt.want)
		}
	}
}

func TestLocalContextFileName(t *testing.T) {
	tests := []struct {
		agent Agent
		want  string
	}{
		{AgentClaude, "CLAUDE.local.md"},
		{AgentCodex, "AGENTS.md"},
		{Agent(""), "CLAUDE.local.md"}, // zero value == claude (fail-safe)
	}
	for _, tt := range tests {
		if got := tt.agent.LocalContextFileName(); got != tt.want {
			t.Fatalf("Agent(%q).LocalContextFileName() = %q, want %q", tt.agent, got, tt.want)
		}
	}
}

func TestWritesRepoLevelContext(t *testing.T) {
	tests := []struct {
		agent Agent
		want  bool
	}{
		{AgentClaude, true},
		{AgentCodex, false},
		{Agent(""), true}, // zero value == claude (fail-safe)
	}
	for _, tt := range tests {
		if got := tt.agent.WritesRepoLevelContext(); got != tt.want {
			t.Fatalf("Agent(%q).WritesRepoLevelContext() = %v, want %v", tt.agent, got, tt.want)
		}
	}
}

func TestResolveAgent(t *testing.T) {
	tests := []struct {
		name             string
		flag, env, wsDef string
		want             Agent
		wantErr          bool
	}{
		{"all empty defaults to claude", "", "", "", AgentClaude, false},
		{"workspace default codex", "", "", "codex", AgentCodex, false},
		{"env overrides workspace default", "", "codex", "claude", AgentCodex, false},
		{"flag overrides env and default", "claude", "codex", "codex", AgentClaude, false},
		{"flag codex over claude default", "codex", "", "claude", AgentCodex, false},
		{"env claude over codex default", "", "claude", "codex", AgentClaude, false},
		{"unknown flag errors", "gemini", "", "", "", true},
		{"unknown env errors", "", "gemini", "", "", true},
		{"unknown workspace default errors", "", "", "gemini", "", true},
		{"flag wins even when env is invalid-shaped but flag valid", "codex", "codex", "", AgentCodex, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveAgent(tt.flag, tt.env, tt.wsDef)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ResolveAgent(%q,%q,%q) = %q, want error", tt.flag, tt.env, tt.wsDef, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveAgent(%q,%q,%q) unexpected error: %v", tt.flag, tt.env, tt.wsDef, err)
			}
			if got != tt.want {
				t.Fatalf("ResolveAgent(%q,%q,%q) = %q, want %q", tt.flag, tt.env, tt.wsDef, got, tt.want)
			}
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
