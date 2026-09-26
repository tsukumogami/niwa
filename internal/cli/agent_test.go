package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/config"
)

func cfgWithDefaultAgent(def string) *config.WorkspaceConfig {
	return &config.WorkspaceConfig{Workspace: config.WorkspaceMeta{Name: "ws", DefaultAgent: def}}
}

func hostCfgWithDefaultHarness(def string) *config.GlobalConfig {
	return &config.GlobalConfig{Global: config.GlobalSettings{DefaultDispatchHarness: def}}
}

// TestResolveSessionAgent covers the CLI-level resolution: the flag and the
// NIWA_DISPATCH_HARNESS env override the workspace default_agent, which in turn overrides
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
				t.Setenv("NIWA_DISPATCH_HARNESS", "")
			} else {
				t.Setenv("NIWA_DISPATCH_HARNESS", tt.env)
			}
			got, err := resolveSessionAgent(tt.flag, cfgWithDefaultAgent(tt.def), "", hostCfgWithDefaultHarness(tt.hostDef))
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

// TestResolveSessionAgentErrorNamesTheOffendingRung is what turns a rejected
// value into something a developer can act on. Four sources can hold the bad
// value and the parse error reads the same for all four, so a stale
// default_agent set weeks ago sends the reader looking through the two files
// they are least likely to have open.
//
// Each rung is named the way it would be edited, and the error names that rung
// only -- a message that listed all four would be the same guessing game with
// more words.
func TestResolveSessionAgentErrorNamesTheOffendingRung(t *testing.T) {
	wsPath := filepath.Join(t.TempDir(), ".niwa", "workspace.toml")
	// Every rung's label, so each case can assert the other three are absent.
	labels := []string{"--" + harnessFlagName, "NIWA_DISPATCH_HARNESS", "[workspace].default_agent", "[global].default_dispatch_harness"}

	tests := []struct {
		name string
		flag string
		env  string
		def  string
		host string
		// want is the label this rung must carry; wantPath is an extra
		// substring, the file that holds the value.
		want         string
		wantPath     string
		wantHostPath bool
	}{
		{name: "flag", flag: "gemini", want: "--" + harnessFlagName},
		{name: "env", env: "gemini", want: "NIWA_DISPATCH_HARNESS"},
		{name: "workspace default", def: "gemini", want: "[workspace].default_agent", wantPath: wsPath},
		{name: "host default", host: "gemini", want: "[global].default_dispatch_harness", wantHostPath: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("NIWA_DISPATCH_HARNESS", tt.env)

			wantPath := tt.wantPath
			if tt.wantHostPath {
				// The personal config's real path, honoring XDG_CONFIG_HOME:
				// naming the file it usually lives in would be naming the wrong
				// file for anyone who moved it.
				p, err := config.GlobalConfigPath()
				if err != nil {
					t.Fatal(err)
				}
				wantPath = p
			}

			_, err := resolveSessionAgent(tt.flag, cfgWithDefaultAgent(tt.def), wsPath, hostCfgWithDefaultHarness(tt.host))
			if err == nil {
				t.Fatalf("resolveSessionAgent accepted %q from the %s rung, want a rejection", "gemini", tt.name)
			}
			msg := err.Error()
			if !strings.Contains(msg, tt.want) {
				t.Errorf("rejection does not name the %s rung (%s):\n%s", tt.name, tt.want, msg)
			}
			if wantPath != "" && !strings.Contains(msg, wantPath) {
				t.Errorf("rejection does not name the file holding the value (%s):\n%s", wantPath, msg)
			}
			for _, other := range labels {
				if other == tt.want {
					continue
				}
				if strings.Contains(msg, other) {
					t.Errorf("rejection blames the %s rung too, so it does not say which one to edit:\n%s", other, msg)
				}
			}
			// The wrapping must not swallow what the old message carried.
			if !strings.Contains(msg, "gemini") {
				t.Errorf("rejection does not quote the rejected value:\n%s", msg)
			}
			for _, ag := range agent.All() {
				if !strings.Contains(msg, string(ag)) {
					t.Errorf("rejection does not name the accepted value %q:\n%s", ag, msg)
				}
			}
		})
	}
}

// TestResolveSessionAgentNilConfigs pins the nil-tolerance both config
// arguments carry. A config niwa could not load is "that source is unset", not
// a panic and not a failed command: dispatch resolves against the sources it
// does have and leaves a broken config for the provisioning path to report.
func TestResolveSessionAgentNilConfigs(t *testing.T) {
	t.Setenv("NIWA_DISPATCH_HARNESS", "")
	got, err := resolveSessionAgent("", nil, "", nil)
	if err != nil {
		t.Fatalf("resolveSessionAgent with nil configs: %v", err)
	}
	if got != agent.AgentClaude {
		t.Fatalf("resolveSessionAgent with nil configs = %q, want %q", got, agent.AgentClaude)
	}

	t.Setenv("NIWA_DISPATCH_HARNESS", "codex")
	got, err = resolveSessionAgent("", nil, "", nil)
	if err != nil {
		t.Fatalf("resolveSessionAgent with nil configs and NIWA_DISPATCH_HARNESS: %v", err)
	}
	if got != agent.AgentCodex {
		t.Fatalf("resolveSessionAgent with nil configs and NIWA_DISPATCH_HARNESS=codex = %q, want %q", got, agent.AgentCodex)
	}
}
