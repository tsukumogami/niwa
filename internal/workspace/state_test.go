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

func TestInstanceStateOverlayFieldsRoundTrip(t *testing.T) {
	dir := t.TempDir()

	state := &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "test-overlay",
		InstanceNumber: 1,
		Root:           dir,
		OverlayURL:     "acme/myconfig-overlay",
		NoOverlay:      false,
		OverlayCommit:  "abc1234def5678901234567890123456789012345",
		Created:        time.Now().Truncate(time.Second),
		LastApplied:    time.Now().Truncate(time.Second),
		Repos:          map[string]RepoState{},
	}

	if err := SaveState(dir, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	loaded, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	if loaded.OverlayURL != state.OverlayURL {
		t.Errorf("OverlayURL = %q, want %q", loaded.OverlayURL, state.OverlayURL)
	}
	if loaded.OverlayCommit != state.OverlayCommit {
		t.Errorf("OverlayCommit = %q, want %q", loaded.OverlayCommit, state.OverlayCommit)
	}
	if loaded.NoOverlay != state.NoOverlay {
		t.Errorf("NoOverlay = %v, want %v", loaded.NoOverlay, state.NoOverlay)
	}
}

func TestInstanceStateNoOverlayRoundTrip(t *testing.T) {
	dir := t.TempDir()

	state := &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "test-no-overlay",
		InstanceNumber: 1,
		Root:           dir,
		NoOverlay:      true,
		Created:        time.Now().Truncate(time.Second),
		LastApplied:    time.Now().Truncate(time.Second),
		Repos:          map[string]RepoState{},
	}

	if err := SaveState(dir, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	loaded, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	if !loaded.NoOverlay {
		t.Error("NoOverlay = false, want true")
	}
	if loaded.OverlayURL != "" {
		t.Errorf("OverlayURL = %q, want empty for no-overlay state", loaded.OverlayURL)
	}
	if loaded.OverlayCommit != "" {
		t.Errorf("OverlayCommit = %q, want empty for no-overlay state", loaded.OverlayCommit)
	}
}

func TestInstanceStateNoWorktreeDelegationRoundTrip(t *testing.T) {
	dir := t.TempDir()

	state := &InstanceState{
		SchemaVersion:        SchemaVersion,
		InstanceName:         "test-no-worktree-delegation",
		InstanceNumber:       1,
		Root:                 dir,
		NoWorktreeDelegation: true,
		Created:              time.Now().Truncate(time.Second),
		LastApplied:          time.Now().Truncate(time.Second),
		Repos:                map[string]RepoState{},
	}

	if err := SaveState(dir, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	loaded, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	if !loaded.NoWorktreeDelegation {
		t.Error("NoWorktreeDelegation = false, want true")
	}
}

func TestInstanceStateNoWorktreeDelegationOmitEmpty(t *testing.T) {
	// When the opt-out is unset (false), the key must not appear in the JSON
	// so old binaries reading new state files stay unaffected.
	dir := t.TempDir()

	state := &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "test-worktree-delegation-default",
		InstanceNumber: 1,
		Root:           dir,
		Created:        time.Now().Truncate(time.Second),
		LastApplied:    time.Now().Truncate(time.Second),
		Repos:          map[string]RepoState{},
	}

	if err := SaveState(dir, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, StateDir, StateFile))
	if err != nil {
		t.Fatalf("reading state file: %v", err)
	}

	if containsKey(string(data), "no_worktree_delegation") {
		t.Error(`JSON contains "no_worktree_delegation" when it should be omitted (zero value)`)
	}
}

func TestInstanceStateOverlayFieldsOmitEmpty(t *testing.T) {
	// When overlay fields are zero, they should not appear in the JSON output
	// (omitempty ensures backward compatibility).
	dir := t.TempDir()

	state := &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "test-no-overlay-fields",
		InstanceNumber: 1,
		Root:           dir,
		Created:        time.Now().Truncate(time.Second),
		LastApplied:    time.Now().Truncate(time.Second),
		Repos:          map[string]RepoState{},
	}

	if err := SaveState(dir, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, StateDir, StateFile))
	if err != nil {
		t.Fatalf("reading state file: %v", err)
	}

	// None of the overlay keys should appear in JSON when zero.
	for _, key := range []string{"overlay_url", "no_overlay", "overlay_commit"} {
		if containsKey(string(data), key) {
			t.Errorf("JSON contains %q when it should be omitted (zero value)", key)
		}
	}
}

// containsKey reports whether raw JSON contains the given key as a JSON string.
func containsKey(json, key string) bool {
	return len(json) > 0 && (len(key) > 0) && (func() bool {
		target := `"` + key + `"`
		for i := 0; i <= len(json)-len(target); i++ {
			if json[i:i+len(target)] == target {
				return true
			}
		}
		return false
	})()
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

func TestNoticeDisclosed(t *testing.T) {
	t.Run("nil state", func(t *testing.T) {
		if noticeDisclosed(nil, "foo") {
			t.Error("noticeDisclosed(nil, ...) must return false")
		}
	})
	t.Run("empty state", func(t *testing.T) {
		s := &InstanceState{}
		if noticeDisclosed(s, "foo") {
			t.Error("noticeDisclosed with empty DisclosedNotices must return false")
		}
	})
	t.Run("notice present", func(t *testing.T) {
		s := &InstanceState{DisclosedNotices: []string{"foo", "bar"}}
		if !noticeDisclosed(s, "foo") {
			t.Error("noticeDisclosed must return true when notice is present")
		}
		if !noticeDisclosed(s, "bar") {
			t.Error("noticeDisclosed must return true for second notice")
		}
	})
	t.Run("notice absent", func(t *testing.T) {
		s := &InstanceState{DisclosedNotices: []string{"foo"}}
		if noticeDisclosed(s, "baz") {
			t.Error("noticeDisclosed must return false when notice is absent")
		}
	})
}

func TestMergeDisclosedNotices(t *testing.T) {
	t.Run("empty added returns existing", func(t *testing.T) {
		existing := []string{"a", "b"}
		got := mergeDisclosedNotices(existing, nil)
		if len(got) != 2 || got[0] != "a" || got[1] != "b" {
			t.Errorf("got %v, want [a b]", got)
		}
	})
	t.Run("deduplicates", func(t *testing.T) {
		got := mergeDisclosedNotices([]string{"a"}, []string{"a", "b"})
		if len(got) != 2 || got[0] != "a" || got[1] != "b" {
			t.Errorf("got %v, want [a b]", got)
		}
	})
	t.Run("nil existing", func(t *testing.T) {
		got := mergeDisclosedNotices(nil, []string{"x"})
		if len(got) != 1 || got[0] != "x" {
			t.Errorf("got %v, want [x]", got)
		}
	})
	t.Run("both nil", func(t *testing.T) {
		got := mergeDisclosedNotices(nil, nil)
		if len(got) != 0 {
			t.Errorf("got %v, want []", got)
		}
	})
}

func TestDisclosedNoticesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	state := &InstanceState{
		SchemaVersion:    SchemaVersion,
		InstanceName:     "test-notices",
		InstanceNumber:   1,
		Root:             dir,
		Created:          time.Now().Truncate(time.Second),
		LastApplied:      time.Now().Truncate(time.Second),
		Repos:            map[string]RepoState{},
		DisclosedNotices: []string{"provider-shadow"},
	}
	if err := SaveState(dir, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	loaded, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(loaded.DisclosedNotices) != 1 || loaded.DisclosedNotices[0] != "provider-shadow" {
		t.Errorf("DisclosedNotices = %v, want [provider-shadow]", loaded.DisclosedNotices)
	}
}

func TestDisclosedNoticesOmitEmpty(t *testing.T) {
	dir := t.TempDir()
	state := &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "test-no-notices",
		InstanceNumber: 1,
		Root:           dir,
		Created:        time.Now().Truncate(time.Second),
		LastApplied:    time.Now().Truncate(time.Second),
		Repos:          map[string]RepoState{},
	}
	if err := SaveState(dir, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, StateDir, StateFile))
	if err != nil {
		t.Fatalf("reading state file: %v", err)
	}
	if containsKey(string(data), "disclosed_notices") {
		t.Error("disclosed_notices must be omitted from JSON when empty")
	}
}

// TestSourceKindEnvExample asserts that SourceKindEnvExample has the string
// value "env_example" and is distinct from the other two source kind constants.
func TestSourceKindEnvExample(t *testing.T) {
	if SourceKindEnvExample != "env_example" {
		t.Errorf("SourceKindEnvExample = %q, want %q", SourceKindEnvExample, "env_example")
	}
	if SourceKindEnvExample == SourceKindPlaintext {
		t.Errorf("SourceKindEnvExample must not equal SourceKindPlaintext (%q)", SourceKindPlaintext)
	}
	if SourceKindEnvExample == SourceKindVault {
		t.Errorf("SourceKindEnvExample must not equal SourceKindVault (%q)", SourceKindVault)
	}
}

// TestSaveAndLoadStateAuthSources confirms that an InstanceState
// with a non-nil AuthSources map round-trips through SaveState +
// LoadState at schema v4. PRD R11 / I3 acceptance.
func TestSaveAndLoadStateAuthSources(t *testing.T) {
	dir := t.TempDir()
	configName := "ws"
	state := &InstanceState{
		SchemaVersion:  SchemaVersion,
		ConfigName:     &configName,
		InstanceName:   "ws",
		InstanceNumber: 1,
		Root:           dir,
		Created:        time.Now(),
		LastApplied:    time.Now(),
		AuthSources: map[string]AuthSourceRecord{
			"infisical/uuid-prod": {
				Source:   "vault:personal-overlay(personal)",
				Fallback: "",
			},
			"infisical/uuid-team": {
				Source:   "local-file",
				Fallback: "vault:personal-overlay(personal)",
			},
			"infisical/uuid-anon": {
				Source:   "vault:personal-overlay",
				Fallback: "",
			},
			"infisical/uuid-cli": {
				Source:   "cli-session",
				Fallback: "",
			},
			"infisical/uuid-none": {
				Source:   "none",
				Fallback: "",
			},
		},
	}
	if err := SaveState(dir, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	loaded, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if loaded.SchemaVersion != 4 {
		t.Errorf("SchemaVersion = %d, want 4", loaded.SchemaVersion)
	}
	if len(loaded.AuthSources) != len(state.AuthSources) {
		t.Errorf("AuthSources size = %d, want %d", len(loaded.AuthSources), len(state.AuthSources))
	}
	for k, want := range state.AuthSources {
		got, ok := loaded.AuthSources[k]
		if !ok {
			t.Errorf("AuthSources[%q] missing after load", k)
			continue
		}
		if got != want {
			t.Errorf("AuthSources[%q] = %+v, want %+v", k, got, want)
		}
	}
}

// TestSaveStateOmitsAuthSourcesWhenEmpty confirms the omitempty JSON
// tag on AuthSources keeps the JSON clean when no credential
// decisions were recorded (single-org / single-default-org users).
// Together with the SchemaVersion bump this is the v3→v4 forward
// guarantee: a v4 state file written by a non-opting-in user looks
// identical to a v3 file aside from `schema_version: 4`.
func TestSaveStateOmitsAuthSourcesWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	configName := "ws"
	state := &InstanceState{
		SchemaVersion:  SchemaVersion,
		ConfigName:     &configName,
		InstanceName:   "ws",
		InstanceNumber: 1,
		Root:           dir,
		Created:        time.Now(),
		LastApplied:    time.Now(),
		// No AuthSources set.
	}
	if err := SaveState(dir, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, StateDir, StateFile))
	if err != nil {
		t.Fatalf("reading state file: %v", err)
	}
	if containsKey(string(data), "auth_sources") {
		t.Error("auth_sources must be omitted from JSON when nil/empty")
	}
}

// TestLoadStateV3WithoutAuthSources confirms the v3→v4 migration:
// a state file written by a v3-aware binary lacks the auth_sources
// key entirely, and a v4-aware LoadState handles that cleanly
// (AuthSources is nil; no error). The next SaveState rewrites the
// file at schema_version 4 (still without auth_sources because
// AuthSources stays nil until the next apply populates it).
func TestLoadStateV3WithoutAuthSources(t *testing.T) {
	dir := t.TempDir()
	niwaDir := filepath.Join(dir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Hand-craft a v3 state file (schema_version=3, no auth_sources).
	v3JSON := `{
  "schema_version": 3,
  "config_name": "ws",
  "instance_name": "ws",
  "instance_number": 1,
  "root": "` + dir + `",
  "created": "2026-01-01T00:00:00Z",
  "last_applied": "2026-01-01T00:00:00Z",
  "managed_files": [],
  "repos": {}
}`
	if err := os.WriteFile(filepath.Join(niwaDir, StateFile), []byte(v3JSON), 0o600); err != nil {
		t.Fatalf("writing v3 state: %v", err)
	}
	loaded, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState v3 file: %v", err)
	}
	if loaded.AuthSources != nil {
		t.Errorf("AuthSources should be nil after loading a v3 file, got %+v", loaded.AuthSources)
	}
	// LoadState does NOT bump schema_version on read — the loaded
	// struct still carries SchemaVersion == 3 (matching the on-disk
	// file). The apply pipeline always constructs a fresh
	// InstanceState literal with SchemaVersion: SchemaVersion (the
	// current constant), so the next SaveState writes at v4. Mirror
	// that here.
	if loaded.SchemaVersion != 3 {
		t.Errorf("loaded.SchemaVersion = %d, want 3 (LoadState should not auto-bump)", loaded.SchemaVersion)
	}
	loaded.SchemaVersion = SchemaVersion
	if err := SaveState(dir, loaded); err != nil {
		t.Fatalf("SaveState (v3→v4 rewrite): %v", err)
	}
	reloaded, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState after rewrite: %v", err)
	}
	if reloaded.SchemaVersion != 4 {
		t.Errorf("SchemaVersion after rewrite = %d, want 4", reloaded.SchemaVersion)
	}
	if reloaded.AuthSources != nil {
		t.Errorf("AuthSources after rewrite should still be nil, got %+v", reloaded.AuthSources)
	}
}
