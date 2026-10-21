package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// writeWorktreeDefaultConfig writes the posture fixture's workspace config
// with workspaceExtra appended to its [workspace] table, and loads it.
func writeWorktreeDefaultConfig(t *testing.T, f *postureFixture, workspaceExtra string) *config.WorkspaceConfig {
	t.Helper()
	body := "[workspace]\nname = \"posture-ws\"\n" + workspaceExtra +
		"\n[[sources]]\norg = \"testorg\"\n\n[groups.default]\nrepos = [\"app\"]\n"
	path := filepath.Join(f.niwaDir, "workspace.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := config.Load(path)
	if err != nil {
		t.Fatalf("loading workspace config: %v", err)
	}
	return result.Config
}

func recordedWorktreeDefault(t *testing.T, instanceRoot string) string {
	t.Helper()
	state, err := LoadState(instanceRoot)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	return state.DefaultWorktreeRepo
}

// TestCreateAndApplyRecordDefaultWorktreeRepo checks that Create records
// [workspace].default_worktree_repo in the instance state, where the
// WorktreeCreate hook reads it, and that Apply recomputes it from the current
// config: a removed key stops being recorded rather than carrying over.
func TestCreateAndApplyRecordDefaultWorktreeRepo(t *testing.T) {
	f := newPostureFixture(t, "")
	f.create(t, writeWorktreeDefaultConfig(t, f, "default_worktree_repo = \"app\"\n"))
	if got := recordedWorktreeDefault(t, f.instanceRoot); got != "app" {
		t.Fatalf("after Create: DefaultWorktreeRepo = %q, want %q", got, "app")
	}

	cfg := writeWorktreeDefaultConfig(t, f, "")
	if err := f.applier.Apply(context.Background(), cfg, f.niwaDir, f.instanceRoot); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := recordedWorktreeDefault(t, f.instanceRoot); got != "" {
		t.Errorf("after Apply without the key: DefaultWorktreeRepo = %q, want empty", got)
	}
	raw, err := os.ReadFile(filepath.Join(f.instanceRoot, ".niwa", StateFile))
	if err != nil {
		t.Fatalf("reading instance state: %v", err)
	}
	if strings.Contains(string(raw), "default_worktree_repo") {
		t.Errorf("instance.json carries a default_worktree_repo key with nothing configured:\n%s", raw)
	}
}
