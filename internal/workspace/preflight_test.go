package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckInitConflicts_NoConflict(t *testing.T) {
	dir := t.TempDir()

	err := CheckInitConflicts(dir)
	if err != nil {
		t.Fatalf("expected no conflict, got: %v", err)
	}
}

func TestCheckInitConflicts_WorkspaceExists(t *testing.T) {
	dir := t.TempDir()

	// Create .niwa/workspace.toml
	niwaDir := filepath.Join(dir, StateDir)
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(niwaDir, WorkspaceConfigFile), []byte("[workspace]\nname = \"test\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := CheckInitConflicts(dir)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var conflict *InitConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected InitConflictError, got %T: %v", err, err)
	}
	if !errors.Is(err, ErrWorkspaceExists) {
		t.Errorf("expected ErrWorkspaceExists, got: %v", conflict.Err)
	}
	if conflict.Suggestion == "" {
		t.Error("expected non-empty suggestion")
	}
}

func TestCheckInitConflicts_NiwaDirectoryExists(t *testing.T) {
	dir := t.TempDir()

	// Create .niwa/ without workspace.toml
	niwaDir := filepath.Join(dir, StateDir)
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	err := CheckInitConflicts(dir)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var conflict *InitConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected InitConflictError, got %T: %v", err, err)
	}
	if !errors.Is(err, ErrNiwaDirectoryExists) {
		t.Errorf("expected ErrNiwaDirectoryExists, got: %v", conflict.Err)
	}
}

func TestCheckInitConflicts_InsideInstance(t *testing.T) {
	dir := t.TempDir()

	// Create an instance marker at dir/.niwa/instance.json
	niwaDir := filepath.Join(dir, StateDir)
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(niwaDir, StateFile), []byte(`{"schema_version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Check from a subdirectory (which itself has no .niwa/)
	subDir := filepath.Join(dir, "projects", "my-tool")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	err := CheckInitConflicts(subDir)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var conflict *InitConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected InitConflictError, got %T: %v", err, err)
	}
	if !errors.Is(err, ErrInsideInstance) {
		t.Errorf("expected ErrInsideInstance, got: %v", conflict.Err)
	}
	if conflict.Detail == "" {
		t.Error("expected detail to contain instance path")
	}
}

func TestCheckInitConflicts_DetectionOrder_WorkspaceBeatsOrphanedDir(t *testing.T) {
	// When both workspace.toml and .niwa/ exist, Case 1 (ErrWorkspaceExists) wins.
	dir := t.TempDir()

	niwaDir := filepath.Join(dir, StateDir)
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(niwaDir, WorkspaceConfigFile), []byte("[workspace]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := CheckInitConflicts(dir)
	if !errors.Is(err, ErrWorkspaceExists) {
		t.Errorf("expected ErrWorkspaceExists (Case 1 priority), got: %v", err)
	}
}

func TestCheckInitConflicts_DetectionOrder_OrphanedDirBeatsInsideInstance(t *testing.T) {
	// Set up: parent is an instance, target dir has orphaned .niwa/
	// Case 3 (ErrNiwaDirectoryExists) should win over Case 2 (ErrInsideInstance).
	parentDir := t.TempDir()

	// Parent is an instance
	parentNiwa := filepath.Join(parentDir, StateDir)
	if err := os.MkdirAll(parentNiwa, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parentNiwa, StateFile), []byte(`{"schema_version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Target dir has orphaned .niwa/
	targetDir := filepath.Join(parentDir, "child")
	targetNiwa := filepath.Join(targetDir, StateDir)
	if err := os.MkdirAll(targetNiwa, 0o755); err != nil {
		t.Fatal(err)
	}

	err := CheckInitConflicts(targetDir)
	if !errors.Is(err, ErrNiwaDirectoryExists) {
		t.Errorf("expected ErrNiwaDirectoryExists (Case 3 priority over Case 2), got: %v", err)
	}
}

func TestInitConflictError_ErrorsIs(t *testing.T) {
	err := &InitConflictError{
		Err:        ErrWorkspaceExists,
		Detail:     "test detail",
		Suggestion: "test suggestion",
	}

	if !errors.Is(err, ErrWorkspaceExists) {
		t.Error("errors.Is should match the wrapped sentinel")
	}
	if errors.Is(err, ErrInsideInstance) {
		t.Error("errors.Is should not match a different sentinel")
	}
}

func TestInitConflictError_ErrorMessage(t *testing.T) {
	err := &InitConflictError{
		Err:        ErrWorkspaceExists,
		Detail:     "found .niwa/workspace.toml",
		Suggestion: "Use niwa apply",
	}

	msg := err.Error()
	if msg == "" {
		t.Fatal("error message should not be empty")
	}
}
