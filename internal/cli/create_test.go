package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/workspace"
)

func TestComputeInstanceName_FirstInstance(t *testing.T) {
	dir := t.TempDir()

	name, err := computeInstanceName("tsuku", "", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "tsuku" {
		t.Errorf("expected %q, got %q", "tsuku", name)
	}
}

func TestComputeInstanceName_SubsequentInstance(t *testing.T) {
	dir := t.TempDir()

	// Create the first instance directory with state.
	firstDir := filepath.Join(dir, "tsuku")
	stateDir := filepath.Join(firstDir, ".niwa")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	state := workspace.InstanceState{
		SchemaVersion:  1,
		InstanceName:   "tsuku",
		InstanceNumber: 1,
		Root:           firstDir,
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "instance.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	name, err := computeInstanceName("tsuku", "", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "tsuku-2" {
		t.Errorf("expected %q, got %q", "tsuku-2", name)
	}
}

func TestComputeInstanceName_CustomName(t *testing.T) {
	dir := t.TempDir()

	name, err := computeInstanceName("tsuku", "hotfix", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "tsuku-hotfix" {
		t.Errorf("expected %q, got %q", "tsuku-hotfix", name)
	}
}

func TestComputeInstanceName_CustomNameIgnoresExisting(t *testing.T) {
	dir := t.TempDir()

	// Even if no instances exist, --name always produces config-name.
	name, err := computeInstanceName("tsuku", "dev", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "tsuku-dev" {
		t.Errorf("expected %q, got %q", "tsuku-dev", name)
	}
}

func TestComputeInstanceName_DirExistsWithoutState(t *testing.T) {
	dir := t.TempDir()

	// Create a directory that exists but has no instance state.
	// NextInstanceNumber should return 1, so we get tsuku-1.
	firstDir := filepath.Join(dir, "tsuku")
	if err := os.MkdirAll(firstDir, 0o755); err != nil {
		t.Fatal(err)
	}

	name, err := computeInstanceName("tsuku", "", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Directory exists but no instances with state, so NextInstanceNumber returns 1.
	if name != "tsuku-1" {
		t.Errorf("expected %q, got %q", "tsuku-1", name)
	}
}

func TestFindRepoDir_SingleMatch(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "public", "niwa"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := findRepoDir(root, "niwa")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(root, "public", "niwa")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFindRepoDir_NotFound(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "public", "niwa"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := findRepoDir(root, "missing")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

func TestFindRepoDir_Ambiguous(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "public", "niwa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "private", "niwa"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := findRepoDir(root, "niwa")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("expected 'ambiguous' in error, got: %v", err)
	}
}

func TestFindRepoDir_PathTraversal(t *testing.T) {
	root := t.TempDir()

	_, err := findRepoDir(root, "../etc")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid repo name") {
		t.Errorf("expected 'invalid repo name' in error, got: %v", err)
	}

	_, err = findRepoDir(root, "foo/bar")
	if err == nil {
		t.Fatal("expected error for slash, got nil")
	}
	if !strings.Contains(err.Error(), "invalid repo name") {
		t.Errorf("expected 'invalid repo name' in error, got: %v", err)
	}
}

func TestFindRepoDir_SkipsDotDirs(t *testing.T) {
	root := t.TempDir()
	// Put repo under .niwa and .claude — should not be found.
	if err := os.MkdirAll(filepath.Join(root, ".niwa", "myrepo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".claude", "myrepo"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := findRepoDir(root, "myrepo")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}
