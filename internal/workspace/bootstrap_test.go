package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"testing"

	"github.com/tsukumogami/niwa/internal/source"
)

// recordedInvocation captures one GitInvoker.CommandContext call's argv
// + env so tests can assert against them after RunBootstrap returns.
type recordedInvocation struct {
	Args []string
	Env  []string
}

// recordingGitInvoker is the test seam for the bootstrap pipeline's
// git subprocess calls. Each CommandContext call is appended to
// invocations under mu; faultFn (if non-nil) decides per-invocation
// whether the produced *exec.Cmd should fail when Run/CombinedOutput
// is called. Faults are simulated by pointing the cmd at /bin/false
// (or a script that mimics git's exit-code behavior).
type recordingGitInvoker struct {
	mu          sync.Mutex
	invocations []recordedInvocation
	cmds        []*exec.Cmd
	// faultFn returns a non-nil shell command to substitute when the
	// arg list matches a fault pattern. Nil means "no fault".
	faultFn func(args []string) []string
}

func (r *recordingGitInvoker) CommandContext(ctx context.Context, args ...string) *exec.Cmd {
	r.mu.Lock()
	// Capture argv (excluding cmd.Env which is populated by the caller
	// AFTER CommandContext returns; the env recorder runs in Run() via
	// a wrapper). We snapshot args here and a fresh os.Environ() so
	// tests querying cmd.Env post-construction see what the caller
	// wired.
	r.invocations = append(r.invocations, recordedInvocation{Args: append([]string(nil), args...)})
	idx := len(r.invocations) - 1
	r.mu.Unlock()

	// Build a cmd that succeeds by default (`true`); replace it with a
	// failing one when the fault function asks for it.
	cmdline := []string{"true"}
	if r.faultFn != nil {
		if alt := r.faultFn(args); alt != nil {
			cmdline = alt
		}
	}
	cmd := exec.CommandContext(ctx, cmdline[0], cmdline[1:]...)
	// Defer-record the env at Run-time so the test sees whatever the
	// caller assigned to cmd.Env. We can't intercept Run() on *exec.Cmd
	// directly, so the env is read at the end of the test from the
	// per-invocation Cmd reference via a custom hook. Simpler approach:
	// the test inspects cmd.Env immediately after RunBootstrap returns
	// using a sidecar []*exec.Cmd slice. To keep the harness simple,
	// stash the cmd pointer alongside the recorded args.
	r.mu.Lock()
	r.cmds = append(r.cmds, cmd)
	// Backfill the recorded env lazily after Run; tests that care
	// inspect via cmd.Env at the time their fault was scheduled.
	_ = idx
	r.mu.Unlock()
	return cmd
}

func (r *recordingGitInvoker) snapshot() ([]recordedInvocation, []*exec.Cmd) {
	r.mu.Lock()
	defer r.mu.Unlock()
	invs := append([]recordedInvocation(nil), r.invocations...)
	cs := append([]*exec.Cmd(nil), r.cmds...)
	return invs, cs
}

// stubCreateSession is a deterministic CreateSession seam for tests.
// scenarios:
//
//   - failOnEntry: returns an error before doing anything
//   - happyPath: creates a worktree dir + writes a session state JSON
//     mimicking the real CreateSession, then returns success
type stubCreateSession struct {
	mu             sync.Mutex
	failOnEntry    error
	createdSession string
	createdBranch  string
	createdWtPath  string
	called         bool
}

func (s *stubCreateSession) Fn(ctx context.Context, instanceRoot, repo, purpose, branchPrefix string, gi GitInvoker) (string, string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.called = true
	if s.failOnEntry != nil {
		return "", "", "", s.failOnEntry
	}
	sid := "abcd1234"
	branch := branchPrefix + sid
	worktreesDir := filepath.Join(instanceRoot, ".niwa", "worktrees")
	if err := os.MkdirAll(worktreesDir, 0o700); err != nil {
		return "", "", "", err
	}
	wt := filepath.Join(worktreesDir, repo+"-"+sid)
	if err := os.MkdirAll(wt, 0o700); err != nil {
		return "", "", "", err
	}
	// Initialize the worktree as a git repo so `git commit` can be
	// dry-run via the recording invoker (the invoker stubs all git
	// calls anyway — this dir just needs to exist for ScaffoldFromSource
	// to write under it).
	sessionsDir := filepath.Join(instanceRoot, ".niwa", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		return "", "", "", err
	}
	// Write a session state JSON so DefaultDestroySession can read it
	// if invoked.
	state := map[string]any{
		"v":             1,
		"session_id":    sid,
		"repo":          repo,
		"purpose":       purpose,
		"status":        "active",
		"creation_time": "2026-01-01T00:00:00Z",
		"worktree_path": wt,
		"branch_name":   branch,
		"creator_pid":   os.Getpid(),
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	if err := os.WriteFile(filepath.Join(sessionsDir, sid+".json"), data, 0o600); err != nil {
		return "", "", "", err
	}
	s.createdSession = sid
	s.createdBranch = branch
	s.createdWtPath = wt
	return sid, wt, branch, nil
}

// stubDestroySession captures invocations + can simulate errors.
type stubDestroySession struct {
	mu        sync.Mutex
	called    bool
	sessionID string
	err       error
}

func (d *stubDestroySession) Fn(ctx context.Context, instanceRoot, sessionID string, gi GitInvoker) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.called = true
	d.sessionID = sessionID
	// Mimic real teardown: remove the worktree dir + session JSON.
	statePath := filepath.Join(instanceRoot, ".niwa", "sessions", sessionID+".json")
	data, err := os.ReadFile(statePath)
	if err == nil {
		var st struct {
			WorktreePath string `json:"worktree_path"`
		}
		_ = json.Unmarshal(data, &st)
		if st.WorktreePath != "" {
			_ = os.RemoveAll(st.WorktreePath)
		}
		_ = os.Remove(statePath)
	}
	return d.err
}

// stubApplierCreate constructs an instance dir + writes a valid
// state.json so DestroyInstance's preconditions pass.
type stubApplierCreate struct {
	failWith error
	called   bool
}

func (a *stubApplierCreate) Fn(ctx context.Context, workspaceRoot, instanceName string) (string, error) {
	a.called = true
	if a.failWith != nil {
		return "", a.failWith
	}
	inst := filepath.Join(workspaceRoot, instanceName)
	if err := os.MkdirAll(filepath.Join(inst, ".niwa"), 0o755); err != nil {
		return "", err
	}
	// Write a minimal state.json so DestroyInstance's
	// ValidateInstanceDir guard accepts the dir if rollback fires.
	if err := os.WriteFile(filepath.Join(inst, ".niwa", "state.json"), []byte(`{"schema_version":1}`), 0o644); err != nil {
		return "", err
	}
	return inst, nil
}

// makeBootstrapParams builds a default BootstrapParams pointed at the
// test temp dir. Callers override the seam closures they want.
func makeBootstrapParams(t *testing.T, ws string, rec *recordingGitInvoker, ac ApplierCreateFunc, cs CreateSessionFunc, ds DestroySessionFunc, host string) BootstrapParams {
	t.Helper()
	src := source.Source{Host: host, Owner: "acme", Repo: "myproj"}
	if host == "" {
		src.Host = "" // canonical slug; IsGitHub returns true
	}
	return BootstrapParams{
		WorkspaceRoot: ws,
		WorkspaceName: "myproj",
		InstanceName:  "myproj",
		Src:           src,
		GitInvoker:    rec,
		Reporter:      NewReporter(io.Discard),
		ScaffoldOpts: ScaffoldOptions{
			Name:           "myproj",
			Org:            "acme",
			Repo:           "myproj",
			Private:        false,
			IncludeGitkeep: true,
		},
		ApplierCreate:  ac,
		CreateSession:  cs,
		DestroySession: ds,
	}
}

// TestRunBootstrap_HostCheckRecordsNoGitCalls asserts the defense-in-depth
// IsGitHub gate at the head of RunBootstrap: a non-GitHub source must
// produce ZERO GitInvoker calls before returning the error.
func TestRunBootstrap_HostCheckRecordsNoGitCalls(t *testing.T) {
	ws := t.TempDir()
	rec := &recordingGitInvoker{}
	ac := &stubApplierCreate{}
	cs := &stubCreateSession{}
	ds := &stubDestroySession{}
	params := makeBootstrapParams(t, ws, rec, ac.Fn, cs.Fn, ds.Fn, "gitlab.com")
	_, err := RunBootstrap(context.Background(), params)
	if err == nil {
		t.Fatal("expected error on non-GitHub host")
	}
	if !errors.Is(err, ErrBootstrapNonGitHub) {
		t.Errorf("error chain missing ErrBootstrapNonGitHub: %v", err)
	}
	invs, _ := rec.snapshot()
	if len(invs) != 0 {
		t.Errorf("recorded %d git invocations, want 0 (host check must precede every git call)", len(invs))
		for _, i := range invs {
			t.Logf("  argv: %v", i.Args)
		}
	}
	if ac.called {
		t.Error("ApplierCreate was called despite host-check failure")
	}
	if cs.called {
		t.Error("CreateSession was called despite host-check failure")
	}
}

// TestRunBootstrap_HappyPath_ByteIdenticalScaffolds asserts the two
// scaffold writes (caller-side and worktree-side) produce byte-identical
// .niwa/workspace.toml content. The test writes the first copy itself
// (mirroring runInit's pre-RunBootstrap step), then invokes RunBootstrap
// with stubs and compares the two on-disk files afterward.
func TestRunBootstrap_HappyPath_ByteIdenticalScaffolds(t *testing.T) {
	ws := t.TempDir()
	rec := &recordingGitInvoker{}
	ac := &stubApplierCreate{}
	cs := &stubCreateSession{}
	ds := &stubDestroySession{}
	params := makeBootstrapParams(t, ws, rec, ac.Fn, cs.Fn, ds.Fn, "")

	// FIRST scaffold write — runInit-owned. RunBootstrap won't write
	// this one (per design); the test simulates runInit doing it before
	// calling RunBootstrap.
	if err := ScaffoldFromSource(ws, params.ScaffoldOpts); err != nil {
		t.Fatalf("pre-RunBootstrap scaffold: %v", err)
	}

	res, err := RunBootstrap(context.Background(), params)
	if err != nil {
		t.Fatalf("RunBootstrap: %v", err)
	}

	a, errA := os.ReadFile(filepath.Join(ws, ".niwa", "workspace.toml"))
	if errA != nil {
		t.Fatalf("read workspace-root toml: %v", errA)
	}
	b, errB := os.ReadFile(filepath.Join(res.WorktreePath, ".niwa", "workspace.toml"))
	if errB != nil {
		t.Fatalf("read worktree toml: %v", errB)
	}
	if string(a) != string(b) {
		t.Errorf("scaffold writes not byte-identical\n  ws: %q\n  wt: %q", a, b)
	}

	// .gitkeep symmetry too — both should exist.
	if _, err := os.Stat(filepath.Join(ws, ".niwa", "claude", ".gitkeep")); err != nil {
		t.Errorf(".gitkeep missing under workspace root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(res.WorktreePath, ".niwa", "claude", ".gitkeep")); err != nil {
		t.Errorf(".gitkeep missing under worktree: %v", err)
	}
}

// TestRunBootstrap_R24_NoPush asserts the happy-path produces no
// `git push` invocations across the entire orchestrator.
func TestRunBootstrap_R24_NoPush(t *testing.T) {
	ws := t.TempDir()
	rec := &recordingGitInvoker{}
	params := makeBootstrapParams(t, ws,
		rec,
		(&stubApplierCreate{}).Fn,
		(&stubCreateSession{}).Fn,
		(&stubDestroySession{}).Fn,
		"")
	if err := ScaffoldFromSource(ws, params.ScaffoldOpts); err != nil {
		t.Fatal(err)
	}
	if _, err := RunBootstrap(context.Background(), params); err != nil {
		t.Fatalf("RunBootstrap: %v", err)
	}
	invs, _ := rec.snapshot()
	for _, inv := range invs {
		for _, a := range inv.Args {
			if a == "push" {
				t.Errorf("found `git push` in invocation %v", inv.Args)
			}
		}
	}
}

// TestRunBootstrap_R5_BranchNameInSessionState asserts the session
// state JSON contains the `niwa-bootstrap/<sid>` branch_name field.
func TestRunBootstrap_R5_BranchNameInSessionState(t *testing.T) {
	ws := t.TempDir()
	rec := &recordingGitInvoker{}
	ac := &stubApplierCreate{}
	cs := &stubCreateSession{}
	ds := &stubDestroySession{}
	params := makeBootstrapParams(t, ws, rec, ac.Fn, cs.Fn, ds.Fn, "")
	if err := ScaffoldFromSource(ws, params.ScaffoldOpts); err != nil {
		t.Fatal(err)
	}
	res, err := RunBootstrap(context.Background(), params)
	if err != nil {
		t.Fatalf("RunBootstrap: %v", err)
	}

	statePath := filepath.Join(res.InstancePath, ".niwa", "sessions", res.SessionID+".json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read session state: %v", err)
	}
	var st struct {
		BranchName string `json:"branch_name"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("parse session state: %v", err)
	}
	want := "niwa-bootstrap/" + res.SessionID
	if st.BranchName != want {
		t.Errorf("branch_name = %q, want %q", st.BranchName, want)
	}
}

// TestRunBootstrap_R18_NoAuthorArgNoAuthorEnv asserts the commit
// invocation contains no --author flag and no GIT_AUTHOR_* /
// GIT_COMMITTER_* env entries, even when the parent process exports
// such variables. A wrong implementation `cmd.Env = os.Environ()`
// inherits them and fails this test.
func TestRunBootstrap_R18_NoAuthorArgNoAuthorEnv(t *testing.T) {
	// Parent env: explicitly set the variables a malicious caller would
	// use to inject identity.
	t.Setenv("GIT_AUTHOR_NAME", "injected-by-parent")
	t.Setenv("GIT_COMMITTER_EMAIL", "evil@example.com")
	t.Setenv("GIT_AUTHOR_DATE", "2000-01-01T00:00:00Z")

	ws := t.TempDir()
	rec := &recordingGitInvoker{}
	params := makeBootstrapParams(t, ws,
		rec,
		(&stubApplierCreate{}).Fn,
		(&stubCreateSession{}).Fn,
		(&stubDestroySession{}).Fn,
		"")
	if err := ScaffoldFromSource(ws, params.ScaffoldOpts); err != nil {
		t.Fatal(err)
	}
	if _, err := RunBootstrap(context.Background(), params); err != nil {
		t.Fatalf("RunBootstrap: %v", err)
	}

	invs, cmds := rec.snapshot()
	if len(invs) == 0 {
		t.Fatal("no git invocations recorded")
	}
	// Find the commit invocation.
	commitRe := regexp.MustCompile(`^GIT_(AUTHOR|COMMITTER)_(NAME|EMAIL|DATE)=`)
	found := false
	for i, inv := range invs {
		isCommit := false
		for _, a := range inv.Args {
			if a == "commit" {
				isCommit = true
				break
			}
		}
		if !isCommit {
			continue
		}
		found = true
		for _, a := range inv.Args {
			if a == "--author" {
				t.Error("commit invocation contains --author")
			}
		}
		// Inspect the cmd's Env (populated by RunBootstrap's
		// sanitizeCommitEnv assignment).
		if i >= len(cmds) {
			t.Fatalf("recorded %d cmds but commit at index %d", len(cmds), i)
		}
		for _, kv := range cmds[i].Env {
			if commitRe.MatchString(kv) {
				t.Errorf("commit cmd.Env leaked %q (parent env must be filtered)", kv)
			}
		}
	}
	if !found {
		t.Error("no git commit invocation recorded")
	}
}

// TestRunBootstrap_CreateStepFails_PreservesScaffold asserts the
// runInit-owned scaffold survives a create-step failure. This proves
// the disarm-after-scaffold ordering is correct: if `runInit` left
// `workspaceCreated = true` until after RunBootstrap returned, a
// create-step failure would delete the user's workspace.
//
// Test setup: the runInit-equivalent flow writes the scaffold itself,
// THEN calls RunBootstrap with an ApplierCreate that fails. After
// RunBootstrap returns, the test asserts the scaffold file still
// exists on disk.
func TestRunBootstrap_CreateStepFails_PreservesScaffold(t *testing.T) {
	ws := t.TempDir()
	rec := &recordingGitInvoker{}
	ac := &stubApplierCreate{failWith: errors.New("simulated create-step failure")}
	cs := &stubCreateSession{}
	ds := &stubDestroySession{}
	params := makeBootstrapParams(t, ws, rec, ac.Fn, cs.Fn, ds.Fn, "")
	if err := ScaffoldFromSource(ws, params.ScaffoldOpts); err != nil {
		t.Fatal(err)
	}

	_, err := RunBootstrap(context.Background(), params)
	if err == nil {
		t.Fatal("expected create-step error")
	}

	// The scaffold MUST still exist — that is the load-bearing assertion.
	configPath := filepath.Join(ws, ".niwa", "workspace.toml")
	if _, statErr := os.Stat(configPath); statErr != nil {
		t.Errorf("scaffold removed after create-step failure (R7 violation): %v", statErr)
	}
	// No instance dir should remain (the instance-rollback defer
	// never armed because ApplierCreate returned an error before
	// creating one).
	if _, statErr := os.Stat(filepath.Join(ws, "myproj")); !os.IsNotExist(statErr) {
		t.Errorf("instance dir present after failed create-step: %v", statErr)
	}
	// CreateSession must not have fired.
	if cs.called {
		t.Error("CreateSession invoked despite create-step failure")
	}
}

// TestRunBootstrap_SessionStepFails_PreservesInstance asserts that a
// CreateSession failure preserves the instance dir + scaffold and
// invokes neither the commit nor DestroySession.
func TestRunBootstrap_SessionStepFails_PreservesInstance(t *testing.T) {
	ws := t.TempDir()
	rec := &recordingGitInvoker{}
	ac := &stubApplierCreate{}
	cs := &stubCreateSession{failOnEntry: errors.New("simulated session-create failure")}
	ds := &stubDestroySession{}
	params := makeBootstrapParams(t, ws, rec, ac.Fn, cs.Fn, ds.Fn, "")
	if err := ScaffoldFromSource(ws, params.ScaffoldOpts); err != nil {
		t.Fatal(err)
	}
	_, err := RunBootstrap(context.Background(), params)
	if err == nil {
		t.Fatal("expected session-create error")
	}

	// Workspace scaffold preserved.
	if _, statErr := os.Stat(filepath.Join(ws, ".niwa", "workspace.toml")); statErr != nil {
		t.Errorf("scaffold removed after session-step failure: %v", statErr)
	}
	// Instance dir preserved per PRD R7 session-step rollback contract.
	if _, statErr := os.Stat(filepath.Join(ws, "myproj")); statErr != nil {
		t.Errorf("instance dir removed on session-step failure (R7 says preserve): %v", statErr)
	}
	// No worktree was created (CreateSession failed at entry).
	if _, statErr := os.Stat(filepath.Join(ws, "myproj", ".niwa", "worktrees")); !os.IsNotExist(statErr) {
		t.Errorf("worktrees dir present after session-create failure: %v", statErr)
	}
}

// TestRunBootstrap_CommitFails_RollsBackSession asserts that a commit
// failure after a successful CreateSession triggers DestroySession to
// remove the worktree and branch and session state JSON, while
// preserving the workspace dir + instance dir.
func TestRunBootstrap_CommitFails_RollsBackSession(t *testing.T) {
	ws := t.TempDir()
	rec := &recordingGitInvoker{
		faultFn: func(args []string) []string {
			for _, a := range args {
				if a == "commit" {
					return []string{"false"} // exit 1
				}
			}
			return nil
		},
	}
	ac := &stubApplierCreate{}
	cs := &stubCreateSession{}
	ds := &stubDestroySession{}
	params := makeBootstrapParams(t, ws, rec, ac.Fn, cs.Fn, ds.Fn, "")
	if err := ScaffoldFromSource(ws, params.ScaffoldOpts); err != nil {
		t.Fatal(err)
	}
	_, err := RunBootstrap(context.Background(), params)
	if err == nil {
		t.Fatal("expected commit-step error")
	}
	if !ds.called {
		t.Error("DestroySession was not invoked on commit failure")
	}
	if ds.sessionID == "" {
		t.Error("DestroySession received empty sessionID")
	}
	// Workspace scaffold preserved.
	if _, statErr := os.Stat(filepath.Join(ws, ".niwa", "workspace.toml")); statErr != nil {
		t.Errorf("scaffold removed on commit failure: %v", statErr)
	}
}
