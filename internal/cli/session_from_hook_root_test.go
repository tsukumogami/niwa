package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
