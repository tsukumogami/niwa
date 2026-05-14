package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWorkspaceInstance_NestedInstance(t *testing.T) {
	tmp := t.TempDir()
	wsRoot := filepath.Join(tmp, "tsuku")
	if err := os.MkdirAll(filepath.Join(wsRoot, "tsuku-4"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	g := &GlobalConfig{
		Registry: map[string]RegistryEntry{
			"tsuku": {Root: wsRoot},
		},
	}
	ws, inst, err := ResolveWorkspaceInstance(g, filepath.Join(wsRoot, "tsuku-4"))
	if err != nil {
		t.Fatalf("ResolveWorkspaceInstance: %v", err)
	}
	if ws != "tsuku" {
		t.Errorf("workspace = %q, want %q", ws, "tsuku")
	}
	if inst != "tsuku-4" {
		t.Errorf("instance = %q, want %q", inst, "tsuku-4")
	}
}

func TestResolveWorkspaceInstance_WorkspaceRoot(t *testing.T) {
	tmp := t.TempDir()
	wsRoot := filepath.Join(tmp, "tsuku")
	if err := os.MkdirAll(wsRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	g := &GlobalConfig{
		Registry: map[string]RegistryEntry{
			"tsuku": {Root: wsRoot},
		},
	}
	ws, inst, err := ResolveWorkspaceInstance(g, wsRoot)
	if err != nil {
		t.Fatalf("ResolveWorkspaceInstance: %v", err)
	}
	if ws != "tsuku" {
		t.Errorf("workspace = %q, want %q", ws, "tsuku")
	}
	if inst != WorkspaceRootSentinel {
		t.Errorf("instance = %q, want %q (root sentinel)", inst, WorkspaceRootSentinel)
	}
}

func TestResolveWorkspaceInstance_NotUnderAny(t *testing.T) {
	tmp := t.TempDir()
	g := &GlobalConfig{
		Registry: map[string]RegistryEntry{
			"tsuku": {Root: filepath.Join(tmp, "tsuku")},
		},
	}
	_, _, err := ResolveWorkspaceInstance(g, filepath.Join(tmp, "other-thing"))
	if !errors.Is(err, ErrInstanceNotUnderWorkspace) {
		t.Errorf("err = %v, want ErrInstanceNotUnderWorkspace", err)
	}
}

func TestResolveWorkspaceInstance_LongestPrefixWins(t *testing.T) {
	// Two workspaces, one nested inside the other. The nested workspace
	// is the more specific match for an instance under it.
	tmp := t.TempDir()
	outer := filepath.Join(tmp, "outer")
	inner := filepath.Join(outer, "inner")
	if err := os.MkdirAll(filepath.Join(inner, "instance-x"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	g := &GlobalConfig{
		Registry: map[string]RegistryEntry{
			"outer": {Root: outer},
			"inner": {Root: inner},
		},
	}
	ws, inst, err := ResolveWorkspaceInstance(g, filepath.Join(inner, "instance-x"))
	if err != nil {
		t.Fatalf("ResolveWorkspaceInstance: %v", err)
	}
	if ws != "inner" {
		t.Errorf("workspace = %q, want %q (longest-prefix wins)", ws, "inner")
	}
	if inst != "instance-x" {
		t.Errorf("instance = %q, want %q", inst, "instance-x")
	}
}

func TestResolveWorkspaceInstance_EmptyRegistry(t *testing.T) {
	_, _, err := ResolveWorkspaceInstance(&GlobalConfig{}, "/anywhere")
	if err == nil {
		t.Error("err = nil, want error for empty registry")
	}
	_, _, err = ResolveWorkspaceInstance(nil, "/anywhere")
	if err == nil {
		t.Error("err = nil, want error for nil GlobalConfig")
	}
}

func TestDiscoverInstances_FindsRootAndSubInstances(t *testing.T) {
	tmp := t.TempDir()
	wsRoot := filepath.Join(tmp, "tsuku")
	// Root-instance: has .niwa/
	if err := os.MkdirAll(filepath.Join(wsRoot, ".niwa"), 0o755); err != nil {
		t.Fatalf("mkdir root .niwa: %v", err)
	}
	// Sub-instances tsuku-2, tsuku-3 with .niwa/
	for _, name := range []string{"tsuku-2", "tsuku-3"} {
		if err := os.MkdirAll(filepath.Join(wsRoot, name, ".niwa"), 0o755); err != nil {
			t.Fatalf("mkdir %s/.niwa: %v", name, err)
		}
	}
	// Sibling without .niwa/ — must be ignored.
	if err := os.MkdirAll(filepath.Join(wsRoot, "not-an-instance"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Dot-prefixed dir — must be ignored.
	if err := os.MkdirAll(filepath.Join(wsRoot, ".claude"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	got, err := DiscoverInstances(wsRoot)
	if err != nil {
		t.Fatalf("DiscoverInstances: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3 (root + tsuku-2 + tsuku-3); got %v", len(got), got)
	}
	want := map[string]bool{
		wsRoot:                              true,
		filepath.Join(wsRoot, "tsuku-2"):    true,
		filepath.Join(wsRoot, "tsuku-3"):    true,
	}
	for _, p := range got {
		if !want[p] {
			t.Errorf("unexpected instance: %q", p)
		}
		delete(want, p)
	}
	if len(want) != 0 {
		t.Errorf("missing instances: %v", want)
	}
}

func TestDiscoverInstances_MissingDir(t *testing.T) {
	tmp := t.TempDir()
	got, err := DiscoverInstances(filepath.Join(tmp, "does-not-exist"))
	if err != nil {
		t.Fatalf("DiscoverInstances on missing dir: err = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Errorf("len(got) = %d, want 0 for missing dir", len(got))
	}
}

func TestInstanceNameFromPath(t *testing.T) {
	cases := []struct {
		name     string
		ws       string
		inst     string
		want     string
		wantErr  bool
	}{
		{"sub-instance", "/ws", "/ws/sub", "sub", false},
		{"workspace-root", "/ws", "/ws", WorkspaceRootSentinel, false},
		{"trailing slash workspace", "/ws/", "/ws", WorkspaceRootSentinel, false},
		{"deep instance returns first segment", "/ws", "/ws/sub/deeper", "sub", false},
		{"outside workspace", "/ws", "/elsewhere", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := InstanceNameFromPath(tc.ws, tc.inst)
			if tc.wantErr && err == nil {
				t.Errorf("err = nil, want error")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("err = %v, want nil", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
