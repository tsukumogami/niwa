package watch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// withTempHome points the trust store at a fresh temp HOME for the duration of a test
// and returns the HOME dir and the ~/.claude.json path within it.
func withTempHome(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	orig := trustHomeDir
	trustHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { trustHomeDir = orig })
	return home, filepath.Join(home, ".claude.json")
}

// readProjects reads the projects map from a ~/.claude.json (empty if absent).
func readProjects(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}
		}
		t.Fatalf("reading %s: %v", path, err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	projects, _ := config["projects"].(map[string]any)
	if projects == nil {
		return map[string]any{}
	}
	return projects
}

func TestEnsureInstanceTrusted_CreatesFileAndEntry(t *testing.T) {
	_, cfg := withTempHome(t)
	inst := t.TempDir()

	if err := EnsureInstanceTrusted(inst); err != nil {
		t.Fatalf("EnsureInstanceTrusted: %v", err)
	}

	projects := readProjects(t, cfg)
	entry, ok := projects[filepath.Clean(inst)].(map[string]any)
	if !ok {
		t.Fatalf("no trust entry for %q; projects=%v", inst, projects)
	}
	if accepted, _ := entry["hasTrustDialogAccepted"].(bool); !accepted {
		t.Errorf("hasTrustDialogAccepted must be true, got %v", entry["hasTrustDialogAccepted"])
	}
	if hooks, _ := entry["hasTrustDialogHooksAccepted"].(bool); !hooks {
		t.Errorf("hasTrustDialogHooksAccepted must be true, got %v", entry["hasTrustDialogHooksAccepted"])
	}
}

func TestEnsureInstanceTrusted_PreservesExistingContent(t *testing.T) {
	_, cfg := withTempHome(t)
	inst := t.TempDir()

	// Seed a populated config with an unrelated top-level key and a sibling project.
	seed := map[string]any{
		"numStartups": 7,
		"projects": map[string]any{
			"/some/other/project": map[string]any{
				"hasTrustDialogAccepted": true,
				"customField":            "keep me",
			},
		},
	}
	seedBytes, _ := json.MarshalIndent(seed, "", "  ")
	if err := os.WriteFile(cfg, seedBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := EnsureInstanceTrusted(inst); err != nil {
		t.Fatalf("EnsureInstanceTrusted: %v", err)
	}

	data, _ := os.ReadFile(cfg)
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("result not valid json: %v", err)
	}
	if got, _ := config["numStartups"].(float64); got != 7 {
		t.Errorf("unrelated top-level key clobbered: numStartups=%v", config["numStartups"])
	}
	projects := config["projects"].(map[string]any)
	sibling, ok := projects["/some/other/project"].(map[string]any)
	if !ok {
		t.Fatalf("sibling project entry dropped")
	}
	if sibling["customField"] != "keep me" {
		t.Errorf("sibling project custom field clobbered: %v", sibling["customField"])
	}
	if _, ok := projects[filepath.Clean(inst)].(map[string]any); !ok {
		t.Errorf("new instance entry not added")
	}
}

func TestEnsureInstanceTrusted_Idempotent(t *testing.T) {
	_, cfg := withTempHome(t)
	inst := t.TempDir()

	if err := EnsureInstanceTrusted(inst); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	if err := EnsureInstanceTrusted(inst); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	projects := readProjects(t, cfg)
	if _, ok := projects[filepath.Clean(inst)]; !ok {
		t.Errorf("entry missing after idempotent re-seed")
	}
	if len(projects) != 1 {
		t.Errorf("re-seed must not duplicate entries, got %d projects", len(projects))
	}
}

func TestRemoveInstanceTrust_RemovesOnlyItsEntry(t *testing.T) {
	_, cfg := withTempHome(t)
	inst := t.TempDir()

	// A sibling entry that must survive removal.
	seed := map[string]any{
		"projects": map[string]any{
			"/keep/this": map[string]any{"hasTrustDialogAccepted": true},
		},
	}
	seedBytes, _ := json.MarshalIndent(seed, "", "  ")
	if err := os.WriteFile(cfg, seedBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureInstanceTrusted(inst); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := RemoveInstanceTrust(inst); err != nil {
		t.Fatalf("RemoveInstanceTrust: %v", err)
	}
	projects := readProjects(t, cfg)
	if _, present := projects[filepath.Clean(inst)]; present {
		t.Errorf("instance entry still present after removal")
	}
	if _, present := projects["/keep/this"]; !present {
		t.Errorf("sibling entry dropped by removal")
	}
}

func TestRemoveInstanceTrust_MissingFileAndEntryAreNoops(t *testing.T) {
	_, cfg := withTempHome(t)
	inst := t.TempDir()

	// Missing file.
	if err := RemoveInstanceTrust(inst); err != nil {
		t.Errorf("removal against a missing config must be a no-op, got %v", err)
	}
	// Present file, missing entry.
	if err := os.WriteFile(cfg, []byte(`{"projects":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveInstanceTrust(inst); err != nil {
		t.Errorf("removal of an absent entry must be a no-op, got %v", err)
	}
}

func TestEnsureInstanceTrusted_UnresolvableHomeErrors(t *testing.T) {
	orig := trustHomeDir
	trustHomeDir = func() (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { trustHomeDir = orig })

	if err := EnsureInstanceTrusted(t.TempDir()); err == nil {
		t.Error("EnsureInstanceTrusted must error when HOME is unresolvable (so the caller falls back to hard deny)")
	}
}

func TestEnsureInstanceTrusted_UnparseableConfigErrors(t *testing.T) {
	_, cfg := withTempHome(t)
	if err := os.WriteFile(cfg, []byte(`{ not valid json `), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureInstanceTrusted(t.TempDir()); err == nil {
		t.Error("EnsureInstanceTrusted must refuse to overwrite an unparseable config")
	}
}
