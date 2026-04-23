package config

import (
	"strings"
	"testing"
)

// TestRejectWorkerSpawnCommandKey asserts that the parser refuses any
// workspace.toml that embeds NIWA_WORKER_SPAWN_COMMAND as a config key
// at any nesting depth. The override is intentionally env-only (PRD
// R51 / design Decision 6): baking a literal spawn binary path into a
// file that tends to be checked into version control would commit a
// machine-local path and create a hiding place for hostile overrides.
//
// Each case exercises a different nesting depth to prove the scan walks
// the full key path, not just top-level.
func TestRejectWorkerSpawnCommandKey(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{
			name: "top-level key",
			input: `
[workspace]
name = "ws"

NIWA_WORKER_SPAWN_COMMAND = "/opt/bin/fake-claude"

[claude.content.workspace]
source = "workspace.md"
`,
		},
		{
			name: "under [env.vars]",
			input: `
[workspace]
name = "ws"

[env.vars]
NIWA_WORKER_SPAWN_COMMAND = "/opt/bin/fake-claude"

[claude.content.workspace]
source = "workspace.md"
`,
		},
		{
			name: "deeply nested under repos",
			input: `
[workspace]
name = "ws"

[repos.web]
url = "git@github.com:x/web.git"
group = "apps"

[repos.web.env.vars]
NIWA_WORKER_SPAWN_COMMAND = "/opt/bin/fake-claude"

[groups.apps]
visibility = "private"

[claude.content.workspace]
source = "workspace.md"
`,
		},
		{
			name: "under [channels.mesh.roles]",
			input: `
[workspace]
name = "ws"

[channels.mesh.roles]
NIWA_WORKER_SPAWN_COMMAND = "web"

[claude.content.workspace]
source = "workspace.md"
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.input))
			if err == nil {
				t.Fatalf("expected parser to reject NIWA_WORKER_SPAWN_COMMAND key")
			}
			msg := err.Error()
			if !strings.Contains(msg, "NIWA_WORKER_SPAWN_COMMAND") {
				t.Errorf("error missing the forbidden key name: %v", err)
			}
			if !strings.Contains(msg, "environment variable") {
				t.Errorf("error should hint at the env-only override path: %v", err)
			}
		})
	}
}

// TestRejectWorkerSpawnCommandKey_UnrelatedConfigAccepted guards against
// over-eager rejection: a workspace config that merely references niwa
// or has other env vars must still parse.
func TestRejectWorkerSpawnCommandKey_UnrelatedConfigAccepted(t *testing.T) {
	input := `
[workspace]
name = "ws"

[env.vars]
LOG_LEVEL = "debug"
CLAUDE_CONFIG = "/some/path"

[claude.content.workspace]
source = "workspace.md"
`
	result, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("unrelated env keys should parse cleanly: %v", err)
	}
	if got := result.Config.Env.Vars.Values["LOG_LEVEL"].Plain; got != "debug" {
		t.Errorf("LOG_LEVEL = %q, want debug", got)
	}
}
