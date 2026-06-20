package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// readSettingsDoc materializes the settings file via SettingsMaterializer for
// the given context and returns the parsed JSON document plus the written path.
func readSettingsDoc(t *testing.T, ctx *MaterializeContext) (map[string]any, string) {
	t.Helper()
	m := &SettingsMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 file written, got %d", len(written))
	}
	data, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatalf("reading settings file: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing settings JSON: %v", err)
	}
	return doc, written[0]
}

// worktreeHookCommands returns the command strings under hooks[event][*].hooks
// for the given event, or nil if the event is absent.
func worktreeHookCommands(t *testing.T, doc map[string]any, event string) []string {
	t.Helper()
	hooks, ok := doc["hooks"].(map[string]any)
	if !ok {
		return nil
	}
	entries, ok := hooks[event].([]any)
	if !ok {
		return nil
	}
	var cmds []string
	for _, e := range entries {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		hl, ok := em["hooks"].([]any)
		if !ok {
			continue
		}
		for _, h := range hl {
			hm, ok := h.(map[string]any)
			if !ok {
				continue
			}
			if c, ok := hm["command"].(string); ok {
				cmds = append(cmds, c)
			}
		}
	}
	return cmds
}

func TestSettingsMaterializerWorktreeSupportedWritesHooks(t *testing.T) {
	repoDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		RepoDir: repoDir,
		WorktreeDelegation: &WorktreeDelegation{
			Supported: true,
			NiwaPath:  "/usr/local/bin/niwa",
		},
	}

	doc, _ := readSettingsDoc(t, ctx)

	wantCmd := "/usr/local/bin/niwa worktree from-hook"
	for _, event := range []string{"WorktreeCreate", "WorktreeRemove"} {
		cmds := worktreeHookCommands(t, doc, event)
		if len(cmds) != 1 {
			t.Fatalf("%s: expected 1 hook command, got %v", event, cmds)
		}
		if cmds[0] != wantCmd {
			t.Errorf("%s command = %q, want %q", event, cmds[0], wantCmd)
		}
		if !strings.HasSuffix(cmds[0], "worktree from-hook") {
			t.Errorf("%s command %q must end with 'worktree from-hook'", event, cmds[0])
		}
		if !filepath.IsAbs(strings.TrimSuffix(cmds[0], " worktree from-hook")) {
			t.Errorf("%s command %q must use an absolute niwa path", event, cmds[0])
		}
	}

	// Supported branch must NOT write permissions.deny (mutual exclusion).
	if perms, ok := doc["permissions"].(map[string]any); ok {
		if _, hasDeny := perms["deny"]; hasDeny {
			t.Error("supported branch must not write permissions.deny")
		}
	}
}

func TestSettingsMaterializerWorktreeUnsupportedWritesDeny(t *testing.T) {
	repoDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		RepoDir: repoDir,
		WorktreeDelegation: &WorktreeDelegation{
			Supported: false,
		},
	}

	doc, _ := readSettingsDoc(t, ctx)

	perms, ok := doc["permissions"].(map[string]any)
	if !ok {
		t.Fatal("expected permissions key in unsupported branch")
	}
	deny, ok := perms["deny"].([]any)
	if !ok {
		t.Fatalf("expected permissions.deny array, got %v", perms["deny"])
	}
	got := make([]string, 0, len(deny))
	for _, d := range deny {
		got = append(got, d.(string))
	}
	want := []string{"EnterWorktree", "ExitWorktree"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("permissions.deny = %v, want %v", got, want)
	}

	// Unsupported branch must NOT write worktree hooks (mutual exclusion).
	if cmds := worktreeHookCommands(t, doc, "WorktreeCreate"); cmds != nil {
		t.Errorf("unsupported branch must not write WorktreeCreate hook, got %v", cmds)
	}
	if cmds := worktreeHookCommands(t, doc, "WorktreeRemove"); cmds != nil {
		t.Errorf("unsupported branch must not write WorktreeRemove hook, got %v", cmds)
	}
}

// TestSettingsMaterializerWorktreeUnsupportedPreservesDefaultMode asserts the
// deny fallback coexists with a configured permissions.defaultMode rather than
// clobbering it.
func TestSettingsMaterializerWorktreeUnsupportedPreservesDefaultMode(t *testing.T) {
	repoDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		RepoDir: repoDir,
		Effective: EffectiveConfig{
			Claude: config.ClaudeConfig{
				Settings: config.SettingsConfig{"permissions": config.MaybeSecret{Plain: "bypass"}},
			},
		},
		WorktreeDelegation: &WorktreeDelegation{Supported: false},
	}

	doc, _ := readSettingsDoc(t, ctx)

	perms, ok := doc["permissions"].(map[string]any)
	if !ok {
		t.Fatal("expected permissions key")
	}
	if perms["defaultMode"] != "bypassPermissions" {
		t.Errorf("defaultMode = %v, want bypassPermissions (must be preserved)", perms["defaultMode"])
	}
	if _, ok := perms["deny"].([]any); !ok {
		t.Errorf("expected permissions.deny alongside defaultMode, got %v", perms)
	}
}

// TestSettingsMaterializerWorktreeIdempotent re-runs the materializer twice
// against the same repo dir and asserts the file bytes are identical (no
// duplicated hook or deny entries).
func TestSettingsMaterializerWorktreeIdempotent(t *testing.T) {
	for _, tc := range []struct {
		name      string
		supported bool
	}{
		{"supported-hooks", true},
		{"unsupported-deny", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repoDir := filepath.Join(t.TempDir(), "repo")
			if err := os.MkdirAll(repoDir, 0o755); err != nil {
				t.Fatal(err)
			}
			newCtx := func() *MaterializeContext {
				return &MaterializeContext{
					RepoDir: repoDir,
					WorktreeDelegation: &WorktreeDelegation{
						Supported: tc.supported,
						NiwaPath:  "/usr/local/bin/niwa",
					},
				}
			}

			m := &SettingsMaterializer{}
			if _, err := m.Materialize(newCtx()); err != nil {
				t.Fatalf("first materialize: %v", err)
			}
			path := filepath.Join(repoDir, ".claude", "settings.local.json")
			first, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := m.Materialize(newCtx()); err != nil {
				t.Fatalf("second materialize: %v", err)
			}
			second, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			if string(first) != string(second) {
				t.Errorf("re-apply changed settings file (not idempotent):\nfirst:\n%s\nsecond:\n%s", first, second)
			}
		})
	}
}

// TestSettingsMaterializerNoWorktreeDelegationUnchanged asserts a nil
// WorktreeDelegation installs neither hook nor deny (the pre-feature behavior).
func TestSettingsMaterializerNoWorktreeDelegationUnchanged(t *testing.T) {
	repoDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := &MaterializeContext{
		RepoDir:            repoDir,
		WorktreeDelegation: nil,
	}

	m := &SettingsMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if written != nil {
		t.Errorf("expected no file written with nil delegation and no settings, got %v", written)
	}
}
