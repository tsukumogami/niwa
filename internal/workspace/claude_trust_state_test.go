package workspace

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/claudetrust"
)

// TestStateWithoutClaudeTrustRoundTrips checks that a state file with no
// grant loads with a nil ClaudeTrust and saves back byte-identical, with no
// claude_trust key for an older binary to trip over.
func TestStateWithoutClaudeTrustRoundTrips(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	name := "ws"
	if err := SaveState(dir, &InstanceState{
		SchemaVersion:  SchemaVersion,
		ConfigName:     &name,
		InstanceName:   "ws",
		InstanceNumber: 1,
		Root:           dir,
		Created:        now,
		LastApplied:    now,
		Repos:          map[string]RepoState{},
		TrustKeys:      []string{"/some/repo"},
	}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	path := filepath.Join(dir, StateDir, StateFile)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(before, []byte("claude_trust")) {
		t.Fatalf("state without a grant carries a claude_trust key:\n%s", before)
	}

	loaded, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if loaded.ClaudeTrust != nil {
		t.Errorf("ClaudeTrust = %+v, want nil", loaded.ClaudeTrust)
	}
	if err := SaveState(dir, loaded); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("state changed across load and save:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestStateWithClaudeTrustRoundTrips checks that a recorded grant survives a
// save and load with every field intact, under the documented JSON names, and
// without a schema version bump.
func TestStateWithClaudeTrustRoundTrips(t *testing.T) {
	dir := t.TempDir()
	grant := &claudetrust.Grant{
		ConfigFile: "/home/dev/.claude.json",
		Key:        "/work/ws/ws-1",
		Mode:       claudetrust.ModeFlagOnly,
		Source:     claudetrust.SourceReview,
	}
	if err := SaveState(dir, &InstanceState{
		SchemaVersion: SchemaVersion,
		InstanceName:  "ws-1",
		Root:          dir,
		Repos:         map[string]RepoState{},
		ClaudeTrust:   grant,
	}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, StateDir, StateFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"claude_trust"`, `"config_file": "/home/dev/.claude.json"`, `"key": "/work/ws/ws-1"`,
		`"mode": "flag-only"`, `"source": "review"`, `"schema_version": 4`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("state file lacks %s:\n%s", want, raw)
		}
	}

	loaded, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if !reflect.DeepEqual(loaded.ClaudeTrust, grant) {
		t.Errorf("ClaudeTrust = %+v, want %+v", loaded.ClaudeTrust, grant)
	}
}

// TestApplyCarriesClaudeTrustForward checks that Apply, which rebuilds state
// from scratch, keeps a recorded grant. Dropping it would strand an entry no
// destroy could withdraw.
func TestApplyCarriesClaudeTrustForward(t *testing.T) {
	f := newPostureFixture(t, "")
	f.create(t, f.writeConfig(t, ""))

	state, err := LoadState(f.instanceRoot)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	grant := &claudetrust.Grant{
		ConfigFile: filepath.Join(t.TempDir(), ".claude.json"),
		Key:        f.instanceRoot,
		Mode:       claudetrust.ModeCreated,
		Source:     claudetrust.SourceWorkspace,
	}
	state.ClaudeTrust = grant
	if err := SaveState(f.instanceRoot, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	if err := f.applier.Apply(context.Background(), f.writeConfig(t, ""), f.niwaDir, f.instanceRoot); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	after, err := LoadState(f.instanceRoot)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if !reflect.DeepEqual(after.ClaudeTrust, grant) {
		t.Errorf("after Apply: ClaudeTrust = %+v, want %+v", after.ClaudeTrust, grant)
	}
}

// TestCreateDoesNotInheritRootClaudeTrust checks that a new instance starts
// with no grant even when the workspace root's state holds one: that grant
// names the root's key, and recording it on the instance would hand its
// removal to the wrong destroy.
func TestCreateDoesNotInheritRootClaudeTrust(t *testing.T) {
	f := newPostureFixture(t, "")
	workspaceRoot := filepath.Dir(f.instanceRoot)
	rootGrant := &claudetrust.Grant{
		ConfigFile: filepath.Join(t.TempDir(), ".claude.json"),
		Key:        workspaceRoot,
		Mode:       claudetrust.ModeCreated,
		Source:     claudetrust.SourceWorkspace,
	}
	if err := SaveState(workspaceRoot, &InstanceState{
		SchemaVersion: SchemaVersion,
		Root:          workspaceRoot,
		Repos:         map[string]RepoState{},
		ClaudeTrust:   rootGrant,
	}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	f.create(t, f.writeConfig(t, ""))

	state, err := LoadState(f.instanceRoot)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.ClaudeTrust != nil {
		t.Errorf("new instance inherited a grant: %+v", state.ClaudeTrust)
	}
	rootState, err := LoadState(workspaceRoot)
	if err != nil {
		t.Fatalf("LoadState(root): %v", err)
	}
	if !reflect.DeepEqual(rootState.ClaudeTrust, rootGrant) {
		t.Errorf("root grant changed: %+v, want %+v", rootState.ClaudeTrust, rootGrant)
	}
}
