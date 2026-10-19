package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hookFixture builds a workspace root holding instance "alpha" (repos niwa
// and koto in group public, plus a configured repo "ghost" with no clone) and
// instance "beta" (one repo, solo).
type hookFixture struct {
	workspaceRoot string
	alpha         string
	beta          string
}

func newHookFixture(t *testing.T) hookFixture {
	t.Helper()
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, StateDir))
	if err := os.WriteFile(filepath.Join(root, StateDir, WorkspaceConfigFile), []byte("[workspace]\nname = \"ws\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// `niwa init` writes unnamed init state at the workspace root; the
	// resolver must not mistake it for an instance.
	if err := SaveState(root, &InstanceState{SchemaVersion: SchemaVersion}); err != nil {
		t.Fatal(err)
	}

	alpha := filepath.Join(root, "alpha")
	for _, repo := range []string{"niwa", "koto"} {
		mustMkdir(t, filepath.Join(alpha, "public", repo, ".git"))
	}
	if err := SaveState(alpha, &InstanceState{
		SchemaVersion: SchemaVersion,
		InstanceName:  "alpha",
		Repos: map[string]RepoState{
			"niwa":  {Cloned: true},
			"koto":  {Cloned: true},
			"ghost": {Cloned: true},
		},
	}); err != nil {
		t.Fatal(err)
	}

	beta := filepath.Join(root, "beta")
	mustMkdir(t, filepath.Join(beta, "private", "solo", ".git"))
	if err := SaveState(beta, &InstanceState{SchemaVersion: SchemaVersion, InstanceName: "beta"}); err != nil {
		t.Fatal(err)
	}
	return hookFixture{workspaceRoot: root, alpha: alpha, beta: beta}
}

func TestResolveHookTarget(t *testing.T) {
	f := newHookFixture(t)
	niwaDir := filepath.Join(f.alpha, "public", "niwa")
	kotoDir := filepath.Join(f.alpha, "public", "koto")

	cases := []struct {
		name     string
		anchor   string
		cwd      string
		hookName string
		wantRepo string
		wantSlug string
		wantErr  string
	}{
		{"repo prefix at instance root", f.alpha, f.alpha, "niwa/fix-x", "niwa", "fix-x", ""},
		{"group and repo prefix", f.alpha, f.alpha, "public/niwa/fix-x", "niwa", "fix-x", ""},
		{"nested slug", f.alpha, f.alpha, "niwa/fix/x", "niwa", "fix/x", ""},
		{"prefix wins over cwd", f.alpha, kotoDir, "niwa/fix-x", "niwa", "fix-x", ""},
		{"cwd picks repo without prefix", f.alpha, kotoDir, "fix-x", "koto", "fix-x", ""},
		{"repo-launched session", niwaDir, niwaDir, "fix-x", "niwa", "fix-x", ""},
		{"group dir is no repo", f.alpha, filepath.Join(f.alpha, "public"), "fix-x", "", "", "no repo selected"},
		{"multi-repo root without prefix", f.alpha, f.alpha, "fix-x", "", "", "no repo selected"},
		{"unknown prefix", f.alpha, f.alpha, "nosuchrepo/fix-x", "", "", "no repo selected"},
		{"unknown group and repo", f.alpha, f.alpha, "public/nosuch/fix-x", "", "", "no repo selected"},
		{"empty slug", f.alpha, f.alpha, "niwa/", "", "", "no repo selected"},
		{"case differs", f.alpha, f.alpha, "Niwa/fix-x", "", "", "no repo selected"},
		{"missing clone", f.alpha, f.alpha, "ghost/fix-x", "", "", "clone is missing"},
		{"missing clone, group form", f.alpha, f.alpha, "public/ghost/fix-x", "", "", "clone is missing"},
		{"single-repo instance", f.beta, f.beta, "fix-x", "solo", "fix-x", ""},
		{"workspace-root launch moved into instance", f.workspaceRoot, f.alpha, "koto/y", "koto", "y", ""},
		{"workspace-root launch not moved", f.workspaceRoot, f.workspaceRoot, "niwa/fix-x", "", "", "not inside a niwa instance"},
		{"cwd outside the launch instance", f.alpha, f.beta, "solo/x", "", "", "outside the instance"},
		{"empty anchor is refused", "", kotoDir, "fix-x", "", "", "launched in"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveHookTarget(tc.anchor, tc.cwd, tc.hookName)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Repo != tc.wantRepo || got.Slug != tc.wantSlug {
				t.Errorf("got repo %q slug %q, want repo %q slug %q", got.Repo, got.Slug, tc.wantRepo, tc.wantSlug)
			}
		})
	}
}

func TestResolveHookTarget_RefusalListsRepos(t *testing.T) {
	f := newHookFixture(t)
	_, err := ResolveHookTarget(f.alpha, f.alpha, "fix-x")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	for _, want := range []string{"no repo selected", "niwa", "koto", "koto/<slug>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q lacks %q", err, want)
		}
	}
}

func TestResolveHookTarget_WorkspaceRootListsInstances(t *testing.T) {
	f := newHookFixture(t)
	_, err := ResolveHookTarget(f.workspaceRoot, f.workspaceRoot, "fix-x")
	if err == nil || !strings.Contains(err.Error(), "alpha") || !strings.Contains(err.Error(), "beta") {
		t.Fatalf("refusal should name both instances, got %v", err)
	}

	empty := t.TempDir()
	mustMkdir(t, filepath.Join(empty, StateDir))
	if err := os.WriteFile(filepath.Join(empty, StateDir, WorkspaceConfigFile), []byte("[workspace]\nname = \"e\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = ResolveHookTarget(empty, empty, "fix-x")
	if err == nil || !strings.Contains(err.Error(), "no instances") {
		t.Fatalf("refusal should say there are no instances, got %v", err)
	}
}

func TestResolveHookTarget_OptedOutInstance(t *testing.T) {
	f := newHookFixture(t)
	state, err := LoadState(f.alpha)
	if err != nil {
		t.Fatal(err)
	}
	state.NoWorktreeDelegation = true
	if err := SaveState(f.alpha, state); err != nil {
		t.Fatal(err)
	}
	_, err = ResolveHookTarget(f.workspaceRoot, f.alpha, "niwa/fix-x")
	if !errors.Is(err, ErrDelegationOptedOut) {
		t.Fatalf("err = %v, want ErrDelegationOptedOut", err)
	}
}

func TestResolveHookTarget_PlantedInstanceIsRefused(t *testing.T) {
	f := newHookFixture(t)
	// A session launched in alpha points cwd at a directory it planted with
	// its own instance marker.
	planted := filepath.Join(t.TempDir(), "planted")
	mustMkdir(t, filepath.Join(planted, "g", "evil", ".git"))
	if err := SaveState(planted, &InstanceState{SchemaVersion: SchemaVersion}); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveHookTarget(f.alpha, filepath.Join(planted, "g", "evil"), "evil/x"); err == nil {
		t.Fatal("a cwd outside the launch instance must be refused")
	}
}

// TestHookAnchor_FallsBackToWorkingDirectory checks that with no
// CLAUDE_PROJECT_DIR the anchor is the process's working directory, never the
// payload's cwd.
func TestHookAnchor_FallsBackToWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(prev) }()
	got, err := canonicalize(HookAnchor())
	if err != nil {
		t.Fatal(err)
	}
	want, _ := canonicalize(dir)
	if got != want {
		t.Errorf("HookAnchor() = %q, want the working directory %q", got, want)
	}

	t.Setenv("CLAUDE_PROJECT_DIR", "/launch/dir")
	if got := HookAnchor(); got != "/launch/dir" {
		t.Errorf("HookAnchor() = %q, want CLAUDE_PROJECT_DIR", got)
	}
}

// TestResolveHookTarget_PlantedInstanceWithoutEnv covers the unset-variable
// case: a session launched in alpha (the hook runs there) can't point the
// hook at a planted instance through cwd.
func TestResolveHookTarget_PlantedInstanceWithoutEnv(t *testing.T) {
	f := newHookFixture(t)
	planted := filepath.Join(t.TempDir(), "planted")
	mustMkdir(t, filepath.Join(planted, "g", "evil", ".git"))
	if err := SaveState(planted, &InstanceState{SchemaVersion: SchemaVersion}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(f.alpha); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(prev) }()
	if _, err := ResolveHookTarget(HookAnchor(), filepath.Join(planted, "g", "evil"), "evil/x"); err == nil {
		t.Fatal("a cwd outside the launch instance must be refused without CLAUDE_PROJECT_DIR too")
	}
}

func TestResolveHookInstance_FromWorkspaceRootByWorktreePath(t *testing.T) {
	f := newHookFixture(t)
	wt := filepath.Join(f.beta, StateDir, "worktrees", "solo-abc")
	mustMkdir(t, wt)
	got, err := ResolveHookInstance(f.workspaceRoot, wt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want, err := canonicalize(f.beta)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("instance = %q, want %q", got, want)
	}
}

// TestDiscoverHookInstance_SingleInstanceLayout checks a root that is itself
// the instance still resolves as one.
func TestDiscoverHookInstance_SingleInstanceLayout(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, StateDir))
	if err := os.WriteFile(filepath.Join(root, StateDir, WorkspaceConfigFile), []byte("[workspace]\nname = \"ws\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveState(root, &InstanceState{SchemaVersion: SchemaVersion, InstanceName: "ws"}); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "public", "app")
	mustMkdir(t, filepath.Join(repo, ".git"))
	if got, err := DiscoverHookInstance(repo); err != nil || got != root {
		t.Fatalf("DiscoverHookInstance = %q, %v; want %q", got, err, root)
	}
}
