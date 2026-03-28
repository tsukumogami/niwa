package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestComputeStatusAllCloned(t *testing.T) {
	root := t.TempDir()
	now := time.Now().Truncate(time.Second)
	configName := "test-ws"

	// Create group/repo directories to simulate cloned repos.
	for _, path := range []string{"public/app", "public/lib"} {
		if err := os.MkdirAll(filepath.Join(root, path), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Create managed files.
	claudeMD := filepath.Join(root, "CLAUDE.md")
	if err := os.WriteFile(claudeMD, []byte("workspace content"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, err := HashFile(claudeMD)
	if err != nil {
		t.Fatal(err)
	}

	state := &InstanceState{
		SchemaVersion:  SchemaVersion,
		ConfigName:     &configName,
		InstanceName:   "test-ws",
		InstanceNumber: 1,
		Root:           root,
		Created:        now,
		LastApplied:    now,
		Repos: map[string]RepoState{
			"app": {URL: "git@github.com:org/app.git", Cloned: true},
			"lib": {URL: "git@github.com:org/lib.git", Cloned: true},
		},
		ManagedFiles: []ManagedFile{
			{Path: claudeMD, Hash: hash, Generated: now},
		},
	}

	status, err := ComputeStatus(state, root)
	if err != nil {
		t.Fatalf("ComputeStatus: %v", err)
	}

	if status.Name != "test-ws" {
		t.Errorf("Name = %q, want %q", status.Name, "test-ws")
	}
	if status.ConfigName != "test-ws" {
		t.Errorf("ConfigName = %q, want %q", status.ConfigName, "test-ws")
	}
	if status.Root != root {
		t.Errorf("Root = %q, want %q", status.Root, root)
	}
	if len(status.Repos) != 2 {
		t.Fatalf("Repos count = %d, want 2", len(status.Repos))
	}

	repoMap := map[string]string{}
	for _, r := range status.Repos {
		repoMap[r.Name] = r.Status
	}
	if repoMap["app"] != "cloned" {
		t.Errorf("app status = %q, want %q", repoMap["app"], "cloned")
	}
	if repoMap["lib"] != "cloned" {
		t.Errorf("lib status = %q, want %q", repoMap["lib"], "cloned")
	}

	if len(status.Files) != 1 {
		t.Fatalf("Files count = %d, want 1", len(status.Files))
	}
	if status.Files[0].Status != "ok" {
		t.Errorf("file status = %q, want %q", status.Files[0].Status, "ok")
	}
	if status.DriftCount != 0 {
		t.Errorf("DriftCount = %d, want 0", status.DriftCount)
	}
}

func TestComputeStatusMissingRepo(t *testing.T) {
	root := t.TempDir()
	now := time.Now().Truncate(time.Second)

	state := &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "test-ws",
		InstanceNumber: 1,
		Root:           root,
		Created:        now,
		LastApplied:    now,
		Repos: map[string]RepoState{
			"missing-repo": {URL: "git@github.com:org/missing.git", Cloned: false},
		},
	}

	status, err := ComputeStatus(state, root)
	if err != nil {
		t.Fatalf("ComputeStatus: %v", err)
	}

	if len(status.Repos) != 1 {
		t.Fatalf("Repos count = %d, want 1", len(status.Repos))
	}
	if status.Repos[0].Status != "missing" {
		t.Errorf("repo status = %q, want %q", status.Repos[0].Status, "missing")
	}
}

func TestComputeStatusDriftedFile(t *testing.T) {
	root := t.TempDir()
	now := time.Now().Truncate(time.Second)

	// Create a managed file.
	filePath := filepath.Join(root, "CLAUDE.md")
	if err := os.WriteFile(filePath, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, err := HashFile(filePath)
	if err != nil {
		t.Fatal(err)
	}

	// Modify the file to create drift.
	if err := os.WriteFile(filePath, []byte("modified"), 0o644); err != nil {
		t.Fatal(err)
	}

	state := &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "test-ws",
		InstanceNumber: 1,
		Root:           root,
		Created:        now,
		LastApplied:    now,
		Repos:          map[string]RepoState{},
		ManagedFiles: []ManagedFile{
			{Path: filePath, Hash: hash, Generated: now},
		},
	}

	status, err := ComputeStatus(state, root)
	if err != nil {
		t.Fatalf("ComputeStatus: %v", err)
	}

	if len(status.Files) != 1 {
		t.Fatalf("Files count = %d, want 1", len(status.Files))
	}
	if status.Files[0].Status != "drifted" {
		t.Errorf("file status = %q, want %q", status.Files[0].Status, "drifted")
	}
	if status.DriftCount != 1 {
		t.Errorf("DriftCount = %d, want 1", status.DriftCount)
	}
}

func TestComputeStatusRemovedFile(t *testing.T) {
	root := t.TempDir()
	now := time.Now().Truncate(time.Second)

	// Reference a file that doesn't exist.
	filePath := filepath.Join(root, "gone.md")

	state := &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "test-ws",
		InstanceNumber: 1,
		Root:           root,
		Created:        now,
		LastApplied:    now,
		Repos:          map[string]RepoState{},
		ManagedFiles: []ManagedFile{
			{Path: filePath, Hash: "sha256:abc", Generated: now},
		},
	}

	status, err := ComputeStatus(state, root)
	if err != nil {
		t.Fatalf("ComputeStatus: %v", err)
	}

	if len(status.Files) != 1 {
		t.Fatalf("Files count = %d, want 1", len(status.Files))
	}
	if status.Files[0].Status != "removed" {
		t.Errorf("file status = %q, want %q", status.Files[0].Status, "removed")
	}
	if status.DriftCount != 1 {
		t.Errorf("DriftCount = %d, want 1", status.DriftCount)
	}
}

func TestComputeStatusNilConfigName(t *testing.T) {
	root := t.TempDir()
	now := time.Now().Truncate(time.Second)

	state := &InstanceState{
		SchemaVersion:  SchemaVersion,
		ConfigName:     nil,
		InstanceName:   "detached-1",
		InstanceNumber: 1,
		Root:           root,
		Detached:       true,
		Created:        now,
		LastApplied:    now,
		Repos:          map[string]RepoState{},
	}

	status, err := ComputeStatus(state, root)
	if err != nil {
		t.Fatalf("ComputeStatus: %v", err)
	}

	if status.ConfigName != "" {
		t.Errorf("ConfigName = %q, want empty for detached", status.ConfigName)
	}
}

func TestFindRepoDirDirect(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if !findRepoDir(root, "myrepo") {
		t.Error("expected to find direct repo dir")
	}
}

func TestFindRepoDirNested(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "public", "myrepo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if !findRepoDir(root, "myrepo") {
		t.Error("expected to find nested repo dir")
	}
}

func TestFindRepoDirMissing(t *testing.T) {
	root := t.TempDir()

	if findRepoDir(root, "nonexistent") {
		t.Error("expected not to find missing repo dir")
	}
}
