package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/workspace"
	"github.com/tsukumogami/niwa/internal/worktree"
)

// TestFromHookCreate_FromInstanceRootWithRepoPrefix covers a session launched
// at the instance root: the name's repo prefix picks the repo, the prefix is
// taken off the recorded purpose, and stdout is exactly one absolute path.
func TestFromHookCreate_FromInstanceRootWithRepoPrefix(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)
	t.Setenv("CLAUDE_PROJECT_DIR", f.root)

	hookJSON := mustHookJSON(t, map[string]any{
		"hook_event_name": "WorktreeCreate",
		"session_id":      "claude-sid",
		"cwd":             f.root,
		"name":            f.repo + "/fix-x",
	})
	out, errOut, err := runFromHook(t, f.root, hookJSON)
	if err != nil {
		t.Fatalf("from-hook create at the instance root: %v\nstderr:\n%s", err, errOut)
	}
	printed := strings.TrimSuffix(out, "\n")
	if strings.Contains(printed, "\n") || !filepath.IsAbs(printed) {
		t.Fatalf("stdout must be exactly one absolute path, got %q", out)
	}
	if resolved, err := filepath.EvalSymlinks(printed); err != nil || resolved != printed {
		t.Errorf("printed path %q is not symlink-resolved (%q, %v)", printed, resolved, err)
	}
	if strings.Contains(printed, "/./") || strings.Contains(printed, "/../") {
		t.Errorf("printed path %q has dot segments", printed)
	}

	states, err := worktree.ListSessionLifecycleStates(f.sessionsDir)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("want 1 session, got %d", len(states))
	}
	if states[0].Repo != f.repo || states[0].Purpose != "fix-x" {
		t.Errorf("session repo %q purpose %q, want %q and %q", states[0].Repo, states[0].Purpose, f.repo, "fix-x")
	}
}

// TestFromHookCreate_SingleRepoInstanceRoot covers the one-repo default: a
// name with no prefix, sent from the instance root, picks the only repo.
func TestFromHookCreate_SingleRepoInstanceRoot(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)
	t.Setenv("CLAUDE_PROJECT_DIR", f.root)

	hookJSON := mustHookJSON(t, map[string]any{
		"hook_event_name": "WorktreeCreate",
		"session_id":      "claude-sid",
		"cwd":             f.root,
		"name":            "plain",
	})
	if _, errOut, err := runFromHook(t, f.root, hookJSON); err != nil {
		t.Fatalf("from-hook create: %v\nstderr:\n%s", err, errOut)
	}
	states, err := worktree.ListSessionLifecycleStates(f.sessionsDir)
	if err != nil || len(states) != 1 || states[0].Repo != f.repo {
		t.Fatalf("want one session for %q, got %+v (%v)", f.repo, states, err)
	}
}

// TestFromHookRemove_ResolvesInstanceFromAnchor covers a remove whose process
// runs outside the instance (as from a workspace-root launch): the instance
// is found from the launch directory and the worktree path.
func TestFromHookRemove_ResolvesInstanceFromAnchor(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)

	created := createViaHook(t, f, "to-remove")

	t.Setenv("NIWA_INSTANCE_ROOT", "")
	t.Setenv("CLAUDE_PROJECT_DIR", f.root)
	elsewhere := t.TempDir()
	hookJSON := mustHookJSON(t, map[string]any{
		"hook_event_name": "WorktreeRemove",
		"session_id":      "claude-sid",
		"worktree_path":   created.WorktreePath,
	})
	if _, errOut, err := runFromHook(t, elsewhere, hookJSON); err != nil {
		t.Fatalf("remove must exit 0: %v\nstderr:\n%s", err, errOut)
	}
	state, err := worktree.ReadSessionLifecycleState(f.sessionsDir, created.SessionID)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if state.Status != worktree.SessionStatusEnded {
		t.Errorf("status = %q, want ended", state.Status)
	}
	if _, statErr := os.Stat(created.WorktreePath); !os.IsNotExist(statErr) {
		t.Errorf("worktree still present: %v", statErr)
	}
}

// TestFromHookCreate_BareNameAtMultiRepoRoot covers Claude Code's Agent tool
// asking for a worktree from the root of a multi-repo instance, with a bare
// name it picked itself: the hook creates it in the instance's first repo,
// says so on stderr with the three ways to choose another, and keeps stdout to the
// path. A WorktreeRemove sent from the same root then tears it down.
func TestFromHookCreate_BareNameAtMultiRepoRoot(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)
	t.Setenv("CLAUDE_PROJECT_DIR", f.root)
	t.Setenv(workspace.WorktreeRepoEnv, "")
	// "zz-other" sorts after f.repo in the same group, so f.repo is the default.
	if err := os.MkdirAll(filepath.Join(f.root, f.group, "zz-other", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	hookJSON := mustHookJSON(t, map[string]any{
		"hook_event_name": "WorktreeCreate",
		"session_id":      "claude-sid",
		"cwd":             f.root,
		"name":            "agent-a1b2c3",
	})
	out, errOut, err := runFromHook(t, f.root, hookJSON)
	if err != nil {
		t.Fatalf("bare name at a multi-repo root: %v\nstderr:\n%s", err, errOut)
	}
	printed := strings.TrimSuffix(out, "\n")
	if strings.Contains(printed, "\n") || !filepath.IsAbs(printed) {
		t.Fatalf("stdout must be exactly one absolute path, got %q", out)
	}
	for _, want := range []string{f.repo, workspace.WorktreeRepoEnv, "<repo>/<slug>", workspace.DefaultWorktreeRepoKey} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr notice lacks %q:\n%s", want, errOut)
		}
	}
	states, err := worktree.ListSessionLifecycleStates(f.sessionsDir)
	if err != nil || len(states) != 1 || states[0].Repo != f.repo {
		t.Fatalf("want one session for %q, got %+v (%v)", f.repo, states, err)
	}
	created := states[0]

	removeJSON := mustHookJSON(t, map[string]any{
		"hook_event_name": "WorktreeRemove",
		"session_id":      "claude-sid",
		"cwd":             f.root,
		"worktree_path":   printed,
	})
	// Clear the fixture's NIWA_INSTANCE_ROOT so remove has to find the
	// instance from the launch directory, as a root session's hook does.
	t.Setenv("NIWA_INSTANCE_ROOT", "")
	if _, errOut, err := runFromHook(t, f.root, removeJSON); err != nil {
		t.Fatalf("remove must exit 0: %v\nstderr:\n%s", err, errOut)
	}
	state, err := worktree.ReadSessionLifecycleState(f.sessionsDir, created.SessionID)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if state.Status != worktree.SessionStatusEnded {
		t.Errorf("status = %q, want ended", state.Status)
	}
	if _, statErr := os.Stat(created.WorktreePath); !os.IsNotExist(statErr) {
		t.Errorf("worktree still present: %v", statErr)
	}
}

// TestFromHookCreate_BareNameTakesConfiguredDefault covers a workspace that
// configured [workspace].default_worktree_repo, as apply records it in the
// instance state: a bare name from a multi-repo root lands in that repo, not
// in "aa-other", the first by group and name, and stderr carries no notice.
func TestFromHookCreate_BareNameTakesConfiguredDefault(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)
	t.Setenv("CLAUDE_PROJECT_DIR", f.root)
	t.Setenv(workspace.WorktreeRepoEnv, "")
	if err := os.MkdirAll(filepath.Join(f.root, f.group, "aa-other", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	state := `{"instance_name":"testws","default_worktree_repo":"` + f.repo + `"}`
	if err := os.WriteFile(filepath.Join(f.root, ".niwa", "instance.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}

	hookJSON := mustHookJSON(t, map[string]any{
		"hook_event_name": "WorktreeCreate",
		"session_id":      "claude-sid",
		"cwd":             f.root,
		"name":            "agent-a1b2c3",
	})
	out, errOut, err := runFromHook(t, f.root, hookJSON)
	if err != nil {
		t.Fatalf("bare name with a configured default: %v\nstderr:\n%s", err, errOut)
	}
	if printed := strings.TrimSuffix(out, "\n"); strings.Contains(printed, "\n") || !filepath.IsAbs(printed) {
		t.Fatalf("stdout must be exactly one absolute path, got %q", out)
	}
	if strings.Contains(errOut, "notice") {
		t.Errorf("a configured default must not print the fallback notice:\n%s", errOut)
	}
	states, err := worktree.ListSessionLifecycleStates(f.sessionsDir)
	if err != nil || len(states) != 1 || states[0].Repo != f.repo {
		t.Fatalf("want one session for %q, got %+v (%v)", f.repo, states, err)
	}
}

// TestFromHookCreate_RefusalsThroughTheCommand covers the refusals end to end:
// in a multi-repo instance an unknown prefix is refused without creating
// anything named after it, and the stderr names the repos and an example.
func TestFromHookCreate_RefusalsThroughTheCommand(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)
	t.Setenv("CLAUDE_PROJECT_DIR", f.root)
	if err := os.MkdirAll(filepath.Join(f.root, f.group, "other", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"nosuchrepo/fix-x", "public/nosuch/fix-x"} {
		hookJSON := mustHookJSON(t, map[string]any{
			"hook_event_name": "WorktreeCreate",
			"session_id":      "claude-sid",
			"cwd":             f.root,
			"name":            name,
		})
		out, _, err := runFromHook(t, f.root, hookJSON)
		if err == nil || out != "" {
			t.Fatalf("name %q: want a refusal and empty stdout, got err=%v out=%q", name, err, out)
		}
		for _, want := range []string{"no repo selected", f.repo, "other", "/<slug>"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("name %q: refusal %q lacks %q", name, err, want)
			}
		}
	}
	if matches, _ := filepath.Glob(filepath.Join(f.root, "*", "nosuchrepo*")); len(matches) != 0 {
		t.Errorf("an unknown prefix must create nothing, found %v", matches)
	}
	states, _ := worktree.ListSessionLifecycleStates(f.sessionsDir)
	if len(states) != 0 {
		t.Errorf("want no sessions, got %d", len(states))
	}
}

// TestFromHookRemove_OptedOutInstanceIsLeftAlone covers the shared
// workspace-root hook reaching an opted-out instance: remove exits 0 and
// changes nothing.
func TestFromHookRemove_OptedOutInstanceIsLeftAlone(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)

	created := createViaHook(t, f, "keep-me")

	state, err := workspace.LoadState(f.root)
	if err != nil {
		t.Fatal(err)
	}
	state.NoWorktreeDelegation = true
	if err := workspace.SaveState(f.root, state); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CLAUDE_PROJECT_DIR", f.root)
	hookJSON := mustHookJSON(t, map[string]any{
		"hook_event_name": "WorktreeRemove",
		"session_id":      "claude-sid",
		"worktree_path":   created.WorktreePath,
	})
	if _, errOut, err := runFromHook(t, f.root, hookJSON); err != nil {
		t.Fatalf("remove must exit 0: %v\nstderr:\n%s", err, errOut)
	}
	if _, statErr := os.Stat(created.WorktreePath); statErr != nil {
		t.Errorf("an opted-out instance's worktree must be left alone: %v", statErr)
	}
	after, err := worktree.ReadSessionLifecycleState(f.sessionsDir, created.SessionID)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if after.Status == worktree.SessionStatusEnded {
		t.Errorf("session should not have been ended")
	}
}

// TestFromHookCreate_MalformedPayloadCreatesNothing covers malformed input:
// a non-zero exit, nothing on stdout, no session.
func TestFromHookCreate_MalformedPayloadCreatesNothing(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)

	for _, payload := range []string{"{not json", `{"cwd": "x", "name": "y"}`} {
		out, _, err := runFromHook(t, f.root, payload)
		if err == nil || out != "" {
			t.Errorf("payload %q: want an error and empty stdout, got err=%v out=%q", payload, err, out)
		}
	}
	states, _ := worktree.ListSessionLifecycleStates(f.sessionsDir)
	if len(states) != 0 {
		t.Errorf("want no sessions, got %d", len(states))
	}
}
