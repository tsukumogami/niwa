package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// TestApplyToWorktreeInheritsPromotedEnvFromClone pins the fix for the worktree
// promote regression. [claude.env] promote lists a key whose value is NOT present
// in the worktree path's (unresolved) config: it was sourced from vault or the
// machine-identity sync during the instance apply and only exists in the clone's
// materialized .local.env. The worktree SettingsMaterializer must inherit that
// value from the clone rather than re-resolving it (which fails with "promoted
// key not found"), so the worktree's settings.local.json carries the key.
//
// This is the GH_TOKEN shape: declared [env.secrets.required] + promoted, with no
// static [env.secrets] binding, resolved only at apply time.
func TestApplyToWorktreeInheritsPromotedEnvFromClone(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)
	cfg.Claude.Env = config.ClaudeEnvConfig{Promote: []string{"GH_TOKEN"}}

	// The instance clone carries the already-materialized env output the full
	// apply wrote. group/repo are "apps"/"app" (matching the ApplyToWorktree call
	// below), so the clone lives at <instanceRoot>/apps/app.
	cloneRepoDir := filepath.Join(instanceRoot, "apps", "app")
	if err := os.MkdirAll(cloneRepoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cloneRepoDir, ".local.env"), []byte("GH_TOKEN=ghp_fromclone\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app", "ship-the-thing", "branch-xyz", WorktreeApplyOptions{})
	if err != nil {
		t.Fatalf("ApplyToWorktree with a clone-sourced promoted key should succeed, got: %v", err)
	}

	settingsPath := filepath.Join(worktreePath, ".claude", "settings.local.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("reading settings.local.json: %v", err)
	}
	if !strings.Contains(string(data), "GH_TOKEN") {
		t.Errorf("settings.local.json missing promoted GH_TOKEN inherited from clone:\n%s", data)
	}
	if !strings.Contains(string(data), "ghp_fromclone") {
		t.Errorf("settings.local.json missing the clone-sourced GH_TOKEN value:\n%s", data)
	}
}

// TestApplyToWorktreePromoteMissingFromCloneErrors pins the negative case: when a
// promoted key is absent from BOTH the config and the clone's materialized env,
// the worktree apply still surfaces the promote error naming the key rather than
// silently dropping it. Inheritance must not mask a genuine misconfiguration.
func TestApplyToWorktreePromoteMissingFromCloneErrors(t *testing.T) {
	cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)
	cfg.Claude.Env = config.ClaudeEnvConfig{Promote: []string{"GH_TOKEN"}}

	// Clone exists but its materialized env holds no GH_TOKEN.
	cloneRepoDir := filepath.Join(instanceRoot, "apps", "app")
	if err := os.MkdirAll(cloneRepoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cloneRepoDir, ".local.env"), []byte("OTHER=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app", "ship-the-thing", "branch-xyz", WorktreeApplyOptions{})
	if err == nil {
		t.Fatal("expected ApplyToWorktree to fail when a promoted key is absent from both config and clone env")
	}
	if !strings.Contains(err.Error(), "GH_TOKEN") {
		t.Errorf("error = %q, want it to name the missing promoted key", err.Error())
	}
}
