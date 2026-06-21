package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/cli/sessionattach"
	"github.com/tsukumogami/niwa/internal/worktree"
)

// resetSessionCreateFlags resets the create/destroy command flags between
// tests so flag state from one test cannot leak into the next (the flag vars
// are package-level, shared across the cobra command instances).
func resetSessionCreateFlags(t *testing.T) {
	t.Helper()
	sessionCreateJSON = false
	sessionDestroyForce = false
	sessionDestroyByPath = ""
}

// createFlowFixture is a workspace instance prepared for an end-to-end
// runSessionCreate: a real git repo at <root>/<group>/<repo>, a minimal
// workspace.toml that disables Claude content for the repo (so create takes
// the lightweight worktree-context layer path, no vault/materializer
// machinery), and the sessions dir.
type createFlowFixture struct {
	root        string // instance root
	group       string
	repo        string
	repoPath    string // <root>/<group>/<repo>
	sessionsDir string
}

// newCreateFlowFixture builds the fixture and wires NIWA_INSTANCE_ROOT plus an
// isolated XDG_CONFIG_HOME (so global-config discovery stays offline). It skips
// the test if git is unavailable.
func newCreateFlowFixture(t *testing.T) *createFlowFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	group := "public"
	repo := "myrepo"
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

	niwaDir := filepath.Join(root, ".niwa")
	if err := os.MkdirAll(filepath.Join(niwaDir, "sessions"), 0o700); err != nil {
		t.Fatalf("mkdir .niwa/sessions: %v", err)
	}
	if err := os.WriteFile(filepath.Join(niwaDir, "instance.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write instance.json: %v", err)
	}
	// Minimal workspace config. Disabling Claude content for the repo keeps the
	// content install on the lightweight worktree-context layer path, which is
	// all this issue's create-flow assertions (path/session id/purpose) need.
	wsTOML := "[workspace]\nname = \"testws\"\n\n[repos." + repo + ".claude]\nenabled = false\n"
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

// runCreate invokes runSessionCreate with the given positional args from inside
// fromDir (so cwd inference can resolve the repo). It returns captured stdout.
func (f *createFlowFixture) runCreate(t *testing.T, fromDir string, args []string) (string, error) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(fromDir); err != nil {
		t.Fatalf("chdir %s: %v", fromDir, err)
	}
	defer func() { _ = os.Chdir(prev) }()

	var buf bytes.Buffer
	sessionCreateCmd.SetOut(&buf)
	sessionCreateCmd.SetErr(&buf)
	defer func() {
		sessionCreateCmd.SetOut(os.Stdout)
		sessionCreateCmd.SetErr(os.Stderr)
	}()
	runErr := runSessionCreate(sessionCreateCmd, args)
	return buf.String(), runErr
}

// TestRunSessionCreate_JSONOutputShape verifies `--json` emits a single valid
// JSON object carrying at least the absolute worktree path and the session id,
// and that the recorded session state matches.
func TestRunSessionCreate_JSONOutputShape(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)
	sessionCreateJSON = true

	out, err := f.runCreate(t, f.repoPath, []string{f.repo, "json-shape"})
	if err != nil {
		t.Fatalf("runSessionCreate: %v\noutput:\n%s", err, out)
	}

	var got sessionCreateJSONOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput:\n%s", err, out)
	}
	if got.SessionID == "" {
		t.Errorf("session_id is empty in JSON: %s", out)
	}
	if got.WorktreePath == "" {
		t.Errorf("worktree_path is empty in JSON: %s", out)
	}
	if !filepath.IsAbs(got.WorktreePath) {
		t.Errorf("worktree_path %q is not absolute", got.WorktreePath)
	}
	if _, statErr := os.Stat(got.WorktreePath); statErr != nil {
		t.Errorf("worktree_path %q does not exist on disk: %v", got.WorktreePath, statErr)
	}
	if got.Repo != f.repo {
		t.Errorf("repo = %q, want %q", got.Repo, f.repo)
	}
	if got.Purpose != "json-shape" {
		t.Errorf("purpose = %q, want %q", got.Purpose, "json-shape")
	}

	// The JSON object must be the only thing on stdout: no human "session:
	// created ..." line leaking through.
	if strings.Contains(out, "session: created") {
		t.Errorf("--json output leaked the human summary line:\n%s", out)
	}

	// The recorded lifecycle state must agree with the JSON.
	state, err := worktree.ReadSessionLifecycleState(f.sessionsDir, got.SessionID)
	if err != nil {
		t.Fatalf("ReadSessionLifecycleState: %v", err)
	}
	if state.WorktreePath != got.WorktreePath {
		t.Errorf("state WorktreePath = %q, JSON = %q", state.WorktreePath, got.WorktreePath)
	}
}

// TestRunSessionCreate_DefaultHumanOutputUnchanged verifies that WITHOUT
// --json the existing human summary line is still printed (regression guard
// for Decision 2's "default output unchanged").
func TestRunSessionCreate_DefaultHumanOutputUnchanged(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)

	out, err := f.runCreate(t, f.repoPath, []string{f.repo, "human"})
	if err != nil {
		t.Fatalf("runSessionCreate: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "session: created ") {
		t.Errorf("default output missing human summary line:\n%s", out)
	}
	if strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Errorf("default output unexpectedly looks like JSON:\n%s", out)
	}
}

// TestRunSessionCreate_RepoInferredFromCwd verifies a bare create (no repo
// arg), run from inside the repo checkout, infers the repo from cwd.
func TestRunSessionCreate_RepoInferredFromCwd(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)
	sessionCreateJSON = true

	// No positional args at all: repo from cwd, purpose defaulted.
	out, err := f.runCreate(t, f.repoPath, nil)
	if err != nil {
		t.Fatalf("runSessionCreate: %v\noutput:\n%s", err, out)
	}
	var got sessionCreateJSONOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if got.Repo != f.repo {
		t.Errorf("inferred repo = %q, want %q", got.Repo, f.repo)
	}
}

// TestRunSessionCreate_PurposeOmittedUsesDefault verifies that omitting the
// purpose arg (passing only the repo) records the generic default purpose.
func TestRunSessionCreate_PurposeOmittedUsesDefault(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)
	sessionCreateJSON = true

	out, err := f.runCreate(t, f.repoPath, []string{f.repo})
	if err != nil {
		t.Fatalf("runSessionCreate: %v\noutput:\n%s", err, out)
	}
	var got sessionCreateJSONOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if got.Purpose != defaultSessionPurpose {
		t.Errorf("purpose = %q, want default %q", got.Purpose, defaultSessionPurpose)
	}
	state, err := worktree.ReadSessionLifecycleState(f.sessionsDir, got.SessionID)
	if err != nil {
		t.Fatalf("ReadSessionLifecycleState: %v", err)
	}
	if state.Purpose != defaultSessionPurpose {
		t.Errorf("recorded purpose = %q, want %q", state.Purpose, defaultSessionPurpose)
	}
}

// TestResolveSessionIDByPath_Resolves verifies --by-path maps a worktree
// directory to the owning active session, tolerating a non-canonical (here:
// trailing-slash and ..-bearing) input that points at the same directory.
func TestResolveSessionIDByPath_Resolves(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	wtA := filepath.Join(t.TempDir(), "wt-a")
	wtB := filepath.Join(t.TempDir(), "wt-b")
	for _, p := range []string{wtA, wtB} {
		if err := os.MkdirAll(filepath.Join(p, ".niwa"), 0o700); err != nil {
			t.Fatalf("mkdir worktree %s: %v", p, err)
		}
	}
	root := seedLifecycleSessions(t, []worktree.SessionLifecycleState{
		{V: 1, SessionID: "aaaaaaaa", Repo: "niwa", Status: worktree.SessionStatusActive, WorktreePath: wtA, CreationTime: now},
		{V: 1, SessionID: "bbbbbbbb", Repo: "niwa", Status: worktree.SessionStatusActive, WorktreePath: wtB, CreationTime: now},
	})

	// Non-canonical spelling of wtB: append a "." segment and a trailing sep.
	noncanonical := filepath.Join(wtB, ".") + string(filepath.Separator)
	got, err := resolveSessionIDByPath(root, noncanonical)
	if err != nil {
		t.Fatalf("resolveSessionIDByPath: %v", err)
	}
	if got != "bbbbbbbb" {
		t.Errorf("resolved session = %q, want bbbbbbbb", got)
	}
}

// TestResolveSessionIDByPath_UnknownPathErrors verifies an unmatched path is a
// clear, code-1 error (not a panic or a silent empty result).
func TestResolveSessionIDByPath_UnknownPathErrors(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	wt := filepath.Join(t.TempDir(), "wt-a")
	if err := os.MkdirAll(filepath.Join(wt, ".niwa"), 0o700); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	root := seedLifecycleSessions(t, []worktree.SessionLifecycleState{
		{V: 1, SessionID: "aaaaaaaa", Repo: "niwa", Status: worktree.SessionStatusActive, WorktreePath: wt, CreationTime: now},
	})

	unknown := filepath.Join(t.TempDir(), "nowhere")
	_, err := resolveSessionIDByPath(root, unknown)
	if err == nil {
		t.Fatalf("want error for unknown path, got nil")
	}
	var ece *sessionattach.ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("err is not *ExitCodeError: %T (%v)", err, err)
	}
	if ece.Code != 1 {
		t.Errorf("Code = %d, want 1", ece.Code)
	}
	if !strings.Contains(ece.Msg, "no active worktree found at path") {
		t.Errorf("message = %q, want substring 'no active worktree found at path'", ece.Msg)
	}
}

// TestResolveSessionIDByPath_SkipsTerminalSessions verifies that an ended
// session whose WorktreePath matches is NOT resolved (its directory is gone;
// resolving it would try to destroy an already-destroyed session).
func TestResolveSessionIDByPath_SkipsTerminalSessions(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	wt := filepath.Join(t.TempDir(), "wt-ended")
	if err := os.MkdirAll(filepath.Join(wt, ".niwa"), 0o700); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	root := seedLifecycleSessions(t, []worktree.SessionLifecycleState{
		{V: 1, SessionID: "aaaaaaaa", Repo: "niwa", Status: worktree.SessionStatusEnded, WorktreePath: wt, CreationTime: now},
	})

	_, err := resolveSessionIDByPath(root, wt)
	if err == nil {
		t.Fatalf("want error (terminal session must be skipped), got nil")
	}
}

// TestRunSessionDestroy_ByPathEndToEnd creates a real worktree and then
// destroys it via --by-path, exercising the full create -> resolve-by-path ->
// DestroySession path against real git. After destroy the session must be
// terminal and the worktree directory removed.
func TestRunSessionDestroy_ByPathEndToEnd(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)
	sessionCreateJSON = true

	out, err := f.runCreate(t, f.repoPath, []string{f.repo, "destroy-by-path"})
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	var created sessionCreateJSONOutput
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("invalid create JSON: %v\n%s", err, out)
	}

	resetSessionCreateFlags(t)
	sessionDestroyByPath = created.WorktreePath

	var dbuf bytes.Buffer
	sessionDestroyCmd.SetOut(&dbuf)
	sessionDestroyCmd.SetErr(&dbuf)
	defer func() {
		sessionDestroyCmd.SetOut(os.Stdout)
		sessionDestroyCmd.SetErr(os.Stderr)
	}()
	if err := runSessionDestroy(sessionDestroyCmd, nil); err != nil {
		t.Fatalf("destroy --by-path: %v\n%s", err, dbuf.String())
	}
	if !strings.Contains(dbuf.String(), "session: destroyed "+created.SessionID) {
		t.Errorf("destroy output missing summary for %s:\n%s", created.SessionID, dbuf.String())
	}

	state, err := worktree.ReadSessionLifecycleState(f.sessionsDir, created.SessionID)
	if err != nil {
		t.Fatalf("ReadSessionLifecycleState: %v", err)
	}
	if state.Status != worktree.SessionStatusEnded {
		t.Errorf("status = %q, want ended after destroy", state.Status)
	}
	if _, statErr := os.Stat(created.WorktreePath); !os.IsNotExist(statErr) {
		t.Errorf("worktree dir still present after destroy: %v", statErr)
	}
}

// TestRunSessionDestroy_ByPathAndArgConflict verifies passing both a
// positional id and --by-path is a usage error (code 2).
func TestRunSessionDestroy_ByPathAndArgConflict(t *testing.T) {
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)
	sessionDestroyByPath = "/some/path"

	err := runSessionDestroy(sessionDestroyCmd, []string{"aaaaaaaa"})
	if err == nil {
		t.Fatalf("want usage error, got nil")
	}
	var ece *sessionattach.ExitCodeError
	if !errors.As(err, &ece) {
		t.Fatalf("err is not *ExitCodeError: %T", err)
	}
	if ece.Code != 2 {
		t.Errorf("Code = %d, want 2", ece.Code)
	}
}
