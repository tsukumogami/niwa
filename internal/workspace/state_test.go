package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveAndLoadState(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)
	configName := "my-workspace"

	state := &InstanceState{
		SchemaVersion:  SchemaVersion,
		ConfigName:     &configName,
		InstanceName:   "my-workspace-1",
		InstanceNumber: 1,
		Root:           dir,
		Created:        now,
		LastApplied:    now,
		ManagedFiles: []ManagedFile{
			{
				Path:        filepath.Join(dir, "CLAUDE.md"),
				ContentHash: "sha256:abc123",
				Generated:   now,
			},
		},
		Repos: map[string]RepoState{
			"app": {URL: "git@github.com:org/app.git", Cloned: true},
		},
	}

	if err := SaveState(dir, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	// Verify the file was created.
	if _, err := os.Stat(filepath.Join(dir, StateDir, StateFile)); err != nil {
		t.Fatalf("state file not created: %v", err)
	}

	loaded, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	if loaded.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", loaded.SchemaVersion, SchemaVersion)
	}
	if *loaded.ConfigName != configName {
		t.Errorf("ConfigName = %q, want %q", *loaded.ConfigName, configName)
	}
	if loaded.InstanceName != "my-workspace-1" {
		t.Errorf("InstanceName = %q, want %q", loaded.InstanceName, "my-workspace-1")
	}
	if loaded.InstanceNumber != 1 {
		t.Errorf("InstanceNumber = %d, want %d", loaded.InstanceNumber, 1)
	}
	if loaded.Root != dir {
		t.Errorf("Root = %q, want %q", loaded.Root, dir)
	}
	if len(loaded.ManagedFiles) != 1 {
		t.Fatalf("ManagedFiles count = %d, want 1", len(loaded.ManagedFiles))
	}
	if loaded.ManagedFiles[0].ContentHash != "sha256:abc123" {
		t.Errorf("ManagedFiles[0].ContentHash = %q, want %q", loaded.ManagedFiles[0].ContentHash, "sha256:abc123")
	}
	if len(loaded.Repos) != 1 {
		t.Fatalf("Repos count = %d, want 1", len(loaded.Repos))
	}
	if !loaded.Repos["app"].Cloned {
		t.Error("Repos[app].Cloned = false, want true")
	}
}

func TestSaveStateCreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "nested", "instance")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	state := &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "test",
		InstanceNumber: 1,
		Root:           subDir,
		Created:        time.Now(),
		LastApplied:    time.Now(),
		Repos:          map[string]RepoState{},
	}

	if err := SaveState(subDir, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	if _, err := os.Stat(filepath.Join(subDir, StateDir, StateFile)); err != nil {
		t.Fatalf("state file not created in nested dir: %v", err)
	}
}

func TestLoadStateNotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadState(dir)
	if err == nil {
		t.Fatal("expected error loading non-existent state")
	}
}

func TestLoadStateInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, StateDir)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, StateFile), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadState(dir)
	if err == nil {
		t.Fatal("expected error loading invalid JSON")
	}
}

func TestStateUpdatePreservesCreated(t *testing.T) {
	dir := t.TempDir()
	created := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	state := &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "test",
		InstanceNumber: 1,
		Root:           dir,
		Created:        created,
		LastApplied:    created,
		Repos:          map[string]RepoState{},
	}

	if err := SaveState(dir, state); err != nil {
		t.Fatal(err)
	}

	// Update state with new LastApplied.
	state.LastApplied = time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	state.ManagedFiles = []ManagedFile{
		{Path: "/some/file", ContentHash: "sha256:aaa", Generated: state.LastApplied},
	}

	if err := SaveState(dir, state); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}

	if !loaded.Created.Equal(created) {
		t.Errorf("Created changed from %v to %v", created, loaded.Created)
	}
	if loaded.LastApplied.Equal(created) {
		t.Error("LastApplied should have been updated")
	}
	if len(loaded.ManagedFiles) != 1 {
		t.Errorf("ManagedFiles count = %d, want 1", len(loaded.ManagedFiles))
	}
}

func TestDetachedState(t *testing.T) {
	dir := t.TempDir()

	state := &InstanceState{
		SchemaVersion:  SchemaVersion,
		ConfigName:     nil,
		InstanceName:   "detached-1",
		InstanceNumber: 1,
		Root:           dir,
		Detached:       true,
		Created:        time.Now(),
		LastApplied:    time.Now(),
		Repos:          map[string]RepoState{},
	}

	if err := SaveState(dir, state); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}

	if loaded.ConfigName != nil {
		t.Errorf("ConfigName = %v, want nil for detached", loaded.ConfigName)
	}
	if !loaded.Detached {
		t.Error("Detached = false, want true")
	}
}

func TestDiscoverInstance(t *testing.T) {
	root := t.TempDir()

	// Create instance at root/workspace-1.
	instanceDir := filepath.Join(root, "workspace-1")
	if err := os.MkdirAll(filepath.Join(instanceDir, StateDir), 0o755); err != nil {
		t.Fatal(err)
	}

	state := &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "workspace-1",
		InstanceNumber: 1,
		Root:           instanceDir,
		Created:        time.Now(),
		LastApplied:    time.Now(),
		Repos:          map[string]RepoState{},
	}
	if err := SaveState(instanceDir, state); err != nil {
		t.Fatal(err)
	}

	// Create a nested directory structure.
	nested := filepath.Join(instanceDir, "public", "app", "src")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	// Discovery from nested dir should find the instance.
	found, err := DiscoverInstance(nested)
	if err != nil {
		t.Fatalf("DiscoverInstance: %v", err)
	}
	if found != instanceDir {
		t.Errorf("DiscoverInstance = %q, want %q", found, instanceDir)
	}

	// Discovery from the instance dir itself should work.
	found, err = DiscoverInstance(instanceDir)
	if err != nil {
		t.Fatalf("DiscoverInstance from instance dir: %v", err)
	}
	if found != instanceDir {
		t.Errorf("DiscoverInstance = %q, want %q", found, instanceDir)
	}
}

func TestDiscoverInstanceNotFound(t *testing.T) {
	dir := t.TempDir()

	_, err := DiscoverInstance(dir)
	if err == nil {
		t.Fatal("expected error when no instance exists")
	}
}

func TestEnumerateInstances(t *testing.T) {
	root := t.TempDir()

	// Create two instances and one non-instance directory.
	for _, name := range []string{"ws-1", "ws-2"} {
		dir := filepath.Join(root, name)
		state := &InstanceState{
			SchemaVersion:  SchemaVersion,
			InstanceName:   name,
			InstanceNumber: 1,
			Root:           dir,
			Created:        time.Now(),
			LastApplied:    time.Now(),
			Repos:          map[string]RepoState{},
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := SaveState(dir, state); err != nil {
			t.Fatal(err)
		}
	}
	// Non-instance directory.
	if err := os.MkdirAll(filepath.Join(root, "not-instance"), 0o755); err != nil {
		t.Fatal(err)
	}

	instances, err := EnumerateInstances(root)
	if err != nil {
		t.Fatalf("EnumerateInstances: %v", err)
	}

	if len(instances) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(instances))
	}

	names := map[string]bool{}
	for _, dir := range instances {
		names[filepath.Base(dir)] = true
	}
	if !names["ws-1"] || !names["ws-2"] {
		t.Errorf("expected ws-1 and ws-2, got %v", names)
	}
}

func TestEnumerateInstancesEmpty(t *testing.T) {
	root := t.TempDir()

	instances, err := EnumerateInstances(root)
	if err != nil {
		t.Fatalf("EnumerateInstances: %v", err)
	}
	if len(instances) != 0 {
		t.Errorf("expected 0 instances, got %d", len(instances))
	}
}

func TestNextInstanceNumber(t *testing.T) {
	root := t.TempDir()

	// No instances: should return 1.
	num, err := NextInstanceNumber(root)
	if err != nil {
		t.Fatalf("NextInstanceNumber: %v", err)
	}
	if num != 1 {
		t.Errorf("NextInstanceNumber = %d, want 1", num)
	}

	// Create instances with numbers 1 and 3 (gap at 2).
	for _, n := range []int{1, 3} {
		dir := filepath.Join(root, fmt.Sprintf("ws-%d", n))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		state := &InstanceState{
			SchemaVersion:  SchemaVersion,
			InstanceName:   "ws",
			InstanceNumber: n,
			Root:           dir,
			Created:        time.Now(),
			LastApplied:    time.Now(),
			Repos:          map[string]RepoState{},
		}
		if err := SaveState(dir, state); err != nil {
			t.Fatal(err)
		}
	}

	// Should fill the gap at 2, not jump to 4.
	num, err = NextInstanceNumber(root)
	if err != nil {
		t.Fatalf("NextInstanceNumber: %v", err)
	}
	if num != 2 {
		t.Errorf("NextInstanceNumber = %d, want 2 (should fill gap)", num)
	}
}

func TestNextInstanceNumber_FillsDeletedGap(t *testing.T) {
	root := t.TempDir()

	// Simulate instances 1, 2, 4, 5 existing (3 was deleted).
	for _, n := range []int{1, 2, 4, 5} {
		dir := filepath.Join(root, fmt.Sprintf("ws-%d", n))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := SaveState(dir, &InstanceState{
			SchemaVersion:  SchemaVersion,
			InstanceName:   "ws",
			InstanceNumber: n,
			Root:           dir,
			Created:        time.Now(),
			LastApplied:    time.Now(),
			Repos:          map[string]RepoState{},
		}); err != nil {
			t.Fatal(err)
		}
	}

	num, err := NextInstanceNumber(root)
	if err != nil {
		t.Fatalf("NextInstanceNumber: %v", err)
	}
	if num != 3 {
		t.Errorf("NextInstanceNumber = %d, want 3 (should fill gap left by deleted instance)", num)
	}
}

func TestNextInstanceNumber_Contiguous(t *testing.T) {
	root := t.TempDir()

	// Instances 1, 2, 3, 4 with no gaps.
	for _, n := range []int{1, 2, 3, 4} {
		dir := filepath.Join(root, fmt.Sprintf("ws-%d", n))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := SaveState(dir, &InstanceState{
			SchemaVersion:  SchemaVersion,
			InstanceName:   "ws",
			InstanceNumber: n,
			Root:           dir,
			Created:        time.Now(),
			LastApplied:    time.Now(),
			Repos:          map[string]RepoState{},
		}); err != nil {
			t.Fatal(err)
		}
	}

	num, err := NextInstanceNumber(root)
	if err != nil {
		t.Fatalf("NextInstanceNumber: %v", err)
	}
	if num != 5 {
		t.Errorf("NextInstanceNumber = %d, want 5", num)
	}
}

func TestHashFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := "hello world\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	hash, err := HashFile(path)
	if err != nil {
		t.Fatalf("HashFile: %v", err)
	}

	if len(hash) == 0 {
		t.Fatal("hash is empty")
	}
	if hash[:7] != "sha256:" {
		t.Errorf("hash missing prefix: %s", hash)
	}

	// Same content should produce the same hash.
	path2 := filepath.Join(dir, "test2.txt")
	if err := os.WriteFile(path2, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	hash2, err := HashFile(path2)
	if err != nil {
		t.Fatalf("HashFile: %v", err)
	}
	if hash != hash2 {
		t.Errorf("same content produced different hashes: %s vs %s", hash, hash2)
	}

	// Different content should produce a different hash.
	path3 := filepath.Join(dir, "test3.txt")
	if err := os.WriteFile(path3, []byte("different\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	hash3, err := HashFile(path3)
	if err != nil {
		t.Fatal(err)
	}
	if hash == hash3 {
		t.Error("different content produced same hash")
	}
}

func TestHashFileNotFound(t *testing.T) {
	_, err := HashFile("/nonexistent/file")
	if err == nil {
		t.Fatal("expected error hashing non-existent file")
	}
}

func TestCheckDriftNoChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.md")
	if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	hash, err := HashFile(path)
	if err != nil {
		t.Fatal(err)
	}

	mf := ManagedFile{
		Path:        path,
		ContentHash: hash,
		Generated:   time.Now(),
	}

	result, err := CheckDrift(mf)
	if err != nil {
		t.Fatalf("CheckDrift: %v", err)
	}
	if result.Drifted() {
		t.Error("expected no drift, but got drift")
	}
}

func TestCheckDriftChanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.md")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	hash, err := HashFile(path)
	if err != nil {
		t.Fatal(err)
	}

	mf := ManagedFile{
		Path:        path,
		ContentHash: hash,
		Generated:   time.Now(),
	}

	// Modify the file.
	if err := os.WriteFile(path, []byte("modified"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := CheckDrift(mf)
	if err != nil {
		t.Fatalf("CheckDrift: %v", err)
	}
	if !result.Drifted() {
		t.Error("expected drift after modification")
	}
	if result.FileRemoved {
		t.Error("file was not removed")
	}
	if result.Expected != hash {
		t.Errorf("Expected = %q, want %q", result.Expected, hash)
	}
	if result.Actual == hash {
		t.Error("Actual should differ from Expected")
	}
}

func TestCheckDriftFileRemoved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.md")

	mf := ManagedFile{
		Path:        path,
		ContentHash: "sha256:abc123",
		Generated:   time.Now(),
	}

	result, err := CheckDrift(mf)
	if err != nil {
		t.Fatalf("CheckDrift: %v", err)
	}
	if !result.Drifted() {
		t.Error("expected drift for removed file")
	}
	if !result.FileRemoved {
		t.Error("FileRemoved should be true")
	}
}

func TestValidName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"plain", "tsuku", true},
		{"hyphen", "my-repo", true},
		{"underscore", "my_repo", true},
		{"dots", "my.repo.v2", true},
		{"unicode-letters", "プロジェクト", true},
		{"empty", "", true},
		{"tab", "bad\tname", false},
		{"newline", "bad\nname", false},
		{"null", "bad\x00name", false},
		{"ascii-control", "bad\x1fname", false},
		{"delete", "bad\x7fname", false},
		{"bidi-override-rtl", "file\u202e.txt", false},
		{"zero-width-joiner", "rep\u200do", false},
		{"bom", "\ufeffrepo", false},
		{"line-separator", "bad\u2028name", false},
		{"paragraph-separator", "bad\u2029name", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidName(tc.in); got != tc.want {
				t.Errorf("ValidName(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestEnumerateInstancesSkipsInvalidNames(t *testing.T) {
	root := t.TempDir()

	// Legitimate instance.
	goodDir := filepath.Join(root, "ws-1")
	if err := os.MkdirAll(goodDir, 0o755); err != nil {
		t.Fatal(err)
	}
	state := &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "ws-1",
		InstanceNumber: 1,
		Root:           goodDir,
		Created:        time.Now(),
		LastApplied:    time.Now(),
		Repos:          map[string]RepoState{},
	}
	if err := SaveState(goodDir, state); err != nil {
		t.Fatal(err)
	}

	// Planted bogus instance with a name containing a bidi-override codepoint.
	badName := "evil\u202etxt"
	badDir := filepath.Join(root, badName)
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		// Some filesystems may reject; skip the rest of this check without failing.
		t.Logf("skipping planted-bad-name case: %v", err)
	} else {
		state2 := *state
		state2.InstanceName = badName
		state2.Root = badDir
		if err := SaveState(badDir, &state2); err != nil {
			t.Fatal(err)
		}
	}

	instances, err := EnumerateInstances(root)
	if err != nil {
		t.Fatalf("EnumerateInstances: %v", err)
	}

	for _, dir := range instances {
		base := filepath.Base(dir)
		if !ValidName(base) {
			t.Errorf("EnumerateInstances returned entry with invalid name: %q", base)
		}
	}
	// At least the legitimate instance should be present.
	if len(instances) == 0 {
		t.Fatal("expected at least one legitimate instance")
	}
}

func TestEnumerateReposEmpty(t *testing.T) {
	root := t.TempDir()
	names, err := EnumerateRepos(root)
	if err != nil {
		t.Fatalf("EnumerateRepos: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("expected no repos, got %v", names)
	}
}

func TestEnumerateReposMissingRoot(t *testing.T) {
	names, err := EnumerateRepos(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatalf("expected error for missing root, got nil (names=%v)", names)
	}
	if names != nil {
		t.Errorf("expected nil slice on error, got %v", names)
	}
}

func TestEnumerateReposHappyPath(t *testing.T) {
	root := t.TempDir()

	layout := map[string][]string{
		"group-a": {"api", "web", "cli"},
		"group-b": {"sdk"},
	}
	for group, repos := range layout {
		for _, repo := range repos {
			if err := os.MkdirAll(filepath.Join(root, group, repo), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}

	names, err := EnumerateRepos(root)
	if err != nil {
		t.Fatalf("EnumerateRepos: %v", err)
	}

	want := []string{"api", "cli", "sdk", "web"}
	if len(names) != len(want) {
		t.Fatalf("length mismatch: got %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("names[%d] = %q, want %q (full: %v)", i, names[i], want[i], names)
		}
	}
}

func TestEnumerateReposSkipsControlDirsAndDotfiles(t *testing.T) {
	root := t.TempDir()

	// Legitimate group with a repo.
	if err := os.MkdirAll(filepath.Join(root, "group", "api"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Reserved control dirs must be skipped at the top level.
	if err := os.MkdirAll(filepath.Join(root, ".niwa", "should-not-show"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".claude", "should-not-show"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Dot-prefixed groups skipped.
	if err := os.MkdirAll(filepath.Join(root, ".hidden-group", "not-visible"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Dot-prefixed repos inside a legitimate group skipped.
	if err := os.MkdirAll(filepath.Join(root, "group", ".hidden-repo"), 0o755); err != nil {
		t.Fatal(err)
	}

	names, err := EnumerateRepos(root)
	if err != nil {
		t.Fatalf("EnumerateRepos: %v", err)
	}

	if len(names) != 1 || names[0] != "api" {
		t.Errorf("expected [api], got %v", names)
	}
}

func TestEnumerateReposDedupsCrossGroup(t *testing.T) {
	root := t.TempDir()

	// Same repo name in two groups -> appears once.
	for _, group := range []string{"group-a", "group-b"} {
		if err := os.MkdirAll(filepath.Join(root, group, "shared"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	names, err := EnumerateRepos(root)
	if err != nil {
		t.Fatalf("EnumerateRepos: %v", err)
	}

	if len(names) != 1 || names[0] != "shared" {
		t.Errorf("expected [shared], got %v", names)
	}
}

func TestEnumerateReposFiltersInvalidNames(t *testing.T) {
	root := t.TempDir()

	// Legitimate group with a legitimate repo.
	if err := os.MkdirAll(filepath.Join(root, "group", "api"), 0o755); err != nil {
		t.Fatal(err)
	}

	// A repo containing a bidi-override codepoint -- must be filtered.
	badName := "evil\u202ename"
	if err := os.MkdirAll(filepath.Join(root, "group", badName), 0o755); err != nil {
		t.Logf("skipping planted-bad-name case: %v", err)
	}

	// A group name containing a control char -- its contents must be filtered.
	badGroup := "group\u2028b"
	if err := os.MkdirAll(filepath.Join(root, badGroup, "shouldnt-show"), 0o755); err != nil {
		t.Logf("skipping planted-bad-group case: %v", err)
	}

	names, err := EnumerateRepos(root)
	if err != nil {
		t.Fatalf("EnumerateRepos: %v", err)
	}

	for _, n := range names {
		if !ValidName(n) {
			t.Errorf("returned invalid name: %q", n)
		}
	}
	if len(names) < 1 || names[0] != "api" {
		t.Errorf("expected at least [api], got %v", names)
	}
}
