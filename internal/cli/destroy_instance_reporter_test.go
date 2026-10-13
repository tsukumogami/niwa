package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/claudetrust"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// TestDestroyInstanceWarningTo checks the teardown reap and dispatch cleanup
// use: a refused Claude trust withdrawal is reported, while the plugin-record
// prune it also runs stays quiet although it removed a record.
func TestDestroyInstanceWarningTo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	inst := filepath.Join(t.TempDir(), "doomed")
	if err := os.MkdirAll(inst, 0o755); err != nil {
		t.Fatal(err)
	}
	claudeJSON := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(claudeJSON, []byte(`{"projects": {}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A grant for another path: the withdrawal refuses it with a warning.
	state := &workspace.InstanceState{
		SchemaVersion:  workspace.SchemaVersion,
		InstanceName:   "doomed",
		InstanceNumber: 1,
		Root:           inst,
		Created:        time.Now(),
		LastApplied:    time.Now(),
		Repos:          map[string]workspace.RepoState{},
		ClaudeTrust: &claudetrust.Grant{
			ConfigFile: claudeJSON,
			Key:        "/somewhere/else",
			Mode:       claudetrust.ModeCreated,
			Source:     claudetrust.SourceWorkspace,
		},
	}
	if err := workspace.SaveState(inst, state); err != nil {
		t.Fatal(err)
	}

	registry := filepath.Join(home, ".claude", "plugins", "installed_plugins.json")
	doc := map[string]any{
		"version": "1",
		"plugins": map[string]any{
			"example@market": []map[string]any{
				{"scope": "project", "projectPath": inst, "installPath": home, "version": "1.0.0"},
			},
		},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(registry), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(registry, data, 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := destroyInstanceWarningTo(inst, &out); err != nil {
		t.Fatalf("destroyInstanceWarningTo: %v", err)
	}
	if _, err := os.Stat(inst); !os.IsNotExist(err) {
		t.Errorf("instance not removed (err=%v)", err)
	}
	if after, err := os.ReadFile(registry); err != nil || bytes.Contains(after, []byte(inst)) {
		t.Fatalf("the plugin record was not pruned (err=%v):\n%s", err, after)
	}

	got := out.String()
	if !strings.Contains(got, "warning:") || !strings.Contains(got, "/somewhere/else") {
		t.Errorf("no warning for the refused trust withdrawal:\n%s", got)
	}
	if strings.Contains(got, "pruned") {
		t.Errorf("the prune's progress line was printed:\n%s", got)
	}
}
