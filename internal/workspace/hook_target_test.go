package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/worktree"
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
	t.Setenv(WorktreeRepoEnv, "")
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
		{"group dir takes the default repo", f.alpha, filepath.Join(f.alpha, "public"), "fix-x", "koto", "fix-x", ""},
		{"multi-repo root takes the default repo", f.alpha, f.alpha, "fix-x", "koto", "fix-x", ""},
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
	t.Setenv(WorktreeRepoEnv, "")
	f := newHookFixture(t)
	_, err := ResolveHookTarget(f.alpha, f.alpha, "nosuchrepo/fix-x")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	for _, want := range []string{"no repo selected", "niwa", "koto", "koto/<slug>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q lacks %q", err, want)
		}
	}
}

// TestResolveHookTarget_BareNameDefault pins the repo a bare name from a
// multi-repo instance root lands in: the first by group, then name, so
// "zeta" in group "lib" beats "alpha" in group "public". Only that path sets
// Defaulted.
func TestResolveHookTarget_BareNameDefault(t *testing.T) {
	t.Setenv(WorktreeRepoEnv, "")
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "public", "alpha", ".git"))
	mustMkdir(t, filepath.Join(root, "public", "beta", ".git"))
	mustMkdir(t, filepath.Join(root, "lib", "zeta", ".git"))
	if err := SaveState(root, &InstanceState{SchemaVersion: SchemaVersion, InstanceName: "inst"}); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveHookTarget(root, root, "agent-a1b2c3")
	if err != nil {
		t.Fatalf("bare name at the root: %v", err)
	}
	if got.Repo != "zeta" || got.Slug != "agent-a1b2c3" || !got.Defaulted {
		t.Errorf("got %+v, want repo zeta, slug agent-a1b2c3, Defaulted", got)
	}

	for _, tc := range []struct{ cwd, name, repo string }{
		{root, "beta/x", "beta"},
		{filepath.Join(root, "public", "beta"), "x", "beta"},
	} {
		got, err := ResolveHookTarget(root, tc.cwd, tc.name)
		if err != nil || got.Repo != tc.repo || got.Defaulted {
			t.Errorf("cwd %s name %q: got %+v (%v), want repo %s not Defaulted", tc.cwd, tc.name, got, err, tc.repo)
		}
	}

	// The refusal's example names the same repo the default picks.
	_, err = ResolveHookTarget(root, root, "nosuch/x")
	if err == nil || !strings.Contains(err.Error(), "for example zeta/<slug>") {
		t.Errorf("refusal %v should give zeta/<slug> as its example", err)
	}
}

// TestResolveHookTarget_WorktreeRepoEnv covers NIWA_WORKTREE_REPO: it picks
// the repo for a bare name from the root, never overrides a prefix or a cwd
// in a clone, and a value naming no cloned repo is refused by name.
func TestResolveHookTarget_WorktreeRepoEnv(t *testing.T) {
	f := newHookFixture(t)
	kotoDir := filepath.Join(f.alpha, "public", "koto")

	t.Setenv(WorktreeRepoEnv, "niwa")
	for _, tc := range []struct{ cwd, name, repo string }{
		{f.alpha, "fix-x", "niwa"},
		{f.alpha, "koto/fix-x", "koto"},
		{kotoDir, "fix-x", "koto"},
	} {
		got, err := ResolveHookTarget(f.alpha, tc.cwd, tc.name)
		if err != nil || got.Repo != tc.repo || got.Defaulted {
			t.Errorf("cwd %s name %q: got %+v (%v), want repo %s not Defaulted", tc.cwd, tc.name, got, err, tc.repo)
		}
	}

	t.Setenv(WorktreeRepoEnv, "nosuch")
	_, err := ResolveHookTarget(f.alpha, f.alpha, "fix-x")
	if err == nil || !strings.Contains(err.Error(), WorktreeRepoEnv) || !strings.Contains(err.Error(), "koto, niwa") {
		t.Fatalf("err = %v, want a refusal naming %s and the repos", err, WorktreeRepoEnv)
	}
}

// setDefaultWorktreeRepo records repo as the instance's configured default,
// as apply does from [workspace].default_worktree_repo.
func setDefaultWorktreeRepo(t *testing.T, instanceRoot, repo string) {
	t.Helper()
	state, err := LoadState(instanceRoot)
	if err != nil {
		t.Fatal(err)
	}
	state.DefaultWorktreeRepo = repo
	if err := SaveState(instanceRoot, state); err != nil {
		t.Fatal(err)
	}
}

// TestResolveHookTarget_ConfiguredDefault covers the workspace's configured
// default repo for a bare name. In the fixture the group/name fallback is
// koto, so the default is set to niwa: a bare name from the root lands there
// without Defaulted, and the refusal's example follows it. NIWA_WORKTREE_REPO
// overrides it; a prefix, a cwd in a clone and a cwd in a live niwa worktree
// all still win over both.
func TestResolveHookTarget_ConfiguredDefault(t *testing.T) {
	t.Setenv(WorktreeRepoEnv, "")
	f := newHookFixture(t)
	setDefaultWorktreeRepo(t, f.alpha, "niwa")

	sessionsDir := filepath.Join(f.alpha, StateDir, "sessions")
	mustMkdir(t, sessionsDir)
	kotoWT := filepath.Join(f.alpha, StateDir, "worktrees", "koto-cccc3333")
	mustMkdir(t, kotoWT)
	if err := worktree.WriteSessionLifecycleState(sessionsDir, worktree.NewSessionLifecycleState("cccc3333", "koto", "p", "", kotoWT, "")); err != nil {
		t.Fatal(err)
	}
	kotoDir := filepath.Join(f.alpha, "public", "koto")

	cases := []struct{ env, cwd, name, repo string }{
		{"", f.alpha, "agent-a1b2c3", "niwa"},
		{"koto", f.alpha, "agent-a1b2c3", "koto"},
		{"", f.alpha, "koto/fix-x", "koto"},
		{"", kotoDir, "fix-x", "koto"},
		{"", kotoWT, "agent-a1b2c3", "koto"},
		{"niwa", kotoWT, "agent-a1b2c3", "koto"},
	}
	for _, tc := range cases {
		t.Setenv(WorktreeRepoEnv, tc.env)
		got, err := ResolveHookTarget(f.alpha, tc.cwd, tc.name)
		if err != nil || got.Repo != tc.repo || got.Defaulted {
			t.Errorf("env %q cwd %s name %q: got %+v (%v), want repo %s not Defaulted", tc.env, tc.cwd, tc.name, got, err, tc.repo)
		}
	}

	t.Setenv(WorktreeRepoEnv, "")
	_, err := ResolveHookTarget(f.alpha, f.alpha, "nosuch/x")
	if err == nil || !strings.Contains(err.Error(), "for example niwa/<slug>") {
		t.Errorf("refusal %v should give the configured niwa/<slug> as its example", err)
	}
}

// TestResolveHookTarget_ConfiguredDefaultWithoutClone covers a configured
// default that names no repo cloned in the instance, whether the instance
// lists it ("ghost") or not: a bare name from the root is refused with the
// key and the repos, rather than landing in the fallback. NIWA_WORKTREE_REPO
// still resolves first, and a prefix is unaffected.
func TestResolveHookTarget_ConfiguredDefaultWithoutClone(t *testing.T) {
	for _, configured := range []string{"ghost", "nosuch"} {
		t.Run(configured, func(t *testing.T) {
			t.Setenv(WorktreeRepoEnv, "")
			f := newHookFixture(t)
			setDefaultWorktreeRepo(t, f.alpha, configured)

			_, err := ResolveHookTarget(f.alpha, f.alpha, "agent-a1b2c3")
			if err == nil {
				t.Fatal("expected a refusal")
			}
			for _, want := range []string{DefaultWorktreeRepoKey, `"` + configured + `"`, "koto, niwa", "niwa apply"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q lacks %q", err, want)
				}
			}
			// The fallback example doesn't name a repo with no clone.
			_, err = ResolveHookTarget(f.alpha, f.alpha, "nosuchrepo/x")
			if err == nil || !strings.Contains(err.Error(), "for example koto/<slug>") {
				t.Errorf("refusal %v should fall back to koto/<slug> as its example", err)
			}

			if got, err := ResolveHookTarget(f.alpha, f.alpha, "niwa/fix-x"); err != nil || got.Repo != "niwa" {
				t.Errorf("prefix: got %+v (%v), want niwa", got, err)
			}
			t.Setenv(WorktreeRepoEnv, "niwa")
			if got, err := ResolveHookTarget(f.alpha, f.alpha, "agent-a1b2c3"); err != nil || got.Repo != "niwa" {
				t.Errorf("%s=niwa: got %+v (%v), want niwa", WorktreeRepoEnv, got, err)
			}
		})
	}
}

// TestWarnUnknownWorktreeDefault covers apply's warning for a configured
// default that names no repo the apply classified.
func TestWarnUnknownWorktreeDefault(t *testing.T) {
	classified := []ClassifiedRepo{{Repo: github.Repo{Name: "niwa"}, Group: "public"}}
	if w := warnUnknownWorktreeDefault("", classified); w != "" {
		t.Errorf("unset: got %q, want no warning", w)
	}
	if w := warnUnknownWorktreeDefault("niwa", classified); w != "" {
		t.Errorf("known repo: got %q, want no warning", w)
	}
	w := warnUnknownWorktreeDefault("nosuch", classified)
	if !strings.Contains(w, DefaultWorktreeRepoKey) || !strings.Contains(w, `"nosuch"`) {
		t.Errorf("unknown repo: got %q, want a warning naming the key and value", w)
	}
}

// TestResolveHookTarget_CwdInNiwaWorktree covers a session that moved into
// one of the instance's niwa worktrees: a bare name resolves to that
// worktree's repo, not to the default. An ended session's worktree doesn't
// count.
func TestResolveHookTarget_CwdInNiwaWorktree(t *testing.T) {
	t.Setenv(WorktreeRepoEnv, "")
	f := newHookFixture(t)
	sessionsDir := filepath.Join(f.alpha, StateDir, "sessions")
	mustMkdir(t, sessionsDir)

	live := filepath.Join(f.alpha, StateDir, "worktrees", "niwa-aaaa1111")
	mustMkdir(t, filepath.Join(live, "sub"))
	if err := worktree.WriteSessionLifecycleState(sessionsDir, worktree.NewSessionLifecycleState("aaaa1111", "niwa", "p", "", live, "")); err != nil {
		t.Fatal(err)
	}
	ended := filepath.Join(f.alpha, StateDir, "worktrees", "niwa-bbbb2222")
	mustMkdir(t, ended)
	endedState := worktree.NewSessionLifecycleState("bbbb2222", "niwa", "p", "", ended, "")
	endedState.Status = worktree.SessionStatusEnded
	if err := worktree.WriteSessionLifecycleState(sessionsDir, endedState); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveHookTarget(f.alpha, filepath.Join(live, "sub"), "agent-x")
	if err != nil || got.Repo != "niwa" || got.Defaulted {
		t.Fatalf("cwd in a live niwa worktree: got %+v (%v), want repo niwa not Defaulted", got, err)
	}
	got, err = ResolveHookTarget(f.alpha, ended, "agent-x")
	if err != nil || got.Repo != "koto" || !got.Defaulted {
		t.Fatalf("cwd in an ended worktree: got %+v (%v), want the default koto", got, err)
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
