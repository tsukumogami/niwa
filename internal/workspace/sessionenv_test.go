package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/secret"
)

// sessionEnvConfig builds a workspace config declaring the neutral session
// environment and nothing else.
func sessionEnvConfig(vars map[string]config.MaybeSecret, promote ...string) *config.WorkspaceConfig {
	return &config.WorkspaceConfig{
		Session: config.SessionConfig{
			Env: config.SessionEnvConfig{
				Promote: promote,
				Vars:    config.EnvVarsTable{Values: vars},
			},
		},
	}
}

// TestSessionEnvResolvesToLiteralValues is the property every generated
// destination rests on: what leaves this package is resolved, so nothing niwa
// writes relies on an agent expanding anything at load time.
func TestSessionEnvResolvesToLiteralValues(t *testing.T) {
	cfg := sessionEnvConfig(map[string]config.MaybeSecret{
		"REGION":    {Plain: "eu-west-1"},
		"API_TOKEN": {Secret: secret.New([]byte("s3cr3t"), secret.Origin{Key: "API_TOKEN"})},
	})

	vars, _, err := SessionEnvVars(cfg, MergeInstanceOverrides(cfg), t.TempDir())
	if err != nil {
		t.Fatalf("SessionEnvVars: %v", err)
	}
	if vars["REGION"] != "eu-west-1" {
		t.Errorf("REGION = %q, want eu-west-1", vars["REGION"])
	}
	if vars["API_TOKEN"] != "s3cr3t" {
		t.Errorf("API_TOKEN did not resolve to its plaintext, got %q", vars["API_TOKEN"])
	}
}

// TestSessionEnvOmitsWhatItCouldNotSupply keeps a credential that never
// resolved from arriving as an empty string. A command started with an empty
// token fails in a way that looks like the service's fault; an absent variable
// at least fails as itself.
func TestSessionEnvOmitsWhatItCouldNotSupply(t *testing.T) {
	cfg := sessionEnvConfig(map[string]config.MaybeSecret{
		"MARKED": {Unresolved: &config.Unresolved{Cause: config.UnresolvedCause("provider unreachable")}},
		"KEPT":   {Plain: "fine"},
	})

	vars, _, err := SessionEnvVars(cfg, MergeInstanceOverrides(cfg), t.TempDir())
	if err != nil {
		t.Fatalf("SessionEnvVars: %v", err)
	}
	if _, present := vars["MARKED"]; present {
		t.Error("a value the resolver could not supply was delivered anyway")
	}
	if vars["KEPT"] != "fine" {
		t.Errorf("KEPT = %q, want fine", vars["KEPT"])
	}
}

// TestSessionEnvPromotesFromTheEnvPipeline covers the second half of the
// declaration's shape: a key the workspace already resolves through [env] is
// named once and reaches every agent's session.
func TestSessionEnvPromotesFromTheEnvPipeline(t *testing.T) {
	cfg := sessionEnvConfig(nil, "SHARED")
	cfg.Env = config.EnvConfig{
		Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"SHARED": {Plain: "from-pipeline"}}},
	}

	vars, _, err := SessionEnvVars(cfg, MergeInstanceOverrides(cfg), t.TempDir())
	if err != nil {
		t.Fatalf("SessionEnvVars: %v", err)
	}
	if vars["SHARED"] != "from-pipeline" {
		t.Errorf("SHARED = %q, want from-pipeline", vars["SHARED"])
	}
}

// TestSessionEnvErrorNamesItsOwnTable checks that a workspace carrying both
// declarations is told which one to fix. The promote machinery is shared, so
// the label is the only thing distinguishing the two failures.
func TestSessionEnvErrorNamesItsOwnTable(t *testing.T) {
	cfg := sessionEnvConfig(nil, "MISSING")
	cfg.Env = config.EnvConfig{
		Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{"OTHER": {Plain: "x"}}},
	}

	_, _, err := SessionEnvVars(cfg, MergeInstanceOverrides(cfg), t.TempDir())
	if err == nil {
		t.Fatal("promoting a key that does not exist produced no error")
	}
	if !strings.Contains(err.Error(), "session.env") {
		t.Errorf("the error does not name the table it came from: %v", err)
	}
	if strings.Contains(err.Error(), "claude.env") {
		t.Errorf("the error blames the Claude table for a neutral declaration: %v", err)
	}
}

// TestNoDeclarationResolvesToNothing keeps the common case free of surprises: a
// workspace that declares no session environment gets no map, which is what
// keeps the generated documents free of an empty policy table.
func TestNoDeclarationResolvesToNothing(t *testing.T) {
	vars, sources, err := SessionEnvVars(&config.WorkspaceConfig{}, EffectiveConfig{}, t.TempDir())
	if err != nil {
		t.Fatalf("SessionEnvVars: %v", err)
	}
	if len(vars) != 0 || len(sources) != 0 {
		t.Errorf("an undeclared session environment produced %v / %v", vars, sources)
	}
}

// TestNoClaudeKeyGatesTheNeutralDeclaration is the requirement stated as a
// test, in both directions.
//
// The neutral table must reach a Claude session with no [claude.env] present at
// all -- if it needed one, the Claude-named table would be the gate -- and where
// both are declared the narrower one wins per key without erasing the rest.
func TestNoClaudeKeyGatesTheNeutralDeclaration(t *testing.T) {
	t.Run("no claude.env at all", func(t *testing.T) {
		ctx := &MaterializeContext{SessionEnv: map[string]string{"REGION": "eu-west-1"}}
		vars, _, err := resolveClaudeEnvVars(ctx)
		if err != nil {
			t.Fatalf("resolveClaudeEnvVars: %v", err)
		}
		if vars["REGION"] != "eu-west-1" {
			t.Errorf("the neutral declaration did not reach a session with no [claude.env] declared: %v", vars)
		}
	})

	t.Run("the agent-specific table wins per key", func(t *testing.T) {
		ctx := &MaterializeContext{
			SessionEnv: map[string]string{"REGION": "eu-west-1", "SHARED": "neutral"},
			Effective: EffectiveConfig{Claude: config.ClaudeConfig{Env: config.ClaudeEnvConfig{
				Vars: config.EnvVarsTable{Values: map[string]config.MaybeSecret{
					"SHARED":      {Plain: "claude"},
					"CLAUDE_ONLY": {Plain: "yes"},
				}},
			}}},
		}
		vars, _, err := resolveClaudeEnvVars(ctx)
		if err != nil {
			t.Fatalf("resolveClaudeEnvVars: %v", err)
		}
		if vars["SHARED"] != "claude" {
			t.Errorf("SHARED = %q, want claude: the narrower declaration wins per key", vars["SHARED"])
		}
		if vars["REGION"] != "eu-west-1" {
			t.Errorf("REGION = %q, want eu-west-1: a colliding key must not erase the rest", vars["REGION"])
		}
		if vars["CLAUDE_ONLY"] != "yes" {
			t.Errorf("CLAUDE_ONLY = %q, want yes", vars["CLAUDE_ONLY"])
		}
	})
}

// TestEnsureInstanceGitignoreAddsAndKeepsExtraPatterns covers the extras arm of
// the instance-root coverage: new patterns are appended without disturbing the
// base one or anything the user wrote, and a second run adds nothing.
func TestEnsureInstanceGitignoreAddsAndKeepsExtraPatterns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(path, []byte("node_modules/\n*.local*\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := EnsureInstanceGitignore(dir, ".codex/"); err != nil {
		t.Fatalf("EnsureInstanceGitignore: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading .gitignore: %v", err)
	}
	if string(data) != "node_modules/\n*.local*\n.codex/\n" {
		t.Errorf(".gitignore = %q", string(data))
	}

	if err := EnsureInstanceGitignore(dir, ".codex/"); err != nil {
		t.Fatalf("second EnsureInstanceGitignore: %v", err)
	}
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading .gitignore: %v", err)
	}
	if string(again) != string(data) {
		t.Errorf("a second run changed the file:\nbefore:\n%s\nafter:\n%s", data, again)
	}
}
