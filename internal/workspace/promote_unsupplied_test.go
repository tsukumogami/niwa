package workspace

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// The tests in this file cover a promoted key that nothing supplied a value
// for, in the two shapes niwa#367 turned up:
//
//   - declared optional, with an optional vault reference merged in from a
//     workspace overlay, and absent from the clone's materialized env;
//   - declared by no table at all, which is how a public base config looks
//     when it promotes a key that only some other layer declares.
//
// Each shape runs through the worktree path (ApplyToWorktree) and through the
// instance apply path (resolveClaudeEnvVars with no inherited env). Neither
// may fail the run: the key is skipped and the settings document is still
// written.

// worktreeWithCloneEnv builds the ApplyToWorktree fixture with the given clone
// .local.env body and returns everything the call needs.
func worktreeWithCloneEnv(t *testing.T, cloneEnv string) (*config.WorkspaceConfig, string, string, string) {
	t.Helper()
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)
	cloneRepoDir := filepath.Join(instanceRoot, "apps", "app")
	if err := os.MkdirAll(cloneRepoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cloneRepoDir, ".local.env"), []byte(cloneEnv), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg, configDir, instanceRoot, worktreePath
}

// worktreeSettingsEnv reads the env block of the worktree's settings document.
func worktreeSettingsEnv(t *testing.T, worktreePath string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(worktreePath, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatalf("reading settings.local.json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing settings.local.json: %v", err)
	}
	env, _ := doc["env"].(map[string]any)
	return env
}

func TestApplyToWorktreeSkipsOptionalPromotedKeyMissingFromClone(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := worktreeWithCloneEnv(t, "OTHER_KEY=present\n")
	cfg.Claude.Env = config.ClaudeEnvConfig{Promote: []string{"OTHER_KEY", "OPTIONAL_KEY"}}
	// The worktree path merges the overlay structurally and never resolves, so
	// the optional reference reaches it as a literal value entry.
	cfg.Env.Secrets = config.EnvVarsTable{
		Values:   map[string]config.MaybeSecret{"OPTIONAL_KEY": {Plain: "vault://team/OPTIONAL_KEY?required=false"}},
		Optional: map[string]string{"OPTIONAL_KEY": "an optional credential"},
	}

	var stderr bytes.Buffer
	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app", "p", "b",
		WorktreeApplyOptions{Stderr: &stderr}); err != nil {
		t.Fatalf("an optional promoted key the clone did not supply must not fail the worktree: %v", err)
	}

	env := worktreeSettingsEnv(t, worktreePath)
	if env["OTHER_KEY"] != "present" {
		t.Errorf("OTHER_KEY = %v, want the clone's value", env["OTHER_KEY"])
	}
	if v, ok := env["OPTIONAL_KEY"]; ok {
		t.Errorf("OPTIONAL_KEY was promoted (%v); it must be skipped, and the vault reference must never be written", v)
	}
	if !strings.Contains(stderr.String(), "OPTIONAL_KEY") {
		t.Errorf("stderr = %q, want a note naming the skipped key", stderr.String())
	}
}

func TestApplyToWorktreeSkipsUndeclaredPromotedKey(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := worktreeWithCloneEnv(t, "OTHER_KEY=present\n")
	cfg.Claude.Env = config.ClaudeEnvConfig{Promote: []string{"OTHER_KEY", "UNDECLARED_KEY"}}

	var stderr bytes.Buffer
	if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app", "p", "b",
		WorktreeApplyOptions{Stderr: &stderr}); err != nil {
		t.Fatalf("an undeclared promoted key must not fail the worktree: %v", err)
	}

	env := worktreeSettingsEnv(t, worktreePath)
	if env["OTHER_KEY"] != "present" {
		t.Errorf("OTHER_KEY = %v, want the clone's value", env["OTHER_KEY"])
	}
	if _, ok := env["UNDECLARED_KEY"]; ok {
		t.Error("UNDECLARED_KEY was promoted; it must be skipped")
	}
	if !strings.Contains(stderr.String(), "UNDECLARED_KEY") {
		t.Errorf("stderr = %q, want a warning naming the skipped key", stderr.String())
	}
}

func TestApplyPathPromote(t *testing.T) {
	t.Run("optional reference that resolved to nothing", func(t *testing.T) {
		// What the resolver leaves behind for a ?required=false miss: an
		// empty value with no mark.
		ctx, _ := unresolvedEnvCtx(t, map[string]config.MaybeSecret{"OPTIONAL_KEY": {}})
		ctx.Effective.Env.Secrets.Optional = map[string]string{"OPTIONAL_KEY": "an optional credential"}
		ctx.Effective.Claude.Env = config.ClaudeEnvConfig{Promote: []string{"OPTIONAL_KEY"}}
		var stderr bytes.Buffer
		ctx.Stderr = &stderr
		if _, _, err := resolveClaudeEnvVars(ctx); err != nil {
			t.Fatalf("resolveClaudeEnvVars: %v", err)
		}
	})

	t.Run("declared optional with no value", func(t *testing.T) {
		ctx, _ := unresolvedEnvCtx(t, nil)
		ctx.Effective.Env.Secrets.Optional = map[string]string{"OPTIONAL_KEY": "an optional credential"}
		ctx.Effective.Claude.Env = config.ClaudeEnvConfig{Promote: []string{"OPTIONAL_KEY"}}
		got, _, err := resolveClaudeEnvVars(ctx)
		if err != nil {
			t.Fatalf("resolveClaudeEnvVars: %v", err)
		}
		if _, ok := got["OPTIONAL_KEY"]; ok {
			t.Errorf("an unsupplied optional key was promoted: %v", got)
		}
	})

	t.Run("declared by no table", func(t *testing.T) {
		ctx, _ := unresolvedEnvCtx(t, map[string]config.MaybeSecret{"OTHER_KEY": {Plain: "v"}})
		ctx.Effective.Claude.Env = config.ClaudeEnvConfig{Promote: []string{"OTHER_KEY", "UNDECLARED_KEY"}}
		var stderr bytes.Buffer
		ctx.Stderr = &stderr
		got, _, err := resolveClaudeEnvVars(ctx)
		if err != nil {
			t.Fatalf("an undeclared promoted key must not fail apply: %v", err)
		}
		if got["OTHER_KEY"] != "v" {
			t.Errorf("OTHER_KEY = %q, want v", got["OTHER_KEY"])
		}
		if _, ok := got["UNDECLARED_KEY"]; ok {
			t.Errorf("UNDECLARED_KEY was promoted: %v", got)
		}
		if !strings.Contains(stderr.String(), "UNDECLARED_KEY") {
			t.Errorf("stderr = %q, want a warning naming the key", stderr.String())
		}
	})

	t.Run("declared by no table under strict mode", func(t *testing.T) {
		ctx, _ := unresolvedEnvCtx(t, nil)
		ctx.Effective.Claude.Env = config.ClaudeEnvConfig{Promote: []string{"UNDECLARED_KEY"}}
		ctx.StrictSecrets = true
		if _, _, err := resolveClaudeEnvVars(ctx); err == nil {
			t.Fatal("strict mode must refuse a promoted key with no value")
		} else if !strings.Contains(err.Error(), "UNDECLARED_KEY") {
			t.Errorf("error = %q, want it to name the key", err)
		}
	})
}
