package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveLoad_V3RoundTripWithConfigSource(t *testing.T) {
	dir := t.TempDir()
	want := &InstanceState{
		SchemaVersion:  SchemaVersion,
		InstanceName:   "ws-1",
		InstanceNumber: 1,
		Root:           dir,
		Created:        time.Now().UTC().Truncate(time.Second),
		LastApplied:    time.Now().UTC().Truncate(time.Second),
		Repos:          map[string]RepoState{},
		ConfigSource: &ConfigSource{
			URL:            "tsukumogami/niwa:.niwa@main",
			Host:           "github.com",
			Owner:          "tsukumogami",
			Repo:           "niwa",
			Subpath:        ".niwa",
			Ref:            "main",
			ResolvedCommit: "abc123def456",
			FetchedAt:      time.Now().UTC().Truncate(time.Second),
		},
	}
	if err := SaveState(dir, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadState(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.ConfigSource == nil {
		t.Fatal("ConfigSource was lost on round-trip")
	}
	if got.ConfigSource.URL != want.ConfigSource.URL {
		t.Errorf("URL: got %q, want %q", got.ConfigSource.URL, want.ConfigSource.URL)
	}
	if got.ConfigSource.ResolvedCommit != "abc123def456" {
		t.Errorf("ResolvedCommit: %q", got.ConfigSource.ResolvedCommit)
	}
}

func TestLoadState_V2FileLoadsAsV3WithNilConfigSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, StateDir), 0o755); err != nil {
		t.Fatal(err)
	}
	v2Body := `{
		"schema_version": 2,
		"instance_name": "ws-1",
		"instance_number": 1,
		"root": "` + dir + `",
		"created": "2026-04-22T10:00:00Z",
		"last_applied": "2026-04-22T10:00:00Z",
		"managed_files": [],
		"repos": {}
	}`
	if err := os.WriteFile(statePath(dir), []byte(v2Body), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := LoadState(dir)
	if err != nil {
		t.Fatalf("load v2: %v", err)
	}
	if state.SchemaVersion != 2 {
		t.Errorf("expected schema_version preserved at 2, got %d", state.SchemaVersion)
	}
	if state.ConfigSource != nil {
		t.Errorf("v2 file should load with nil ConfigSource, got %+v", state.ConfigSource)
	}

	// Saving rewrites at current schema; v2 is preserved unless caller bumps.
	state.SchemaVersion = SchemaVersion
	state.ConfigSource = &ConfigSource{
		URL:            "org/repo",
		Host:           "github.com",
		Owner:          "org",
		Repo:           "repo",
		ResolvedCommit: "deadbeef",
		FetchedAt:      time.Now().UTC().Truncate(time.Second),
	}
	if err := SaveState(dir, state); err != nil {
		t.Fatalf("save after migration: %v", err)
	}
	reloaded, err := LoadState(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.SchemaVersion != SchemaVersion {
		t.Errorf("schema version should bump to %d after save, got %d", SchemaVersion, reloaded.SchemaVersion)
	}
	if reloaded.ConfigSource == nil || reloaded.ConfigSource.URL != "org/repo" {
		t.Errorf("ConfigSource not persisted: %+v", reloaded.ConfigSource)
	}
}

func TestLoadState_RejectsForwardVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, StateDir), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"schema_version": 99, "instance_name": "x", "root": "/tmp", "created": "2026-04-22T10:00:00Z", "last_applied": "2026-04-22T10:00:00Z", "managed_files": [], "repos": {}}`
	originalBytes := []byte(body)
	if err := os.WriteFile(statePath(dir), originalBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadState(dir)
	if err == nil {
		t.Fatal("expected forward-version error")
	}
	if !strings.Contains(err.Error(), "99") {
		t.Errorf("error should name observed version: %v", err)
	}
	// Per PRD R25 / AC-X4: on-disk file must be byte-identical after failed load.
	got, _ := os.ReadFile(statePath(dir))
	if string(got) != body {
		t.Errorf("file mutated on failed load:\n got %q\n want %q", got, body)
	}
}

func TestConfigSource_JSONShape(t *testing.T) {
	cs := ConfigSource{
		URL:            "org/repo:.niwa",
		Host:           "github.com",
		Owner:          "org",
		Repo:           "repo",
		Subpath:        ".niwa",
		Ref:            "main",
		ResolvedCommit: "9f8e7d6c",
		FetchedAt:      time.Date(2026, 4, 23, 10, 0, 0, 0, time.UTC),
	}
	data, err := json.Marshal(cs)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	for _, key := range []string{`"url"`, `"host"`, `"owner"`, `"repo"`, `"subpath"`, `"ref"`, `"resolved_commit"`, `"fetched_at"`} {
		if !strings.Contains(out, key) {
			t.Errorf("missing key %s in JSON: %s", key, out)
		}
	}
}
