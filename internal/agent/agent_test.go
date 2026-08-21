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
	for _, want := range All() {
		if !contains(msg, string(want)) {
			t.Fatalf("error %q does not name accepted value %q", msg, want)
		}
	}
}

// TestParseAgentAcceptedSetIsDerivedFromAll pins the whole accepted-values
// clause to All(), not just the presence of each name. The clause was the last
// place in the package where the closed set was typed out, and a hand-written
// list is the kind of thing that goes stale silently: the agent is added, the
// switch accepts it, and the error a developer reads still names two.
func TestParseAgentAcceptedSetIsDerivedFromAll(t *testing.T) {
	joined := ""
	for i, a := range All() {
		if i > 0 {
			joined += ", "
		}
		joined += string(a)
	}

	_, err := ParseAgent("nope")
	if err == nil {
		t.Fatal("expected error for unknown agent")
	}
	if want := "accepted values are: " + joined; !contains(err.Error(), want) {
		t.Fatalf("error %q does not carry the accepted set as All() lists it (%q)", err, want)
	}
}

func TestAllReturnsAFreshSlice(t *testing.T) {
	first := All()
	if len(first) == 0 {
		t.Fatal("All() returned no agents")
	}
	first[0] = Agent("mutated")
	if got := All()[0]; got == Agent("mutated") {
		t.Fatal("All() handed out the package's own slice; a caller can narrow the closed set")
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
		// AGENTS.override.md, not AGENTS.md: it is first in Codex's hardcoded
		// per-directory precedence, so it is the one name that is read in a
		// repository that commits its own context file.
		{AgentCodex, "AGENTS.override.md"},
		{Agent(""), "CLAUDE.local.md"}, // zero value == claude (fail-safe)
	}
	for _, tt := range tests {
		if got := tt.agent.LocalContextFileName(); got != tt.want {
			t.Fatalf("Agent(%q).LocalContextFileName() = %q, want %q", tt.agent, got, tt.want)
		}
	}
}

func TestResolveAgent(t *testing.T) {
	tests := []struct {
		name                      string
		flag, env, wsDef, hostDef string
		want                      Agent
		wantErr                   bool
	}{
		{"all empty defaults to claude", "", "", "", "", AgentClaude, false},
		{"workspace default codex", "", "", "codex", "", AgentCodex, false},
		{"env overrides workspace default", "", "codex", "claude", "", AgentCodex, false},
		{"flag overrides env and default", "claude", "codex", "codex", "", AgentClaude, false},
		{"flag codex over claude default", "codex", "", "claude", "", AgentCodex, false},
		{"env claude over codex default", "", "claude", "codex", "", AgentClaude, false},
		{"unknown flag errors", "gemini", "", "", "", "", true},
		{"unknown env errors", "", "gemini", "", "", "", true},
		{"unknown workspace default errors", "", "", "gemini", "", "", true},
		{"flag wins even when env is invalid-shaped but flag valid", "codex", "codex", "", "", AgentCodex, false},

		// The host default is the broadest rung: it answers when nothing more
		// specific does, and loses to every source above it.
		{"host default alone", "", "", "", "codex", AgentCodex, false},
		{"workspace default outranks host default", "", "", "claude", "codex", AgentClaude, false},
		{"env outranks host default", "", "claude", "", "codex", AgentClaude, false},
		{"flag outranks host default", "claude", "", "", "codex", AgentClaude, false},
		{"host default fills in for a workspace that states none", "", "", "", "claude", AgentClaude, false},
		{"unknown host default errors", "", "", "", "gemini", "", true},
		{"a valid workspace default hides an invalid host default", "", "", "codex", "gemini", AgentCodex, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveAgent(tt.flag, tt.env, tt.wsDef, tt.hostDef)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ResolveAgent(%q,%q,%q,%q) = %q, want error", tt.flag, tt.env, tt.wsDef, tt.hostDef, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveAgent(%q,%q,%q,%q) unexpected error: %v", tt.flag, tt.env, tt.wsDef, tt.hostDef, err)
			}
			if got != tt.want {
				t.Fatalf("ResolveAgent(%q,%q,%q,%q) = %q, want %q", tt.flag, tt.env, tt.wsDef, tt.hostDef, got, tt.want)
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
