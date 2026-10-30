package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tsukumogami/niwa/internal/worktree"
)

// newDotNamedRepoFixture builds an instance whose workspace config declares
// a repo whose directory name starts with a dot, the way an org's .github
// repo is cloned at <root>/public/.github, next to an ordinary repo. Both are
// listed in the instance state, as an apply records them. Only .github is a
// real git repo; the other needs just its .git entry to count as a clone.
func newDotNamedRepoFixture(t *testing.T) *createFlowFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	group := "public"
	repo := ".github"
	repoPath := filepath.Join(root, group, repo)
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	for _, args := range [][]string{
		{"-C", repoPath, "init", "-b", "main"},
		{"-C", repoPath, "-c", "user.email=test@test.com", "-c", "user.name=Test", "commit", "--allow-empty", "-m", "init"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, group, "myrepo", ".git"), 0o755); err != nil {
		t.Fatalf("mkdir myrepo: %v", err)
	}

	niwaDir := filepath.Join(root, ".niwa")
	if err := os.MkdirAll(filepath.Join(niwaDir, "sessions"), 0o700); err != nil {
		t.Fatalf("mkdir .niwa/sessions: %v", err)
	}
	state := `{"instance_name":"testws","repos":{".github":{"url":"","cloned":true},"myrepo":{"url":"","cloned":true}}}`
	if err := os.WriteFile(filepath.Join(niwaDir, "instance.json"), []byte(state), 0o600); err != nil {
		t.Fatalf("write instance.json: %v", err)
	}
	wsTOML := "[workspace]\nname = \"testws\"\n\n[repos.\".github\".claude]\nenabled = false\n\n[repos.myrepo.claude]\nenabled = false\n"
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(wsTOML), 0o644); err != nil {
		t.Fatalf("write workspace.toml: %v", err)
	}

	t.Setenv("NIWA_INSTANCE_ROOT", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))

	return &createFlowFixture{
		root:        root,
		group:       group,
		repo:        repo,
		repoPath:    repoPath,
		sessionsDir: filepath.Join(niwaDir, "sessions"),
	}
}

// TestFromHookCreate_DeclaredDotNamedRepo covers a WorktreeCreate hook asking
// for a worktree of a declared dot-named repo from the instance root, by
// <repo>/<slug> and by <group>/<repo>/<slug>. The repo walk used to skip every
// dot-named repo directory, so the hook refused the repo as configured but
// not cloned and pointed at niwa apply.
func TestFromHookCreate_DeclaredDotNamedRepo(t *testing.T) {
	for _, tc := range []struct {
		name     string
		hookName string
		purpose  string
	}{
		{"repo prefix", ".github/fix-x", "fix-x"},
		{"group and repo prefix", "public/.github/fix-y", "fix-y"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDotNamedRepoFixture(t)
			resetSessionCreateFlags(t)
			defer resetSessionCreateFlags(t)
			t.Setenv("CLAUDE_PROJECT_DIR", f.root)

			hookJSON := mustHookJSON(t, map[string]any{
				"hook_event_name": "WorktreeCreate",
				"session_id":      "claude-sid",
				"cwd":             f.root,
				"name":            tc.hookName,
			})
			if _, errOut, err := runFromHook(t, f.root, hookJSON); err != nil {
				t.Fatalf("from-hook create of %q: %v\nstderr:\n%s", tc.hookName, err, errOut)
			}
			states, err := worktree.ListSessionLifecycleStates(f.sessionsDir)
			if err != nil {
				t.Fatalf("list sessions: %v", err)
			}
			if len(states) != 1 || states[0].Repo != ".github" || states[0].Purpose != tc.purpose {
				t.Fatalf("want one session for .github with purpose %q, got %+v", tc.purpose, states)
			}
			if _, err := os.Stat(states[0].WorktreePath); err != nil {
				t.Errorf("worktree not on disk: %v", err)
			}
		})
	}
}

// TestRunSessionCreate_DeclaredDotNamedRepo covers `niwa worktree create` for
// a declared dot-named repo, named explicitly and inferred from a cwd inside
// its clone.
func TestRunSessionCreate_DeclaredDotNamedRepo(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		from func(f *createFlowFixture) string
	}{
		{"explicit repo", []string{".github", "explicit"}, func(f *createFlowFixture) string { return f.root }},
		{"repo from cwd", nil, func(f *createFlowFixture) string { return f.repoPath }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newDotNamedRepoFixture(t)
			resetSessionCreateFlags(t)
			defer resetSessionCreateFlags(t)

			if out, err := f.runCreate(t, tc.from(f), tc.args); err != nil {
				t.Fatalf("niwa worktree create %v: %v\n%s", tc.args, err, out)
			}
			states, err := worktree.ListSessionLifecycleStates(f.sessionsDir)
			if err != nil {
				t.Fatalf("list sessions: %v", err)
			}
			if len(states) != 1 || states[0].Repo != ".github" {
				t.Fatalf("want one session for .github, got %+v", states)
			}
		})
	}
}
