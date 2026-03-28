package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/config"
)

// setupWorkspace creates a workspace root with .niwa/workspace.toml and the
// given named instances as subdirectories with state markers.
func setupWorkspace(t *testing.T, names []string) (workspaceRoot string) {
	t.Helper()
	root := t.TempDir()

	// Create workspace config.
	configDir := filepath.Join(root, config.ConfigDir)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configContent := "[workspace]\nname = \"test-ws\"\n"
	if err := os.WriteFile(filepath.Join(configDir, config.ConfigFile), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create instances.
	for i, name := range names {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		state := &InstanceState{
			SchemaVersion:  SchemaVersion,
			InstanceName:   name,
			InstanceNumber: i + 1,
			Root:           dir,
			Created:        time.Now(),
			LastApplied:    time.Now(),
			Repos:          map[string]RepoState{},
		}
		if err := SaveState(dir, state); err != nil {
			t.Fatal(err)
		}
	}

	return root
}

func TestResolveApplyScope_SingleFromInstance(t *testing.T) {
	root := setupWorkspace(t, []string{"ws-1", "ws-2"})
	instanceDir := filepath.Join(root, "ws-1")

	// Resolve from inside an instance directory.
	scope, err := ResolveApplyScope(instanceDir, "")
	if err != nil {
		t.Fatalf("ResolveApplyScope: %v", err)
	}

	if scope.Mode != ApplySingle {
		t.Errorf("Mode = %d, want ApplySingle (%d)", scope.Mode, ApplySingle)
	}
	if len(scope.Instances) != 1 {
		t.Fatalf("Instances count = %d, want 1", len(scope.Instances))
	}
	if scope.Instances[0] != instanceDir {
		t.Errorf("Instances[0] = %q, want %q", scope.Instances[0], instanceDir)
	}
	if scope.Config == "" {
		t.Error("Config should be set when workspace.toml exists")
	}
}

func TestResolveApplyScope_SingleFromNestedDir(t *testing.T) {
	root := setupWorkspace(t, []string{"ws-1"})
	instanceDir := filepath.Join(root, "ws-1")
	nested := filepath.Join(instanceDir, "sub", "deep")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	scope, err := ResolveApplyScope(nested, "")
	if err != nil {
		t.Fatalf("ResolveApplyScope: %v", err)
	}

	if scope.Mode != ApplySingle {
		t.Errorf("Mode = %d, want ApplySingle (%d)", scope.Mode, ApplySingle)
	}
	if scope.Instances[0] != instanceDir {
		t.Errorf("Instances[0] = %q, want %q", scope.Instances[0], instanceDir)
	}
}

func TestResolveApplyScope_AllFromRoot(t *testing.T) {
	root := setupWorkspace(t, []string{"ws-1", "ws-2"})

	// Resolve from workspace root (not inside any instance).
	scope, err := ResolveApplyScope(root, "")
	if err != nil {
		t.Fatalf("ResolveApplyScope: %v", err)
	}

	if scope.Mode != ApplyAll {
		t.Errorf("Mode = %d, want ApplyAll (%d)", scope.Mode, ApplyAll)
	}
	if len(scope.Instances) != 2 {
		t.Fatalf("Instances count = %d, want 2", len(scope.Instances))
	}
	if scope.Config == "" {
		t.Error("Config should be set")
	}
}

func TestResolveApplyScope_AllFromRootNoInstances(t *testing.T) {
	root := setupWorkspace(t, nil)

	scope, err := ResolveApplyScope(root, "")
	if err != nil {
		t.Fatalf("ResolveApplyScope: %v", err)
	}

	if scope.Mode != ApplyAll {
		t.Errorf("Mode = %d, want ApplyAll (%d)", scope.Mode, ApplyAll)
	}
	if len(scope.Instances) != 0 {
		t.Errorf("Instances count = %d, want 0", len(scope.Instances))
	}
}

func TestResolveApplyScope_NamedFound(t *testing.T) {
	root := setupWorkspace(t, []string{"ws-1", "ws-2"})
	targetDir := filepath.Join(root, "ws-2")

	scope, err := ResolveApplyScope(root, "ws-2")
	if err != nil {
		t.Fatalf("ResolveApplyScope: %v", err)
	}

	if scope.Mode != ApplyNamed {
		t.Errorf("Mode = %d, want ApplyNamed (%d)", scope.Mode, ApplyNamed)
	}
	if len(scope.Instances) != 1 {
		t.Fatalf("Instances count = %d, want 1", len(scope.Instances))
	}
	if scope.Instances[0] != targetDir {
		t.Errorf("Instances[0] = %q, want %q", scope.Instances[0], targetDir)
	}
}

func TestResolveApplyScope_NamedFromInsideInstance(t *testing.T) {
	root := setupWorkspace(t, []string{"ws-1", "ws-2"})

	// Run from inside ws-1 but request ws-2 by name.
	scope, err := ResolveApplyScope(filepath.Join(root, "ws-1"), "ws-2")
	if err != nil {
		t.Fatalf("ResolveApplyScope: %v", err)
	}

	if scope.Mode != ApplyNamed {
		t.Errorf("Mode = %d, want ApplyNamed (%d)", scope.Mode, ApplyNamed)
	}
	targetDir := filepath.Join(root, "ws-2")
	if scope.Instances[0] != targetDir {
		t.Errorf("Instances[0] = %q, want %q", scope.Instances[0], targetDir)
	}
}

func TestResolveApplyScope_NamedNotFound(t *testing.T) {
	root := setupWorkspace(t, []string{"ws-1", "ws-2"})

	_, err := ResolveApplyScope(root, "nonexistent")
	if err == nil {
		t.Fatal("expected error for non-existent instance name")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention the requested name: %v", err)
	}
	if !strings.Contains(err.Error(), "ws-1") || !strings.Contains(err.Error(), "ws-2") {
		t.Errorf("error should list available instances: %v", err)
	}
}

func TestResolveApplyScope_NamedNoInstances(t *testing.T) {
	root := setupWorkspace(t, nil)

	_, err := ResolveApplyScope(root, "anything")
	if err == nil {
		t.Fatal("expected error when no instances exist")
	}
	if !strings.Contains(err.Error(), "no instances exist") {
		t.Errorf("error should mention no instances: %v", err)
	}
}

func TestResolveApplyScope_NoWorkspace(t *testing.T) {
	dir := t.TempDir()

	_, err := ResolveApplyScope(dir, "")
	if err == nil {
		t.Fatal("expected error when not in a workspace")
	}
}

func TestResolveApplyScope_NamedNoWorkspace(t *testing.T) {
	dir := t.TempDir()

	_, err := ResolveApplyScope(dir, "some-instance")
	if err == nil {
		t.Fatal("expected error when not in a workspace")
	}
}
