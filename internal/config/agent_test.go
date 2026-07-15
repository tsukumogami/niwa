package config

import "testing"

// TestWorkspaceDefaultAgentDecodes asserts that [workspace].default_agent
// decodes into WorkspaceMeta.DefaultAgent as a raw string, and that its absence
// leaves the field empty (the default-agent case). The value is validated later
// by internal/agent.ParseAgent, not at decode time, so config carries it raw.
func TestWorkspaceDefaultAgentDecodes(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		res, err := Parse([]byte(`
[workspace]
name = "ws"
default_agent = "codex"
`))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if got := res.Config.Workspace.DefaultAgent; got != "codex" {
			t.Fatalf("DefaultAgent = %q, want %q", got, "codex")
		}
	})

	t.Run("absent", func(t *testing.T) {
		res, err := Parse([]byte(`
[workspace]
name = "ws"
`))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if got := res.Config.Workspace.DefaultAgent; got != "" {
			t.Fatalf("DefaultAgent = %q, want empty", got)
		}
	})
}
