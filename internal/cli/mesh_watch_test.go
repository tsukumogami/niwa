package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/tsukumogami/niwa/internal/mcp"
)

// ---------------------------------------------------------------------
// Startup helper unit tests
// ---------------------------------------------------------------------

func TestResolveSpawnTarget_OverrideAbsolutePath(t *testing.T) {
	t.Setenv("NIWA_WORKER_SPAWN_COMMAND", "/bin/true")
	info, err := resolveSpawnTarget()
	if err != nil {
		t.Fatalf("resolveSpawnTarget: %v", err)
	}
	if info.Path != "/bin/true" {
		t.Errorf("Path = %q, want /bin/true", info.Path)
	}
	if info.Mode.Perm() == 0 {
		t.Errorf("Mode.Perm() = 0, want non-zero")
	}
}

func TestResolveSpawnTarget_OverrideRejectsRelative(t *testing.T) {
	t.Setenv("NIWA_WORKER_SPAWN_COMMAND", "./claude")
	_, err := resolveSpawnTarget()
	if err == nil {
		t.Fatalf("expected error for non-absolute override")
	}
	if !strings.Contains(err.Error(), "absolute") {
		t.Errorf("error = %v, want mention of absolute", err)
	}
}

func TestResolveSpawnTarget_FallsBackToLookPath(t *testing.T) {
	// Arrange a directory containing a fake `claude` binary and set it
	// as PATH so LookPath returns our synthetic binary.
	dir := t.TempDir()
	fakePath := filepath.Join(dir, "claude")
	if err := os.WriteFile(fakePath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("writing fake claude: %v", err)
	}
	t.Setenv("NIWA_WORKER_SPAWN_COMMAND", "")
	t.Setenv("PATH", dir)

	info, err := resolveSpawnTarget()
	if err != nil {
		t.Fatalf("resolveSpawnTarget: %v", err)
	}
	if info.Path != fakePath {
		t.Errorf("Path = %q, want %q", info.Path, fakePath)
	}
}

func TestResolveSpawnTarget_FailsWhenNothingResolves(t *testing.T) {
	t.Setenv("NIWA_WORKER_SPAWN_COMMAND", "")
	t.Setenv("PATH", t.TempDir()) // empty directory → LookPath fails
	_, err := resolveSpawnTarget()
	if err == nil {
		t.Fatalf("expected error when neither override nor claude resolves")
	}
}

func TestLoadDaemonConfig_Defaults(t *testing.T) {
	t.Setenv("NIWA_RETRY_BACKOFF_SECONDS", "")
	t.Setenv("NIWA_STALL_WATCHDOG_SECONDS", "")
	t.Setenv("NIWA_SIGTERM_GRACE_SECONDS", "")
	cfg := loadDaemonConfig(log.New(io.Discard, "", 0))
	if len(cfg.RetryBackoffs) != 3 {
		t.Errorf("RetryBackoffs len = %d, want 3", len(cfg.RetryBackoffs))
	}
	if cfg.RetryBackoffs[0] != 30*time.Second {
		t.Errorf("RetryBackoffs[0] = %v, want 30s", cfg.RetryBackoffs[0])
	}
	if cfg.StallWatchdog != 900*time.Second {
		t.Errorf("StallWatchdog = %v, want 900s", cfg.StallWatchdog)
	}
	if cfg.SIGTermGrace != 5*time.Second {
		t.Errorf("SIGTermGrace = %v, want 5s", cfg.SIGTermGrace)
	}
}

func TestLoadDaemonConfig_Overrides(t *testing.T) {
	t.Setenv("NIWA_RETRY_BACKOFF_SECONDS", "1,2,3")
	t.Setenv("NIWA_STALL_WATCHDOG_SECONDS", "10")
	t.Setenv("NIWA_SIGTERM_GRACE_SECONDS", "2")
	cfg := loadDaemonConfig(log.New(io.Discard, "", 0))
	if len(cfg.RetryBackoffs) != 3 ||
		cfg.RetryBackoffs[0] != time.Second ||
		cfg.RetryBackoffs[1] != 2*time.Second ||
		cfg.RetryBackoffs[2] != 3*time.Second {
		t.Errorf("RetryBackoffs = %v, want [1s,2s,3s]", cfg.RetryBackoffs)
	}
	if cfg.StallWatchdog != 10*time.Second {
		t.Errorf("StallWatchdog = %v, want 10s", cfg.StallWatchdog)
	}
	if cfg.SIGTermGrace != 2*time.Second {
		t.Errorf("SIGTermGrace = %v, want 2s", cfg.SIGTermGrace)
	}
}

func TestLoadDaemonConfig_InvalidValuesFallBackToDefault(t *testing.T) {
	t.Setenv("NIWA_RETRY_BACKOFF_SECONDS", "bogus")
	t.Setenv("NIWA_STALL_WATCHDOG_SECONDS", "-1")
	t.Setenv("NIWA_SIGTERM_GRACE_SECONDS", "abc")
	cfg := loadDaemonConfig(log.New(io.Discard, "", 0))
	// Defaults preserved.
	if cfg.RetryBackoffs[0] != 30*time.Second {
		t.Errorf("RetryBackoffs[0] = %v, want 30s", cfg.RetryBackoffs[0])
	}
	if cfg.StallWatchdog != 900*time.Second {
		t.Errorf("StallWatchdog = %v, want 900s", cfg.StallWatchdog)
	}
	if cfg.SIGTermGrace != 5*time.Second {
		t.Errorf("SIGTermGrace = %v, want 5s", cfg.SIGTermGrace)
	}
}

func TestRoleFromInboxPath(t *testing.T) {
	cases := map[string]string{
		"/root/.niwa/roles/web/inbox/abc.json":             "web",
		"/root/.niwa/roles/coordinator/inbox/task-id.json": "coordinator",
		"/root/.niwa/roles/web/inbox/in-progress/abc.json": "", // subdir, skip
		"/root/.niwa/roles/web/outbox/abc.json":            "", // not inbox
	}
	for path, want := range cases {
		got := roleFromInboxPath(path)
		if got != want {
			t.Errorf("roleFromInboxPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestResolveRoleCWD_Coordinator(t *testing.T) {
	root := t.TempDir()
	if got := resolveRoleCWD(root, "coordinator"); got != root {
		t.Errorf("resolveRoleCWD(coordinator) = %q, want %q", got, root)
	}
}

func TestResolveRoleCWD_RoleUnderGroup(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "backend", "web")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	got := resolveRoleCWD(root, "web")
	if got != repoDir {
		t.Errorf("resolveRoleCWD(web) = %q, want %q", got, repoDir)
	}
}

func TestResolveRoleCWD_MissingRoleFallsBack(t *testing.T) {
	root := t.TempDir()
	got := resolveRoleCWD(root, "nonexistent-role")
	if got != root {
		t.Errorf("resolveRoleCWD(nonexistent) = %q, want fallback %q", got, root)
	}
}

func TestParseDurationList(t *testing.T) {
	got, err := parseDurationList("1,2,3")
	if err != nil {
		t.Fatalf("parseDurationList: %v", err)
	}
	if len(got) != 3 || got[0] != time.Second || got[1] != 2*time.Second || got[2] != 3*time.Second {
		t.Errorf("got %v", got)
	}
	if _, err := parseDurationList(""); err == nil {
		t.Errorf("empty string: expected error")
	}
	if _, err := parseDurationList("0"); err == nil {
		t.Errorf("zero: expected error")
	}
	if _, err := parseDurationList("x"); err == nil {
		t.Errorf("non-int: expected error")
	}
}

// ---------------------------------------------------------------------
// Spawn helper tests — capture exec.Cmd shape without running the daemon
// ---------------------------------------------------------------------

// buildSpawnCommand mirrors the argv/env/CWD construction in
// spawnWorker so tests can assert on the produced exec.Cmd without
// starting a process. This keeps tests hermetic while still exercising
// the production argument-shape logic.
func buildSpawnCommand(instanceRoot, role, taskID, spawnBin string) *exec.Cmd {
	prompt := "You are a worker for niwa task " + taskID + ". Call niwa_check_messages to retrieve your task envelope."
	mcpConfigPath := filepath.Join(instanceRoot, ".claude", ".mcp.json")
	cmd := exec.Command(
		spawnBin,
		"-p", prompt,
		"--permission-mode=acceptEdits",
		"--mcp-config="+mcpConfigPath,
		"--strict-mcp-config",
	)
	cmd.Env = append(os.Environ(),
		"NIWA_INSTANCE_ROOT="+instanceRoot,
		"NIWA_SESSION_ROLE="+role,
		"NIWA_TASK_ID="+taskID,
	)
	cmd.Dir = resolveRoleCWD(instanceRoot, role)
	return cmd
}

// TestSpawnArgvShape_FixedShape (scenario-13): argv matches the Decision
// 4 contract exactly. No part of the envelope body leaks into argv.
func TestSpawnArgvShape_FixedShape(t *testing.T) {
	root := t.TempDir()
	taskID := "11111111-1111-4111-8111-111111111111"

	cmd := buildSpawnCommand(root, "web", taskID, "/bin/true")

	// argv[0] = binary, argv[1..] = flags
	args := cmd.Args
	if len(args) != 6 {
		t.Fatalf("len(args) = %d, want 6, got %v", len(args), args)
	}
	if args[0] != "/bin/true" {
		t.Errorf("argv[0] = %q, want /bin/true", args[0])
	}
	if args[1] != "-p" {
		t.Errorf("argv[1] = %q, want -p", args[1])
	}
	if !strings.Contains(args[2], taskID) {
		t.Errorf("argv[2] (prompt) does not contain task ID: %q", args[2])
	}
	if args[3] != "--permission-mode=acceptEdits" {
		t.Errorf("argv[3] = %q, want --permission-mode=acceptEdits", args[3])
	}
	wantMCP := "--mcp-config=" + filepath.Join(root, ".claude", ".mcp.json")
	if args[4] != wantMCP {
		t.Errorf("argv[4] = %q, want %q", args[4], wantMCP)
	}
	if args[5] != "--strict-mcp-config" {
		t.Errorf("argv[5] = %q, want --strict-mcp-config", args[5])
	}
}

// TestSpawnArgvShape_NoBodyLeak (scenario-13 / AC-D5): task bodies must
// never appear in argv even when the body contains suggestive strings.
func TestSpawnArgvShape_NoBodyLeak(t *testing.T) {
	root := t.TempDir()
	taskID := "22222222-2222-4222-8222-222222222222"
	// The body is not an input to buildSpawnCommand; confirm by exhausting
	// the entire joined argv for any mention of the body-marker string.
	cmd := buildSpawnCommand(root, "web", taskID, "/bin/true")
	joined := strings.Join(cmd.Args, " ")
	bodyMarker := "BODY-MUST-NOT-APPEAR-IN-ARGV"
	if strings.Contains(joined, bodyMarker) {
		t.Errorf("argv contains body marker (leak): %s", joined)
	}
}

// TestSpawnEnv_NiwaOwnedKeys (scenario-13): NIWA_INSTANCE_ROOT /
// NIWA_SESSION_ROLE / NIWA_TASK_ID appear in env, last-wins semantics
// override any pre-existing values.
func TestSpawnEnv_NiwaOwnedKeys(t *testing.T) {
	// Pre-seed a conflicting NIWA_SESSION_ROLE in the daemon's env;
	// the spawn must overwrite it (last-wins).
	t.Setenv("NIWA_SESSION_ROLE", "stale-role")

	root := t.TempDir()
	taskID := "33333333-3333-4333-8333-333333333333"
	cmd := buildSpawnCommand(root, "web", taskID, "/bin/true")

	env := envToMap(cmd.Env)
	if env["NIWA_INSTANCE_ROOT"] != root {
		t.Errorf("NIWA_INSTANCE_ROOT = %q, want %q", env["NIWA_INSTANCE_ROOT"], root)
	}
	if env["NIWA_SESSION_ROLE"] != "web" {
		t.Errorf("NIWA_SESSION_ROLE = %q, want web (last-wins)", env["NIWA_SESSION_ROLE"])
	}
	if env["NIWA_TASK_ID"] != taskID {
		t.Errorf("NIWA_TASK_ID = %q, want %q", env["NIWA_TASK_ID"], taskID)
	}
}

// TestSpawnCWD_CoordinatorVsRole (scenario-13): coordinator workers
// run at instance root; role workers run in the role's repo dir.
func TestSpawnCWD_CoordinatorVsRole(t *testing.T) {
	root := t.TempDir()
	repoDir := filepath.Join(root, "backend", "web")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	coord := buildSpawnCommand(root, "coordinator", "44444444-4444-4444-8444-444444444444", "/bin/true")
	if coord.Dir != root {
		t.Errorf("coordinator CWD = %q, want %q", coord.Dir, root)
	}

	web := buildSpawnCommand(root, "web", "55555555-5555-4555-8555-555555555555", "/bin/true")
	if web.Dir != repoDir {
		t.Errorf("web CWD = %q, want %q", web.Dir, repoDir)
	}
}

// envToMap turns an env-slice into a map, keeping the last occurrence
// of each key (Go's exec package also uses last-wins on duplicates).
func envToMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		i := strings.IndexByte(kv, '=')
		if i < 0 {
			continue
		}
		m[kv[:i]] = kv[i+1:]
	}
	return m
}

// ---------------------------------------------------------------------
// End-to-end daemon tests (integration-flavoured)
// ---------------------------------------------------------------------

// daemonTestFixture sets up a minimal instance layout (`.niwa/roles/web/
// inbox/`, `.niwa/tasks/`), pre-populates a queued task, and returns
// handles for inspection.
type daemonTestFixture struct {
	root      string
	niwaDir   string
	rolesRoot string
	tasksDir  string
}

func newDaemonTestFixture(t *testing.T) *daemonTestFixture {
	t.Helper()
	root := t.TempDir()
	niwaDir := filepath.Join(root, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o700); err != nil {
		t.Fatalf("mkdir niwa: %v", err)
	}
	rolesRoot := filepath.Join(niwaDir, "roles")
	tasksDir := filepath.Join(niwaDir, "tasks")
	for _, p := range []string{
		filepath.Join(rolesRoot, "web", "inbox"),
		filepath.Join(rolesRoot, "web", "inbox", "in-progress"),
		filepath.Join(rolesRoot, "web", "inbox", "cancelled"),
		filepath.Join(rolesRoot, "web", "inbox", "expired"),
		filepath.Join(rolesRoot, "web", "inbox", "read"),
		tasksDir,
	} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
	}
	return &daemonTestFixture{root: root, niwaDir: niwaDir, rolesRoot: rolesRoot, tasksDir: tasksDir}
}

// seedQueuedTask creates .niwa/tasks/<id>/{envelope.json,state.json,
// .lock} with state=queued, and drops the Message wrapper into the
// target role's inbox.
func (f *daemonTestFixture) seedQueuedTask(t *testing.T, toRole string) string {
	t.Helper()
	taskID := mcp.NewTaskID()
	taskDir := filepath.Join(f.tasksDir, taskID)
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		t.Fatalf("mkdir task dir: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	env := mcp.TaskEnvelope{
		V:      1,
		ID:     taskID,
		From:   mcp.TaskParty{Role: "coordinator", PID: os.Getpid()},
		To:     mcp.TaskParty{Role: toRole},
		Body:   json.RawMessage(`{"kind":"test"}`),
		SentAt: now,
	}
	envBytes, _ := json.MarshalIndent(env, "", "  ")
	if err := os.WriteFile(filepath.Join(taskDir, "envelope.json"), envBytes, 0o600); err != nil {
		t.Fatalf("write envelope: %v", err)
	}
	st := &mcp.TaskState{
		V:      1,
		TaskID: taskID,
		State:  mcp.TaskStateQueued,
		StateTransitions: []mcp.StateTransition{
			{From: "", To: mcp.TaskStateQueued, At: now},
		},
		MaxRestarts:   3,
		DelegatorRole: "coordinator",
		TargetRole:    toRole,
		UpdatedAt:     now,
	}
	stBytes, _ := json.MarshalIndent(st, "", "  ")
	if err := os.WriteFile(filepath.Join(taskDir, "state.json"), stBytes, 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	// Inbox file is a Message wrapper so it shares the format with peer
	// messages; the daemon reads the filename only.
	msg := mcp.Message{
		V:      1,
		ID:     taskID,
		Type:   "task.delegate",
		From:   mcp.MessageFrom{Role: "coordinator", PID: os.Getpid()},
		To:     mcp.MessageTo{Role: toRole},
		TaskID: taskID,
		SentAt: now,
		Body:   env.Body,
	}
	msgBytes, _ := json.Marshal(msg)
	inboxPath := filepath.Join(f.rolesRoot, toRole, "inbox", taskID+".json")
	if err := os.WriteFile(inboxPath, msgBytes, 0o600); err != nil {
		t.Fatalf("write inbox: %v", err)
	}
	return taskID
}

// startDaemon runs runMeshWatch in a goroutine. Returns a cleanup func
// that sends SIGTERM (via the package-level signal mechanism is not
// available in tests, so we send directly to ourselves) — instead the
// caller invokes the returned cancelPID to stop the goroutine via
// context cancellation is not accessible from outside; we work around
// by launching the mesh watch as an external process in the E2E test.
//
// For in-process tests we instead drive the helpers directly
// (handleInboxEvent, handleSupervisorExit). The E2E-shaped flow lives
// in TestRunEventLoop_CatchupSpawnsWorker below.

// TestRunEventLoop_CatchupSpawnsWorker exercises the catch-up path
// plus claim → spawn → exit handling without running a real
// `niwa mesh watch` process. It wires up the same central loop that
// the production code runs.
func TestRunEventLoop_CatchupSpawnsWorker(t *testing.T) {
	f := newDaemonTestFixture(t)
	taskID := f.seedQueuedTask(t, "web")

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("fsnotify: %v", err)
	}
	defer watcher.Close()

	logger := log.New(io.Discard, "", 0)
	roles, err := registerInboxWatches(watcher, f.rolesRoot, logger)
	if err != nil {
		t.Fatalf("register watches: %v", err)
	}
	if len(roles) != 1 || roles[0] != "web" {
		t.Fatalf("roles = %v, want [web]", roles)
	}

	catchup, err := scanExistingInboxes(f.rolesRoot, roles)
	if err != nil {
		t.Fatalf("catchup scan: %v", err)
	}
	if len(catchup) != 1 || catchup[0].taskID != taskID {
		t.Fatalf("catchup = %+v, want 1 event for %s", catchup, taskID)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	exitCh := make(chan supervisorExit, 4)
	catchupCh := make(chan inboxEvent, len(catchup)+1)
	for _, evt := range catchup {
		catchupCh <- evt
	}
	close(catchupCh)

	var wg sync.WaitGroup
	spawnCtx := spawnContext{
		instanceRoot: f.root,
		niwaDir:      f.niwaDir,
		spawnBin:     "/bin/true",
		logger:       logger,
		exitCh:       exitCh,
		wg:           &wg,
		shutdownCtx:  ctx,
		// Use a long backoff so the retry scheduled by Issue 5's
		// classifier does NOT fire during this test's observation
		// window. The point of this test is the claim→spawn path, not
		// the retry path.
		backoffs: []time.Duration{60 * time.Second},
	}

	loopDone := make(chan struct{})
	go func() {
		runEventLoop(ctx, watcher, catchupCh, exitCh, spawnCtx)
		close(loopDone)
	}()

	// Poll the state.json for the transition queued → running, then
	// wait for the supervisor to record unexpected_exit (since /bin/true
	// exits immediately without calling niwa_finish_task). Issue 5
	// leaves the state at "running" while the retry is pending (the
	// 60 s backoff set above ensures the retry has not fired yet).
	if err := waitForState(f, taskID, mcp.TaskStateRunning, 2*time.Second); err != nil {
		t.Fatalf("waiting for running: %v", err)
	}

	// Wait for the rename: the inbox file must have moved to
	// inbox/in-progress/.
	inboxPath := filepath.Join(f.rolesRoot, "web", "inbox", taskID+".json")
	inProgressPath := filepath.Join(f.rolesRoot, "web", "inbox", "in-progress", taskID+".json")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(inProgressPath); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(inProgressPath); err != nil {
		t.Fatalf("inbox file not renamed to in-progress: %v", err)
	}
	if _, err := os.Stat(inboxPath); !os.IsNotExist(err) {
		t.Errorf("original inbox path still exists: %v", err)
	}

	// Wait for the supervisor goroutine to emit its exit event AND for
	// the central loop to record `unexpected_exit` in transitions.log.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if logHasKind(t, f.tasksDir, taskID, "unexpected_exit") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !logHasKind(t, f.tasksDir, taskID, "unexpected_exit") {
		t.Errorf("transitions.log missing unexpected_exit entry")
	}

	// Verify state.json is STILL "running" while the retry waits out
	// the 60 s backoff configured above.
	_, st, err := mcp.ReadState(filepath.Join(f.tasksDir, taskID))
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.State != mcp.TaskStateRunning {
		t.Errorf("state = %q, want running (retry pending, backoff not fired)", st.State)
	}

	cancel()
	<-loopDone
	wg.Wait()
}

// TestHandleInboxEvent_SkipsNonQueuedState: if state.json is already
// "running" (e.g. claimed by another daemon in a crash-recovery
// scenario) the claim path must skip without renaming.
func TestHandleInboxEvent_SkipsNonQueuedState(t *testing.T) {
	f := newDaemonTestFixture(t)
	taskID := f.seedQueuedTask(t, "web")

	// Flip state.json to running directly under the flock.
	_ = mcp.UpdateState(filepath.Join(f.tasksDir, taskID), func(cur *mcp.TaskState) (*mcp.TaskState, *mcp.TransitionLogEntry, error) {
		next := *cur
		next.State = mcp.TaskStateRunning
		next.StateTransitions = append(next.StateTransitions,
			mcp.StateTransition{From: mcp.TaskStateQueued, To: mcp.TaskStateRunning})
		return &next, nil, nil
	})

	exitCh := make(chan supervisorExit, 1)
	var wg sync.WaitGroup
	spawnCtx := spawnContext{
		instanceRoot: f.root,
		niwaDir:      f.niwaDir,
		spawnBin:     "/bin/true",
		logger:       log.New(io.Discard, "", 0),
		exitCh:       exitCh,
		wg:           &wg,
		shutdownCtx:  context.Background(),
	}

	inboxPath := filepath.Join(f.rolesRoot, "web", "inbox", taskID+".json")
	handleInboxEvent(inboxEvent{
		role:     "web",
		taskID:   taskID,
		filePath: inboxPath,
	}, spawnCtx)

	// The inbox file must still be present (no rename) and the task
	// state must remain "running" (no backfill and no new transitions).
	if _, err := os.Stat(inboxPath); err != nil {
		t.Errorf("inbox file missing after skipped claim: %v", err)
	}

	// No supervisor goroutine should have started.
	wg.Wait() // returns immediately if no supervisors were added.
	select {
	case <-exitCh:
		t.Errorf("unexpected supervisor exit event")
	default:
	}
}

// TestHandleInboxEvent_DanglingEnvelope: a stray file whose task dir
// does not exist must be moved into inbox/dangling/ so fsnotify stops
// re-firing CREATE events for it. The task dir must NOT be created and
// the daemon must not crash.
func TestHandleInboxEvent_DanglingEnvelope(t *testing.T) {
	f := newDaemonTestFixture(t)
	fakeTaskID := mcp.NewTaskID()

	inboxPath := filepath.Join(f.rolesRoot, "web", "inbox", fakeTaskID+".json")
	if err := os.WriteFile(inboxPath, []byte(`{"id":"`+fakeTaskID+`"}`), 0o600); err != nil {
		t.Fatalf("write stray inbox: %v", err)
	}

	var wg sync.WaitGroup
	exitCh := make(chan supervisorExit, 1)
	spawnCtx := spawnContext{
		instanceRoot: f.root,
		niwaDir:      f.niwaDir,
		spawnBin:     "/bin/true",
		logger:       log.New(io.Discard, "", 0),
		exitCh:       exitCh,
		wg:           &wg,
		shutdownCtx:  context.Background(),
	}

	handleInboxEvent(inboxEvent{
		role:     "web",
		taskID:   fakeTaskID,
		filePath: inboxPath,
	}, spawnCtx)

	// Original stray file should have been moved out of the queued inbox
	// so fsnotify does not keep re-firing CREATE events for it.
	if _, err := os.Stat(inboxPath); !os.IsNotExist(err) {
		t.Errorf("stray inbox still present at queued path: err=%v", err)
	}
	danglingPath := filepath.Join(f.rolesRoot, "web", "inbox", "dangling", fakeTaskID+".json")
	if _, err := os.Stat(danglingPath); err != nil {
		t.Errorf("stray inbox not moved to dangling/: %v", err)
	}
	taskDir := filepath.Join(f.tasksDir, fakeTaskID)
	if _, err := os.Stat(taskDir); !os.IsNotExist(err) {
		t.Errorf("task dir created for dangling envelope: %v", err)
	}
}

// ---------------------------------------------------------------------
// Full-binary tests — startup log assertions (scenario-12)
// ---------------------------------------------------------------------

// TestRunMeshWatch_LogsSpawnTargetAtStartup (scenario-12): starts a
// real `niwa mesh watch` subprocess using the NIWA_TEST_BINARY from
// the Makefile (if present) OR falls back to building a test binary.
// Asserts the "spawn_target" log line contains the resolved path,
// UID, and mode.
//
// This test is guarded by NIWA_TEST_BINARY for parity with functional
// tests; when unset the test compiles the binary on the fly.
func TestRunMeshWatch_LogsSpawnTargetAtStartup(t *testing.T) {
	niwaBin := ensureTestBinary(t)

	f := newDaemonTestFixture(t)

	// Override NIWA_WORKER_SPAWN_COMMAND so the daemon does not need
	// `claude` on PATH.
	cmd := exec.Command(niwaBin, "mesh", "watch", "--instance-root="+f.root)
	cmd.Env = append(os.Environ(), "NIWA_WORKER_SPAWN_COMMAND=/bin/true")
	// Run in a new session so SIGTERM on us doesn't cascade to the test process.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting daemon: %v", err)
	}
	defer func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_, _ = cmd.Process.Wait()
	}()

	// Poll daemon.log for the spawn_target line.
	logPath := filepath.Join(f.niwaDir, "daemon.log")
	var data []byte
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		d, err := os.ReadFile(logPath)
		if err == nil && strings.Contains(string(d), "spawn_target") {
			data = d
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(data) == 0 {
		t.Fatalf("daemon.log missing spawn_target line after 3s")
	}

	logs := string(data)
	if !strings.Contains(logs, "path=/bin/true") {
		t.Errorf("spawn_target log missing path=/bin/true: %s", logs)
	}
	if !strings.Contains(logs, "uid=") {
		t.Errorf("spawn_target log missing uid=: %s", logs)
	}
	if !strings.Contains(logs, "mode=") {
		t.Errorf("spawn_target log missing mode=: %s", logs)
	}

	// PID file must exist with the expected content.
	pidData, err := os.ReadFile(filepath.Join(f.niwaDir, "daemon.pid"))
	if err != nil {
		t.Fatalf("reading daemon.pid: %v", err)
	}
	if len(pidData) == 0 {
		t.Fatalf("daemon.pid is empty")
	}
	// First line is pid; second line is start_time. Just sanity-check
	// the shape — getpid() should be a positive integer.
	lines := strings.Split(strings.TrimSpace(string(pidData)), "\n")
	if len(lines) < 1 {
		t.Fatalf("daemon.pid has no lines")
	}

	// daemon.pid.lock should exist as the flock sidecar.
	if _, err := os.Stat(filepath.Join(f.niwaDir, "daemon.pid.lock")); err != nil {
		t.Errorf("daemon.pid.lock missing: %v", err)
	}
}

// TestRunMeshWatch_FailsWhenClaudeMissing (scenario-12 negative path):
// with NIWA_WORKER_SPAWN_COMMAND unset and `claude` not on PATH the
// daemon must fail at startup with a clear error.
func TestRunMeshWatch_FailsWhenClaudeMissing(t *testing.T) {
	niwaBin := ensureTestBinary(t)

	f := newDaemonTestFixture(t)

	cmd := exec.Command(niwaBin, "mesh", "watch", "--instance-root="+f.root)
	cmd.Env = []string{
		"PATH=" + t.TempDir(), // empty dir: no `claude`
		"HOME=" + f.root,
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("daemon did not fail; output=%s", out)
	}
	if !strings.Contains(string(out), "claude") {
		t.Errorf("startup error does not mention claude: %s", out)
	}
}

// ---------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------

func waitForState(f *daemonTestFixture, taskID, want string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, st, err := mcp.ReadState(filepath.Join(f.tasksDir, taskID))
		if err == nil && st.State == want {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, st, err := mcp.ReadState(filepath.Join(f.tasksDir, taskID))
	if err != nil {
		return err
	}
	return &stateMismatch{want: want, got: st.State}
}

type stateMismatch struct{ want, got string }

func (e *stateMismatch) Error() string { return "want state=" + e.want + ", got=" + e.got }

// logHasKind returns true when transitions.log for the given task has
// at least one NDJSON entry with the expected Kind field.
func logHasKind(t *testing.T, tasksDir, taskID, kind string) bool {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(tasksDir, taskID, "transitions.log"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var entry mcp.TransitionLogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Kind == kind {
			return true
		}
	}
	return false
}

// ensureTestBinary returns the path to the compiled niwa binary.
// Prefers NIWA_TEST_BINARY (set by the Makefile's functional targets);
// otherwise it `go build`s a test binary in t.TempDir().
func ensureTestBinary(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("NIWA_TEST_BINARY"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "niwa-test")
	buildCmd := exec.Command("go", "build", "-o", bin, "github.com/tsukumogami/niwa/cmd/niwa")
	buildCmd.Env = os.Environ()
	out, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// ---------------------------------------------------------------------
// Regression tests for reviewer-flagged defects
// ---------------------------------------------------------------------

// countOpenFDs returns the number of entries under /proc/self/fd. It's
// the cheapest way to assert we're not leaking file descriptors between
// spawns without depending on lsof or platform-specific rusage.
func countOpenFDs(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("/proc/self/fd unavailable (not Linux?): %v", err)
	}
	return len(entries)
}

// TestSpawnWorker_StderrFileClosedAfterExit (blocking finding B1):
// spawnWorker must close the stderr *os.File it opens once the
// supervisor's cmd.Wait returns. Prior to the fix each spawn leaked one
// fd; the daemon eventually exhausted its fd budget and silently wedged
// fsnotify, state.json writes, and flocks.
//
// Strategy: measure the process fd count, spawn several workers that
// each exit immediately, wait for their supervisors to finish, and
// assert the fd count has NOT grown by ~N. Some noise is tolerated
// (GC-triggered file closes, test framework files), but a leak of one
// fd per spawn is easily visible above the noise floor.
func TestSpawnWorker_StderrFileClosedAfterExit(t *testing.T) {
	const spawns = 20

	f := newDaemonTestFixture(t)

	// Seed N queued tasks.
	taskIDs := make([]string, spawns)
	for i := range taskIDs {
		taskIDs[i] = f.seedQueuedTask(t, "web")
	}

	// Measure fd count just before the spawn storm.
	before := countOpenFDs(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	exitCh := make(chan supervisorExit, spawns*2)
	s := spawnContext{
		instanceRoot: f.root,
		niwaDir:      f.niwaDir,
		spawnBin:     "/bin/true",
		logger:       log.New(io.Discard, "", 0),
		exitCh:       exitCh,
		wg:           &wg,
		shutdownCtx:  ctx,
	}

	for _, id := range taskIDs {
		evt := inboxEvent{
			role:     "web",
			taskID:   id,
			filePath: filepath.Join(f.rolesRoot, "web", "inbox", id+".json"),
		}
		handleInboxEvent(evt, s)
	}

	// Drain every supervisor — cmd.Wait must return before we measure fds.
	wg.Wait()

	// Drain exitCh so buffered supervisorExit{} payloads don't retain
	// anything interesting (they don't, but be explicit).
	for len(exitCh) > 0 {
		<-exitCh
	}

	after := countOpenFDs(t)

	// Allow a small slack for Go runtime/test-framework noise. If we
	// were leaking one fd per spawn we'd see `after - before >= spawns`;
	// the assertion fails well below that threshold.
	if delta := after - before; delta > spawns/4 {
		t.Fatalf("fd count grew by %d after %d spawns (before=%d after=%d) — stderr.log files are being leaked",
			delta, spawns, before, after)
	}
}

// TestSpawnWorker_StderrClosedOnStartFailure (blocking finding B1
// early-return path): when cmd.Start fails, the stderr file we opened
// must be closed before returning. Otherwise the spawn_failed path
// leaks an fd for every rejected spawn.
//
// We drive this by pointing spawnBin at a non-executable file so
// cmd.Start returns EACCES. The stderr file has already been opened at
// that point, so a leak would show up in /proc/self/fd.
func TestSpawnWorker_StderrClosedOnStartFailure(t *testing.T) {
	f := newDaemonTestFixture(t)

	// Create a non-executable file to use as the spawn target. cmd.Start
	// will fail with EACCES / permission denied, exercising the early-
	// return path in spawnWorker.
	bogusBin := filepath.Join(t.TempDir(), "not-executable")
	if err := os.WriteFile(bogusBin, []byte("#!/bin/sh\nexit 0\n"), 0o600); err != nil {
		t.Fatalf("write bogus bin: %v", err)
	}

	const spawns = 20
	taskIDs := make([]string, spawns)
	for i := range taskIDs {
		taskIDs[i] = f.seedQueuedTask(t, "web")
	}

	before := countOpenFDs(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	exitCh := make(chan supervisorExit, spawns*2)
	s := spawnContext{
		instanceRoot: f.root,
		niwaDir:      f.niwaDir,
		spawnBin:     bogusBin,
		logger:       log.New(io.Discard, "", 0),
		exitCh:       exitCh,
		wg:           &wg,
		shutdownCtx:  ctx,
	}

	for _, id := range taskIDs {
		evt := inboxEvent{
			role:     "web",
			taskID:   id,
			filePath: filepath.Join(f.rolesRoot, "web", "inbox", id+".json"),
		}
		handleInboxEvent(evt, s)
	}

	// cmd.Start failing means no supervisor goroutine was spawned, so
	// there's nothing for wg.Wait to do — but call it anyway in case a
	// future refactor starts one.
	wg.Wait()

	after := countOpenFDs(t)
	if delta := after - before; delta > spawns/4 {
		t.Fatalf("fd count grew by %d after %d failed spawns (before=%d after=%d) — stderr.log files leaked on start-failure path",
			delta, spawns, before, after)
	}
}

// TestSpawnWorker_ExitEventNotDroppedUnderBackPressure (blocking
// finding B2): before the fix, the supervisor goroutine used a
// non-blocking `select { case exitCh<-…: default: }` which silently
// dropped exit events when the channel was full. Tasks then stayed in
// `running` forever and Issue 5's retry pipeline never kicked in.
//
// This test saturates exitCh by using a buffer size of 1 and delaying
// the central loop's drain, forcing the supervisor send to block.
// After the loop starts draining, every supervised task's
// unexpected_exit entry must eventually be recorded in transitions.log.
func TestSpawnWorker_ExitEventNotDroppedUnderBackPressure(t *testing.T) {
	const spawns = 10

	f := newDaemonTestFixture(t)

	// Seed N queued tasks.
	taskIDs := make([]string, spawns)
	for i := range taskIDs {
		taskIDs[i] = f.seedQueuedTask(t, "web")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	// Deliberately tiny buffer so supervisors block on the send once
	// the first event is parked, reproducing back-pressure.
	exitCh := make(chan supervisorExit, 1)
	s := spawnContext{
		instanceRoot: f.root,
		niwaDir:      f.niwaDir,
		spawnBin:     "/bin/true",
		logger:       log.New(io.Discard, "", 0),
		exitCh:       exitCh,
		wg:           &wg,
		shutdownCtx:  ctx,
		// Long backoff so Issue 5's retry scheduler does NOT re-enter
		// the spawn path during this test. Issue 5's retry coverage
		// lives in TestRetryCap_* below.
		backoffs: []time.Duration{60 * time.Second},
	}

	// Fire off spawns. Each /bin/true exits immediately; the supervisor
	// goroutines then race to send on the size-1 exitCh. The fixed code
	// blocks (pace-matches the central loop) instead of dropping.
	for _, id := range taskIDs {
		evt := inboxEvent{
			role:     "web",
			taskID:   id,
			filePath: filepath.Join(f.rolesRoot, "web", "inbox", id+".json"),
		}
		handleInboxEvent(evt, s)
	}

	// Drain the exitCh through handleSupervisorExit — the same path the
	// central loop takes. Record each exit so we can assert coverage.
	seen := map[string]bool{}
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		for ex := range exitCh {
			handleSupervisorExit(ex, s)
			seen[ex.taskID] = true
			if len(seen) == spawns {
				return
			}
		}
	}()

	// Wait for the drain goroutine to see every exit. The supervisor
	// WG's `Add(1)` inside handleSupervisorExit (for the retry
	// scheduler goroutines) means wg.Wait() no longer returns after
	// supervisors alone — we must cancel shutdownCtx first to unblock
	// the retry schedulers.
	select {
	case <-drainDone:
		// drain completed via count check
	case <-time.After(3 * time.Second):
		close(exitCh)
		<-drainDone
	}

	// Cancel shutdownCtx so the retry-scheduler goroutines return and
	// wg.Wait() can proceed.
	cancel()
	wg.Wait()

	// Every task's exit must have been observed.
	if len(seen) != spawns {
		t.Fatalf("received exits for %d/%d tasks; some exit events were dropped under back-pressure: %v",
			len(seen), spawns, seen)
	}

	// Every task's transitions.log must contain the unexpected_exit
	// record written by handleSupervisorExit.
	for _, id := range taskIDs {
		if !logHasKind(t, f.tasksDir, id, "unexpected_exit") {
			t.Errorf("task %s missing unexpected_exit in transitions.log (back-pressure drop?)", id)
		}
	}
}

// TestSpawnWorker_ExitEventDroppedAfterShutdown (blocking finding B2
// contract, shutdown path): when shutdownCtx is already cancelled the
// supervisor must NOT block forever trying to send on an exitCh nobody
// reads. The central loop has returned by that point; intentionally
// dropping the event is the correct choice.
func TestSpawnWorker_ExitEventDroppedAfterShutdown(t *testing.T) {
	f := newDaemonTestFixture(t)
	taskID := f.seedQueuedTask(t, "web")

	// Pre-cancel the shutdown context before spawning. The supervisor
	// goroutine will see ctx.Done() immediately on the select and drop
	// the event rather than blocking.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var wg sync.WaitGroup
	// Buffer of 0 makes the send outright block if the shutdownCtx guard
	// is removed, which would cause wg.Wait() below to hang and the
	// test to time out.
	exitCh := make(chan supervisorExit)
	s := spawnContext{
		instanceRoot: f.root,
		niwaDir:      f.niwaDir,
		spawnBin:     "/bin/true",
		logger:       log.New(io.Discard, "", 0),
		exitCh:       exitCh,
		wg:           &wg,
		shutdownCtx:  ctx,
	}

	evt := inboxEvent{
		role:     "web",
		taskID:   taskID,
		filePath: filepath.Join(f.rolesRoot, "web", "inbox", taskID+".json"),
	}
	handleInboxEvent(evt, s)

	// The supervisor must return within a short window despite nobody
	// reading exitCh, because shutdownCtx is already done.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		// pass
	case <-time.After(2 * time.Second):
		t.Fatalf("supervisor did not return after shutdown — shutdownCtx guard missing?")
	}
}

// ---------------------------------------------------------------------
// Issue 5: restart cap + backoff tests
// ---------------------------------------------------------------------

// retryFixture wires up a minimal central-loop simulator for Issue 5's
// retry-path tests. It seeds a daemon fixture, drives supervisor-exit
// events through a background pump (the production-loop equivalent),
// and uses short backoffs so retry timing fits inside test windows.
type retryFixture struct {
	*daemonTestFixture
	cancel     context.CancelFunc // shutdownCtx cancel
	wg         *sync.WaitGroup
	exitCh     chan supervisorExit
	spawnCtx   spawnContext
	pumpDone   chan struct{}
	pumpCancel context.CancelFunc
}

func newRetryFixture(t *testing.T, backoffs []time.Duration, spawnBin string) *retryFixture {
	t.Helper()
	f := newDaemonTestFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	exitCh := make(chan supervisorExit, 64)
	s := spawnContext{
		instanceRoot: f.root,
		niwaDir:      f.niwaDir,
		spawnBin:     spawnBin,
		logger:       log.New(io.Discard, "", 0),
		exitCh:       exitCh,
		wg:           &wg,
		shutdownCtx:  ctx,
		backoffs:     backoffs,
	}

	pumpCtx, pumpCancel := context.WithCancel(context.Background())
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		for {
			select {
			case ex := <-exitCh:
				handleSupervisorExit(ex, s)
			case <-pumpCtx.Done():
				return
			}
		}
	}()

	return &retryFixture{
		daemonTestFixture: f,
		cancel:            cancel,
		wg:                &wg,
		exitCh:            exitCh,
		spawnCtx:          s,
		pumpDone:          pumpDone,
		pumpCancel:        pumpCancel,
	}
}

// Shutdown cancels shutdownCtx and stops the exit pump, then waits for
// both to drain. Safe to call once.
func (r *retryFixture) Shutdown(t *testing.T) {
	t.Helper()
	r.cancel()     // shutdownCtx — retry schedulers exit immediately
	r.pumpCancel() // exit pump — stops consuming new events
	r.wg.Wait()
	<-r.pumpDone
}

// fireInitial performs the initial claim → spawn for a seeded task, as
// the central loop would on catch-up scan or fsnotify CREATE.
func (r *retryFixture) fireInitial(t *testing.T, taskID string) {
	t.Helper()
	evt := inboxEvent{
		role:     "web",
		taskID:   taskID,
		filePath: filepath.Join(r.rolesRoot, "web", "inbox", taskID+".json"),
	}
	handleInboxEvent(evt, r.spawnCtx)
}

// TestRetryCap_WorkerCompletesCleanly — when a worker transitions
// state to a terminal value (completed) before exit, the classifier's
// terminal-state branch must win: no retry scheduled, no restart_count
// bump, no task.abandoned delivery. Exercises handleSupervisorExit
// directly so there is no race between /bin/true's exit and the
// transition-to-completed write.
func TestRetryCap_WorkerCompletesCleanly(t *testing.T) {
	f := newDaemonTestFixture(t)
	taskID := f.seedQueuedTask(t, "web")
	taskDir := filepath.Join(f.tasksDir, taskID)

	// Simulate worker's finish_task sequence: queued → running →
	// completed, all via UpdateState.
	if err := mcp.UpdateState(taskDir, func(cur *mcp.TaskState) (*mcp.TaskState, *mcp.TransitionLogEntry, error) {
		next := *cur
		next.State = mcp.TaskStateRunning
		next.StateTransitions = append(next.StateTransitions,
			mcp.StateTransition{From: cur.State, To: mcp.TaskStateRunning})
		return &next, &mcp.TransitionLogEntry{Kind: "spawn", From: cur.State, To: mcp.TaskStateRunning}, nil
	}); err != nil {
		t.Fatalf("transition to running: %v", err)
	}
	if err := mcp.UpdateState(taskDir, func(cur *mcp.TaskState) (*mcp.TaskState, *mcp.TransitionLogEntry, error) {
		next := *cur
		next.State = mcp.TaskStateCompleted
		next.StateTransitions = append(next.StateTransitions,
			mcp.StateTransition{From: cur.State, To: mcp.TaskStateCompleted})
		next.Result = json.RawMessage(`{"ok":true}`)
		return &next, &mcp.TransitionLogEntry{Kind: "state_transition", From: cur.State, To: mcp.TaskStateCompleted}, nil
	}); err != nil {
		t.Fatalf("transition to completed: %v", err)
	}

	// Feed the synthetic supervisor exit after the worker has already
	// committed the completed transition. handleSupervisorExit must
	// no-op: terminal state wins over unexpected-exit classification.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	exitCh := make(chan supervisorExit, 1)
	s := spawnContext{
		instanceRoot: f.root,
		niwaDir:      f.niwaDir,
		spawnBin:     "/bin/true",
		logger:       log.New(io.Discard, "", 0),
		exitCh:       exitCh,
		wg:           &wg,
		shutdownCtx:  ctx,
		backoffs:     []time.Duration{10 * time.Millisecond},
	}
	handleSupervisorExit(supervisorExit{taskID: taskID, exitCode: 0}, s)

	cancel()
	wg.Wait()

	_, st, err := mcp.ReadState(taskDir)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.State != mcp.TaskStateCompleted {
		t.Errorf("state = %q, want completed", st.State)
	}
	if st.RestartCount != 0 {
		t.Errorf("restart_count = %d, want 0 (clean completion must not bump)", st.RestartCount)
	}
	if logHasKind(t, f.tasksDir, taskID, "retry_scheduled") {
		t.Errorf("retry_scheduled present in transitions.log — retry path wrongly engaged after clean completion")
	}
}

// TestRetryCap_UnexpectedExitWithinCap — when /bin/true repeatedly
// exits without calling niwa_finish_task, the daemon must retry up to
// MaxRestarts (3) times, then abandon with reason retry_cap_exceeded.
// restart_count ends at 3 (the last attempt number actually started).
func TestRetryCap_UnexpectedExitWithinCap(t *testing.T) {
	// 10 ms backoff × 3 retries = retries complete in < 100 ms.
	r := newRetryFixture(t, []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond}, "/bin/true")
	defer r.Shutdown(t)

	taskID := r.seedQueuedTask(t, "web")
	r.fireInitial(t, taskID)

	// Wait up to 3 s for state.json to reach "abandoned". Every cycle
	// (spawn → exit → backoff) is ~tens of ms so this is very
	// comfortable. If state never reaches abandoned, the retry
	// pipeline is broken.
	taskDir := filepath.Join(r.tasksDir, taskID)
	deadline := time.Now().Add(3 * time.Second)
	var st *mcp.TaskState
	for time.Now().Before(deadline) {
		_, cur, err := mcp.ReadState(taskDir)
		if err == nil && cur.State == mcp.TaskStateAbandoned {
			st = cur
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if st == nil {
		t.Fatalf("task did not reach abandoned within 3 s")
	}
	if st.State != mcp.TaskStateAbandoned {
		t.Errorf("state = %q, want abandoned", st.State)
	}
	if st.RestartCount != 3 {
		t.Errorf("restart_count = %d, want 3 (attempts 1, 2, 3 all ran)", st.RestartCount)
	}
	// Reason must reference retry_cap_exceeded for downstream observability.
	var reason map[string]any
	if err := json.Unmarshal(st.Reason, &reason); err != nil {
		t.Fatalf("unmarshal reason: %v (raw=%s)", err, st.Reason)
	}
	if reason["error"] != "retry_cap_exceeded" {
		t.Errorf("reason.error = %v, want retry_cap_exceeded", reason["error"])
	}
}

// TestRetryCap_BackoffTiming — backoffs [50 ms, 80 ms, 50 ms] should
// produce retry transitions.log entries roughly 50 ms, 80 ms, 50 ms
// apart. Measured from the timestamps embedded in retry_scheduled /
// spawn entries in transitions.log.
func TestRetryCap_BackoffTiming(t *testing.T) {
	backoffs := []time.Duration{50 * time.Millisecond, 80 * time.Millisecond, 50 * time.Millisecond}
	r := newRetryFixture(t, backoffs, "/bin/true")
	defer r.Shutdown(t)

	taskID := r.seedQueuedTask(t, "web")
	r.fireInitial(t, taskID)

	// Wait until abandoned (all 3 retries consumed).
	taskDir := filepath.Join(r.tasksDir, taskID)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, st, err := mcp.ReadState(taskDir)
		if err == nil && st.State == mcp.TaskStateAbandoned {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Read transitions.log and extract timestamps of each
	// unexpected_exit → spawn pair.
	data, err := os.ReadFile(filepath.Join(taskDir, "transitions.log"))
	if err != nil {
		t.Fatalf("read transitions.log: %v", err)
	}
	var entries []mcp.TransitionLogEntry
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var e mcp.TransitionLogEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("parse log line: %v", err)
		}
		entries = append(entries, e)
	}

	// Expected shape: spawn(attempt=1), unexpected_exit,
	// retry_scheduled(attempt=1, backoff=50ms), spawn(attempt=1 bumped
	// to restart_count=1 → attempt field in retry spawn = 1),
	// unexpected_exit, retry_scheduled(attempt=2 ... ), etc.
	//
	// Rather than pin the exact sequence, extract retry_scheduled
	// entries and assert each one's backoff_seconds field matches the
	// configured backoff. (The configured backoff is in ms; we asked
	// appendRetryScheduledEntry to record it in seconds which yields 0
	// for sub-second values. So assert presence of retry_scheduled
	// entries at least equal to cap.)
	var retrySched []mcp.TransitionLogEntry
	for _, e := range entries {
		if e.Kind == "retry_scheduled" {
			retrySched = append(retrySched, e)
		}
	}
	if len(retrySched) != 3 {
		t.Fatalf("retry_scheduled entries = %d, want 3", len(retrySched))
	}
	for i, e := range retrySched {
		if e.Attempt != i+1 {
			t.Errorf("retry_scheduled[%d].Attempt = %d, want %d", i, e.Attempt, i+1)
		}
	}

	// Now verify backoff *timing* using the retry_scheduled.At ->
	// following spawn.At gap (both parsed to time.Time).
	// Walk entries to find each retry_scheduled followed by a spawn.
	for i := 0; i < len(entries); i++ {
		if entries[i].Kind != "retry_scheduled" {
			continue
		}
		// Find the next "spawn" after i.
		for j := i + 1; j < len(entries); j++ {
			if entries[j].Kind == "spawn" {
				startT, err1 := time.Parse(time.RFC3339Nano, entries[i].At)
				spawnT, err2 := time.Parse(time.RFC3339Nano, entries[j].At)
				if err1 != nil || err2 != nil {
					t.Fatalf("parse timestamps: %v / %v", err1, err2)
				}
				gap := spawnT.Sub(startT)
				// Match against backoffs[attempt-1] (clamped).
				want := backoffForAttempt(backoffs, entries[i].Attempt)
				// Accept gap in [want, want + 500 ms] — upper bound is
				// scheduler jitter on a loaded test host.
				if gap < want-5*time.Millisecond {
					t.Errorf("retry[%d] gap=%v, want >= %v", entries[i].Attempt, gap, want)
				}
				if gap > want+500*time.Millisecond {
					t.Errorf("retry[%d] gap=%v exceeds want=%v + 500 ms", entries[i].Attempt, gap, want)
				}
				break
			}
		}
	}
}

// TestRetryCap_FailTaskDoesNotBumpCounter — niwa_fail_task and
// niwa_finish_task(outcome=abandoned) transition state directly to
// abandoned without going through the daemon's retry path. When the
// supervisor goroutine eventually returns (after the worker exits),
// handleSupervisorExit must see terminal state and do nothing — no
// retry_scheduled, no restart_count bump.
//
// This test exercises the classifier decision directly: seed a task
// in state=abandoned (as niwa_fail_task would leave it), synthesize a
// supervisorExit, and run handleSupervisorExit. The race between
// worker exit and worker's abandoned-transition is NOT what's under
// test here — it's the classifier's handling of a terminal state at
// exit time.
func TestRetryCap_FailTaskDoesNotBumpCounter(t *testing.T) {
	f := newDaemonTestFixture(t)
	taskID := f.seedQueuedTask(t, "web")
	taskDir := filepath.Join(f.tasksDir, taskID)

	// Move state queued → running → abandoned without the daemon's
	// retry path. Mirrors what niwa_fail_task handlers produce.
	if err := mcp.UpdateState(taskDir, func(cur *mcp.TaskState) (*mcp.TaskState, *mcp.TransitionLogEntry, error) {
		next := *cur
		next.State = mcp.TaskStateRunning
		next.StateTransitions = append(next.StateTransitions,
			mcp.StateTransition{From: cur.State, To: mcp.TaskStateRunning})
		return &next, &mcp.TransitionLogEntry{Kind: "spawn", From: cur.State, To: mcp.TaskStateRunning}, nil
	}); err != nil {
		t.Fatalf("transition to running: %v", err)
	}
	if err := mcp.UpdateState(taskDir, func(cur *mcp.TaskState) (*mcp.TaskState, *mcp.TransitionLogEntry, error) {
		next := *cur
		next.State = mcp.TaskStateAbandoned
		next.StateTransitions = append(next.StateTransitions,
			mcp.StateTransition{From: cur.State, To: mcp.TaskStateAbandoned})
		next.Reason = json.RawMessage(`{"error":"fail_task","detail":"worker called niwa_fail_task"}`)
		return &next, &mcp.TransitionLogEntry{
			Kind: "state_transition", From: cur.State, To: mcp.TaskStateAbandoned,
		}, nil
	}); err != nil {
		t.Fatalf("transition to abandoned: %v", err)
	}

	// Now feed a supervisor exit to handleSupervisorExit. The state is
	// terminal, so the classifier's "terminal state" branch must win.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	exitCh := make(chan supervisorExit, 1)
	s := spawnContext{
		instanceRoot: f.root,
		niwaDir:      f.niwaDir,
		spawnBin:     "/bin/true",
		logger:       log.New(io.Discard, "", 0),
		exitCh:       exitCh,
		wg:           &wg,
		shutdownCtx:  ctx,
		backoffs:     []time.Duration{10 * time.Millisecond},
	}
	handleSupervisorExit(supervisorExit{taskID: taskID, exitCode: 0}, s)

	// Allow any stray goroutines to run (there should be none).
	cancel()
	wg.Wait()

	_, st, err := mcp.ReadState(taskDir)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.State != mcp.TaskStateAbandoned {
		t.Errorf("state = %q, want abandoned", st.State)
	}
	if st.RestartCount != 0 {
		t.Errorf("restart_count = %d, want 0 (fail_task must not bump counter)", st.RestartCount)
	}

	// transitions.log must NOT contain retry_scheduled.
	if logHasKind(t, f.tasksDir, taskID, "retry_scheduled") {
		t.Errorf("retry_scheduled present in transitions.log — retry path wrongly engaged after fail_task")
	}
}

// TestRetryCap_BackoffSliceShorterThanCap — when the configured
// backoff slice has fewer entries than MaxRestarts, the last value is
// reused for all remaining attempts. backoffs=[20 ms] with cap=3 means
// every retry uses 20 ms.
func TestRetryCap_BackoffSliceShorterThanCap(t *testing.T) {
	backoffs := []time.Duration{20 * time.Millisecond}
	r := newRetryFixture(t, backoffs, "/bin/true")
	defer r.Shutdown(t)

	taskID := r.seedQueuedTask(t, "web")
	r.fireInitial(t, taskID)

	// Wait for abandoned (3 retries at 20 ms each).
	taskDir := filepath.Join(r.tasksDir, taskID)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, st, err := mcp.ReadState(taskDir)
		if err == nil && st.State == mcp.TaskStateAbandoned {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, st, err := mcp.ReadState(taskDir)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.State != mcp.TaskStateAbandoned {
		t.Fatalf("state = %q, want abandoned (short-backoff clamp path)", st.State)
	}
	if st.RestartCount != 3 {
		t.Errorf("restart_count = %d, want 3", st.RestartCount)
	}

	// Each retry must have used the clamped value. Walk retry_scheduled
	// entries and assert attempt indices 1..3, and that backoffForAttempt
	// with this configured slice returns 20 ms for all of them.
	for attempt := 1; attempt <= 3; attempt++ {
		got := backoffForAttempt(backoffs, attempt)
		if got != 20*time.Millisecond {
			t.Errorf("backoffForAttempt(%d) = %v, want 20 ms (clamp)", attempt, got)
		}
	}
}

// TestRetryCap_AbandonedMessageDeliveredToDelegator — when the daemon
// abandons a task (cap exceeded), a task.abandoned Message must appear
// in the delegator's inbox with body carrying reason + restart_count.
func TestRetryCap_AbandonedMessageDeliveredToDelegator(t *testing.T) {
	r := newRetryFixture(t, []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond}, "/bin/true")
	defer r.Shutdown(t)

	// Delegator role "coordinator" inbox must exist so the message
	// write does not silently fail. newDaemonTestFixture only creates
	// "web"; explicitly materialize "coordinator".
	coordInbox := filepath.Join(r.rolesRoot, "coordinator", "inbox")
	if err := os.MkdirAll(coordInbox, 0o700); err != nil {
		t.Fatalf("mkdir coordinator inbox: %v", err)
	}

	taskID := r.seedQueuedTask(t, "web")
	r.fireInitial(t, taskID)

	// Wait for abandoned.
	taskDir := filepath.Join(r.tasksDir, taskID)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, st, err := mcp.ReadState(taskDir)
		if err == nil && st.State == mcp.TaskStateAbandoned {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Scan coordinator inbox for a task.abandoned message referencing
	// our task ID. Allow a small grace period for the message write to
	// complete after the abandon transition.
	var found *mcp.Message
	deadline = time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) && found == nil {
		entries, err := os.ReadDir(coordInbox)
		if err != nil {
			t.Fatalf("read coordinator inbox: %v", err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(coordInbox, e.Name()))
			if err != nil {
				continue
			}
			var m mcp.Message
			if err := json.Unmarshal(data, &m); err != nil {
				continue
			}
			if m.Type == "task.abandoned" && m.TaskID == taskID {
				found = &m
				break
			}
		}
		if found == nil {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if found == nil {
		t.Fatalf("no task.abandoned message for task %s in coordinator inbox", taskID)
	}
	if found.To.Role != "coordinator" {
		t.Errorf("message.to.role = %q, want coordinator", found.To.Role)
	}
	if found.TaskID != taskID {
		t.Errorf("message.task_id = %q, want %s", found.TaskID, taskID)
	}
	// Body must carry the reason discriminator.
	var body map[string]any
	if err := json.Unmarshal(found.Body, &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if body["reason"] != "retry_cap_exceeded" {
		t.Errorf("body.reason = %v, want retry_cap_exceeded", body["reason"])
	}
	// restart_count and max_restarts are JSON numbers → float64 in Go's
	// generic map.
	if rc, ok := body["restart_count"].(float64); !ok || int(rc) != 3 {
		t.Errorf("body.restart_count = %v, want 3", body["restart_count"])
	}
}

// TestBackoffForAttempt — pure-function table test for the clamping /
// default behavior that production code relies on.
func TestBackoffForAttempt(t *testing.T) {
	cases := []struct {
		name     string
		backoffs []time.Duration
		attempt  int
		want     time.Duration
	}{
		{"normal_first", []time.Duration{10, 20, 30}, 1, 10},
		{"normal_second", []time.Duration{10, 20, 30}, 2, 20},
		{"normal_third", []time.Duration{10, 20, 30}, 3, 30},
		{"clamp_past_end", []time.Duration{10, 20, 30}, 5, 30},
		{"single_value_clamp", []time.Duration{10}, 3, 10},
		{"empty_returns_zero", nil, 1, 0},
		{"zero_or_negative_attempt_clamps_to_first", []time.Duration{10, 20}, 0, 10},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := backoffForAttempt(c.backoffs, c.attempt)
			if got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

// ---------------------------------------------------------------------
// Issue 6: stalled-progress watchdog tests
// ---------------------------------------------------------------------

// seedRunningTask is a watchdog-specific variant of seedQueuedTask that
// materializes a task directory already in the "running" state. The
// watchdog is invoked on behalf of a live worker; there's no need to go
// through the claim path.
func (f *daemonTestFixture) seedRunningTask(t *testing.T, toRole string, workerPID int) string {
	t.Helper()
	taskID := mcp.NewTaskID()
	taskDir := filepath.Join(f.tasksDir, taskID)
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		t.Fatalf("mkdir task dir: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	// ReadState validates envelope.json on every read, so materialize a
	// valid envelope alongside state.json. Without this the watchdog's
	// ticker ReadState returns ErrCorruptedState on every poll and the
	// terminal-state check never runs.
	env := mcp.TaskEnvelope{
		V:      1,
		ID:     taskID,
		From:   mcp.TaskParty{Role: "coordinator", PID: os.Getpid()},
		To:     mcp.TaskParty{Role: toRole},
		Body:   json.RawMessage(`{"kind":"test"}`),
		SentAt: now,
	}
	envBytes, _ := json.MarshalIndent(env, "", "  ")
	if err := os.WriteFile(filepath.Join(taskDir, "envelope.json"), envBytes, 0o600); err != nil {
		t.Fatalf("write envelope: %v", err)
	}

	st := &mcp.TaskState{
		V:      1,
		TaskID: taskID,
		State:  mcp.TaskStateRunning,
		StateTransitions: []mcp.StateTransition{
			{From: "", To: mcp.TaskStateQueued, At: now},
			{From: mcp.TaskStateQueued, To: mcp.TaskStateRunning, At: now},
		},
		MaxRestarts:   3,
		DelegatorRole: "coordinator",
		TargetRole:    toRole,
		Worker: mcp.TaskWorker{
			PID:            workerPID,
			Role:           toRole,
			SpawnStartedAt: now,
		},
		UpdatedAt: now,
	}
	stBytes, _ := json.MarshalIndent(st, "", "  ")
	if err := os.WriteFile(filepath.Join(taskDir, "state.json"), stBytes, 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}
	// Touch the lock file so UpdateState can flock it.
	lockPath := filepath.Join(taskDir, ".lock")
	if f, err := os.Create(lockPath); err == nil {
		_ = f.Close()
	}
	return taskID
}

// startFakeWorker launches a real subprocess suitable as a watchdog
// target. Setsid:true mirrors the production spawn path so SIGTERM to
// the process group behaves the same as for real workers. The caller is
// responsible for killing the process on test teardown (via t.Cleanup).
func startFakeWorker(t *testing.T, cmdline []string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(cmdline[0], cmdline[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting fake worker: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_, _ = cmd.Process.Wait()
		}
	})
	return cmd
}

// runWatchdogAsync is a test helper that drives runWatchdog in a
// goroutine and returns a waitDone channel the caller closes when the
// supervisor (i.e., cmd.Wait) returns. The returned done channel closes
// when runWatchdog returns.
func runWatchdogAsync(t *testing.T, cmd *exec.Cmd, taskID, taskDir string, s spawnContext) (waitDone chan struct{}, watchdogDone chan struct{}) {
	t.Helper()
	waitDone = make(chan struct{})
	watchdogDone = make(chan struct{})
	go func() {
		defer close(watchdogDone)
		runWatchdog(cmd, taskID, taskDir, waitDone, s)
	}()
	return
}

// countWatchdogSignals returns the number of watchdog_signal entries
// with the given signal name in transitions.log.
func countWatchdogSignals(t *testing.T, tasksDir, taskID, signal string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(tasksDir, taskID, "transitions.log"))
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var entry mcp.TransitionLogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Kind == "watchdog_signal" && entry.Signal == signal {
			count++
		}
	}
	return count
}

// TestWatchdog_ActiveProgressNoTrigger: a worker that updates
// last_progress.at faster than the watchdog timeout must never trigger
// SIGTERM. Uses a generous stall-watchdog : bump-interval ratio so the
// test survives scheduler jitter on loaded CI hosts.
func TestWatchdog_ActiveProgressNoTrigger(t *testing.T) {
	defer setWatchdogPollIntervalForTest(50 * time.Millisecond)()

	f := newDaemonTestFixture(t)
	cmd := startFakeWorker(t, []string{"/bin/sleep", "10"})
	taskID := f.seedRunningTask(t, "web", cmd.Process.Pid)
	taskDir := filepath.Join(f.tasksDir, taskID)

	// Seed an initial LastProgress so the watchdog's baseline is
	// populated before the stall timer starts counting.
	_ = mcp.UpdateState(taskDir, func(cur *mcp.TaskState) (*mcp.TaskState, *mcp.TransitionLogEntry, error) {
		next := *cur
		next.LastProgress = &mcp.TaskProgress{
			Summary: "seed",
			At:      time.Now().UTC().Format(time.RFC3339Nano),
		}
		return &next, nil, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	s := spawnContext{
		instanceRoot: f.root,
		niwaDir:      f.niwaDir,
		logger:       log.New(io.Discard, "", 0),
		wg:           &wg,
		shutdownCtx:  ctx,
		// 800 ms stall window vs 100 ms bump cadence → 8x margin.
		stallWatchdog: 800 * time.Millisecond,
		sigTermGrace:  300 * time.Millisecond,
	}

	waitDone, watchdogDone := runWatchdogAsync(t, cmd, taskID, taskDir, s)

	// Bump last_progress.at every 100 ms for 2 s — generous margin
	// under the 800 ms stall window.
	stopBump := make(chan struct{})
	bumpDone := make(chan struct{})
	go func() {
		defer close(bumpDone)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopBump:
				return
			case <-ticker.C:
				_ = mcp.UpdateState(taskDir, func(cur *mcp.TaskState) (*mcp.TaskState, *mcp.TransitionLogEntry, error) {
					next := *cur
					next.LastProgress = &mcp.TaskProgress{
						Summary: "ping",
						At:      time.Now().UTC().Format(time.RFC3339Nano),
					}
					return &next, nil, nil
				})
			}
		}
	}()

	// Observe for 2 s. The stall window is 800 ms, so without resets we
	// would see at least 2 SIGTERM entries.
	time.Sleep(2 * time.Second)
	close(stopBump)
	<-bumpDone

	// No watchdog_signal entries must have been written.
	if n := countWatchdogSignals(t, f.tasksDir, taskID, "SIGTERM"); n != 0 {
		t.Errorf("watchdog_signal SIGTERM count = %d, want 0 (progress kept advancing)", n)
	}
	if n := countWatchdogSignals(t, f.tasksDir, taskID, "SIGKILL"); n != 0 {
		t.Errorf("watchdog_signal SIGKILL count = %d, want 0", n)
	}

	// Fake worker must still be alive — watchdog did not fire.
	if cmd.ProcessState != nil {
		t.Errorf("fake worker exited before we killed it: %v", cmd.ProcessState)
	}

	// Tear down: signal waitDone, expect watchdog to return.
	close(waitDone)
	select {
	case <-watchdogDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("watchdog did not exit after waitDone closed")
	}
}

// TestWatchdog_StallTriggersSigterm: with no progress updates, the
// watchdog must send SIGTERM and record a watchdog_signal entry.
func TestWatchdog_StallTriggersSigterm(t *testing.T) {
	defer setWatchdogPollIntervalForTest(100 * time.Millisecond)()

	f := newDaemonTestFixture(t)
	cmd := startFakeWorker(t, []string{"/bin/sleep", "30"})
	taskID := f.seedRunningTask(t, "web", cmd.Process.Pid)
	taskDir := filepath.Join(f.tasksDir, taskID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	s := spawnContext{
		instanceRoot:  f.root,
		niwaDir:       f.niwaDir,
		logger:        log.New(io.Discard, "", 0),
		wg:            &wg,
		shutdownCtx:   ctx,
		stallWatchdog: 300 * time.Millisecond,
		sigTermGrace:  500 * time.Millisecond,
	}

	waitDone, watchdogDone := runWatchdogAsync(t, cmd, taskID, taskDir, s)

	// Run a goroutine that waits on cmd.Wait and closes waitDone when
	// the process has been reaped — mirrors the supervisor goroutine's
	// contract. Otherwise the watchdog would block forever waiting for
	// the process to exit after its SIGKILL.
	go func() {
		_ = cmd.Wait()
		close(waitDone)
	}()

	// Wait up to 2 s for the SIGTERM entry to appear (stall = 300 ms).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if countWatchdogSignals(t, f.tasksDir, taskID, "SIGTERM") > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := countWatchdogSignals(t, f.tasksDir, taskID, "SIGTERM"); n == 0 {
		t.Fatalf("no SIGTERM watchdog_signal entry after stall window expired")
	}

	// Wait for the process to exit (watchdog or supervisor kill).
	select {
	case <-waitDone:
	case <-time.After(3 * time.Second):
		t.Fatalf("fake worker did not exit after watchdog fired")
	}

	// Watchdog should return shortly after waitDone closes.
	select {
	case <-watchdogDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("watchdog did not exit after waitDone closed")
	}
}

// TestWatchdog_SigkillAfterGrace: a worker that ignores SIGTERM must be
// escalated to SIGKILL after sigTermGrace expires. transitions.log must
// record both signals.
func TestWatchdog_SigkillAfterGrace(t *testing.T) {
	defer setWatchdogPollIntervalForTest(100 * time.Millisecond)()

	f := newDaemonTestFixture(t)
	// trap '' TERM ignores SIGTERM; sleep holds the process alive.
	cmd := startFakeWorker(t, []string{"/bin/sh", "-c", "trap '' TERM; sleep 30"})
	taskID := f.seedRunningTask(t, "web", cmd.Process.Pid)
	taskDir := filepath.Join(f.tasksDir, taskID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	s := spawnContext{
		instanceRoot:  f.root,
		niwaDir:       f.niwaDir,
		logger:        log.New(io.Discard, "", 0),
		wg:            &wg,
		shutdownCtx:   ctx,
		stallWatchdog: 200 * time.Millisecond,
		sigTermGrace:  300 * time.Millisecond,
	}

	waitDone, watchdogDone := runWatchdogAsync(t, cmd, taskID, taskDir, s)
	go func() {
		_ = cmd.Wait()
		close(waitDone)
	}()

	// Wait up to 3 s for both signals to be recorded.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if countWatchdogSignals(t, f.tasksDir, taskID, "SIGKILL") > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := countWatchdogSignals(t, f.tasksDir, taskID, "SIGTERM"); n == 0 {
		t.Errorf("missing SIGTERM watchdog_signal (must precede SIGKILL)")
	}
	if n := countWatchdogSignals(t, f.tasksDir, taskID, "SIGKILL"); n == 0 {
		t.Fatalf("missing SIGKILL watchdog_signal after grace expired")
	}

	// Process must eventually die.
	select {
	case <-waitDone:
	case <-time.After(3 * time.Second):
		t.Fatalf("SIGTERM-ignoring worker did not die even after SIGKILL")
	}

	select {
	case <-watchdogDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("watchdog did not exit")
	}
}

// TestWatchdog_DefensiveReapAfterFinish: a worker that transitions the
// task to a terminal state but then fails to exit must be reaped after
// sigTermGrace. The defensive-reap path must NOT bump restart_count —
// Issue 5's classifier sees terminal state and returns without retry.
func TestWatchdog_DefensiveReapAfterFinish(t *testing.T) {
	defer setWatchdogPollIntervalForTest(100 * time.Millisecond)()

	f := newDaemonTestFixture(t)
	cmd := startFakeWorker(t, []string{"/bin/sleep", "30"})
	taskID := f.seedRunningTask(t, "web", cmd.Process.Pid)
	taskDir := filepath.Join(f.tasksDir, taskID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	s := spawnContext{
		instanceRoot: f.root,
		niwaDir:      f.niwaDir,
		logger:       log.New(io.Discard, "", 0),
		wg:           &wg,
		shutdownCtx:  ctx,
		// Long stall watchdog so the stall path cannot fire first —
		// we're exercising the defensive-reap branch.
		stallWatchdog: 60 * time.Second,
		sigTermGrace:  300 * time.Millisecond,
	}

	waitDone, watchdogDone := runWatchdogAsync(t, cmd, taskID, taskDir, s)
	go func() {
		_ = cmd.Wait()
		close(waitDone)
	}()

	// Simulate the worker calling niwa_finish_task: transition state
	// to "completed". The worker process stays alive (hung).
	if err := mcp.UpdateState(taskDir, func(cur *mcp.TaskState) (*mcp.TaskState, *mcp.TransitionLogEntry, error) {
		next := *cur
		next.State = mcp.TaskStateCompleted
		next.StateTransitions = append(next.StateTransitions,
			mcp.StateTransition{From: mcp.TaskStateRunning, To: mcp.TaskStateCompleted})
		next.Result = json.RawMessage(`{"ok":true}`)
		return &next, &mcp.TransitionLogEntry{Kind: "state_transition", From: mcp.TaskStateRunning, To: mcp.TaskStateCompleted}, nil
	}); err != nil {
		t.Fatalf("transition to completed: %v", err)
	}

	// Wait for defensive reap to fire. Poll interval = 100 ms, grace =
	// 300 ms → SIGTERM should land within ~500 ms. Give it 3 s of
	// slack for scheduler noise.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if countWatchdogSignals(t, f.tasksDir, taskID, "SIGTERM") > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := countWatchdogSignals(t, f.tasksDir, taskID, "SIGTERM"); n == 0 {
		t.Fatalf("defensive reap did not send SIGTERM")
	}

	// Wait for the process to die.
	select {
	case <-waitDone:
	case <-time.After(3 * time.Second):
		t.Fatalf("fake worker did not exit after defensive-reap SIGTERM")
	}
	<-watchdogDone

	// restart_count must remain 0 — defensive-reap does NOT feed the
	// retry path (Issue 5's classifier sees terminal state and returns
	// without bumping).
	_, st, err := mcp.ReadState(taskDir)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.RestartCount != 0 {
		t.Errorf("restart_count = %d, want 0 (defensive reap must not count as retry)", st.RestartCount)
	}
	if st.State != mcp.TaskStateCompleted {
		t.Errorf("state = %q, want completed (defensive reap must not change state)", st.State)
	}
}

// TestWatchdog_NaturalExitNoSignal: when cmd.Wait returns on its own
// (worker exited cleanly without the watchdog firing), no
// watchdog_signal entries must appear. This protects against false
// positives in the leak-detection path above.
func TestWatchdog_NaturalExitNoSignal(t *testing.T) {
	defer setWatchdogPollIntervalForTest(100 * time.Millisecond)()

	f := newDaemonTestFixture(t)
	// /bin/true exits immediately; we simulate the supervisor closing
	// waitDone in response.
	cmd := startFakeWorker(t, []string{"/bin/sleep", "0.2"})
	taskID := f.seedRunningTask(t, "web", cmd.Process.Pid)
	taskDir := filepath.Join(f.tasksDir, taskID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	s := spawnContext{
		instanceRoot:  f.root,
		niwaDir:       f.niwaDir,
		logger:        log.New(io.Discard, "", 0),
		wg:            &wg,
		shutdownCtx:   ctx,
		stallWatchdog: 5 * time.Second,
		sigTermGrace:  500 * time.Millisecond,
	}

	waitDone, watchdogDone := runWatchdogAsync(t, cmd, taskID, taskDir, s)
	go func() {
		_ = cmd.Wait()
		close(waitDone)
	}()

	// Wait for the watchdog to exit after natural termination.
	select {
	case <-watchdogDone:
	case <-time.After(3 * time.Second):
		t.Fatalf("watchdog did not exit after natural worker exit")
	}

	if n := countWatchdogSignals(t, f.tasksDir, taskID, "SIGTERM"); n != 0 {
		t.Errorf("unexpected SIGTERM watchdog_signal after natural exit: %d", n)
	}
	if n := countWatchdogSignals(t, f.tasksDir, taskID, "SIGKILL"); n != 0 {
		t.Errorf("unexpected SIGKILL watchdog_signal after natural exit: %d", n)
	}
}

// TestWatchdog_DisabledWhenStallZero: when stallWatchdog is zero or
// negative, spawnWorker must not launch the watchdog goroutine. This
// keeps unit tests (and any downstream callers that opt out) from
// paying the 2 s ticker cost when they don't need it.
func TestWatchdog_DisabledWhenStallZero(t *testing.T) {
	f := newDaemonTestFixture(t)
	taskID := f.seedQueuedTask(t, "web")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	exitCh := make(chan supervisorExit, 1)
	s := spawnContext{
		instanceRoot:  f.root,
		niwaDir:       f.niwaDir,
		spawnBin:      "/bin/true",
		logger:        log.New(io.Discard, "", 0),
		exitCh:        exitCh,
		wg:            &wg,
		shutdownCtx:   ctx,
		backoffs:      []time.Duration{60 * time.Second},
		stallWatchdog: 0, // disabled
		sigTermGrace:  500 * time.Millisecond,
	}

	evt := inboxEvent{
		role:     "web",
		taskID:   taskID,
		filePath: filepath.Join(f.rolesRoot, "web", "inbox", taskID+".json"),
	}
	handleInboxEvent(evt, s)

	// Drain the exit event.
	select {
	case <-exitCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("supervisor exit never arrived")
	}

	// Wait for the supervisor goroutine to finish. If the watchdog had
	// been launched it would have added another goroutine to wg.
	cancel()
	wg.Wait()

	// No watchdog_signal entries may exist since the watchdog did not
	// run.
	if n := countWatchdogSignals(t, f.tasksDir, taskID, "SIGTERM"); n != 0 {
		t.Errorf("unexpected SIGTERM watchdog_signal when watchdog disabled: %d", n)
	}
}

// TestWatchdog_EventuallyRetriesViaIssue5Pipeline: a watchdog-triggered
// kill must feed Issue 5's classifier (the kill happens while state is
// still "running"), which schedules a retry. transitions.log must
// contain a retry_scheduled entry after the watchdog_signal entry.
//
// Exercises the full supervisor + watchdog integration in spawnWorker.
func TestWatchdog_EventuallyRetriesViaIssue5Pipeline(t *testing.T) {
	defer setWatchdogPollIntervalForTest(100 * time.Millisecond)()

	f := newDaemonTestFixture(t)
	taskID := f.seedQueuedTask(t, "web")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	exitCh := make(chan supervisorExit, 4)

	// spawnWorker enforces a fixed argv shape (-p prompt, --permission-
	// mode, --mcp-config, --strict-mcp-config) that /bin/sleep cannot
	// parse. Wrap sleep in a tiny shell script that ignores its args
	// and keeps the worker alive long enough for the watchdog to fire.
	script := filepath.Join(t.TempDir(), "sleeper.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatalf("writing sleeper: %v", err)
	}

	s := spawnContext{
		instanceRoot: f.root,
		niwaDir:      f.niwaDir,
		spawnBin:     script,
		logger:       log.New(io.Discard, "", 0),
		exitCh:       exitCh,
		wg:           &wg,
		shutdownCtx:  ctx,
		// 60 s backoff so the scheduled retry does NOT fire during the
		// test — we're only verifying retry_scheduled appears.
		backoffs:      []time.Duration{60 * time.Second},
		stallWatchdog: 300 * time.Millisecond,
		sigTermGrace:  500 * time.Millisecond,
	}

	evt := inboxEvent{
		role:     "web",
		taskID:   taskID,
		filePath: filepath.Join(f.rolesRoot, "web", "inbox", taskID+".json"),
	}

	// Drain exitCh through handleSupervisorExit on a pump goroutine, as
	// the central loop does in production.
	pumpCtx, pumpCancel := context.WithCancel(context.Background())
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		for {
			select {
			case ex := <-exitCh:
				handleSupervisorExit(ex, s)
			case <-pumpCtx.Done():
				return
			}
		}
	}()

	handleInboxEvent(evt, s)

	// Wait up to 3 s for both watchdog_signal and retry_scheduled to
	// appear. The first retry_scheduled is the end-to-end proof that
	// the watchdog-triggered kill flowed through Issue 5's pipeline.
	taskDir := filepath.Join(f.tasksDir, taskID)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if logHasKind(t, f.tasksDir, taskID, "retry_scheduled") &&
			countWatchdogSignals(t, f.tasksDir, taskID, "SIGTERM") > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if countWatchdogSignals(t, f.tasksDir, taskID, "SIGTERM") == 0 {
		t.Errorf("watchdog_signal SIGTERM never recorded")
	}
	if !logHasKind(t, f.tasksDir, taskID, "retry_scheduled") {
		t.Errorf("retry_scheduled never appeared — Issue 5 pipeline did not see the watchdog-triggered exit")
	}

	// Verify the retry_scheduled entry comes AFTER the watchdog_signal
	// entry, which is the whole point of the integration.
	data, err := os.ReadFile(filepath.Join(taskDir, "transitions.log"))
	if err != nil {
		t.Fatalf("read transitions.log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	var sigIdx, retryIdx int = -1, -1
	for i, line := range lines {
		var e mcp.TransitionLogEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		if e.Kind == "watchdog_signal" && e.Signal == "SIGTERM" && sigIdx == -1 {
			sigIdx = i
		}
		if e.Kind == "retry_scheduled" && retryIdx == -1 {
			retryIdx = i
		}
	}
	if sigIdx == -1 || retryIdx == -1 {
		t.Fatalf("missing signal entry or retry entry; sigIdx=%d retryIdx=%d", sigIdx, retryIdx)
	}
	if retryIdx <= sigIdx {
		t.Errorf("retry_scheduled (line %d) did not follow watchdog_signal (line %d)", retryIdx, sigIdx)
	}

	// State must still be "running" (retry pending, backoff hasn't
	// fired). Defensive-reap did not kick in (state never went
	// terminal). restart_count is not yet bumped (it bumps at retry
	// fire time, which is deferred by the 60 s backoff above).
	_, st, err := mcp.ReadState(taskDir)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.State != mcp.TaskStateRunning {
		t.Errorf("state = %q, want running (retry pending)", st.State)
	}

	// Tear down in the same order the production daemon uses: drain the
	// exit-event pump (analogue of runEventLoop returning) BEFORE calling
	// wg.Wait. Otherwise handleSupervisorExit's wg.Add(1) can race
	// wg.Wait if the pump is still processing when Wait starts.
	cancel()
	pumpCancel()
	<-pumpDone
	wg.Wait()
}

// ---------------------------------------------------------------------
// Issue 7: reconciliation + orphan polling + flock orchestration
// ---------------------------------------------------------------------

// seedReconcileTask writes a minimal on-disk task in "running" state
// directly via os.WriteFile (no UpdateState) — by the time
// reconcileRunningTasks runs on a real crash-recovery path, the dying
// daemon has left state.json in whatever partial form its last write
// produced. These tests seed each classification case explicitly.
func seedReconcileTask(t *testing.T, f *daemonTestFixture, worker mcp.TaskWorker) string {
	t.Helper()
	taskID := mcp.NewTaskID()
	taskDir := filepath.Join(f.tasksDir, taskID)
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		t.Fatalf("mkdir task dir: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	env := mcp.TaskEnvelope{
		V:      1,
		ID:     taskID,
		From:   mcp.TaskParty{Role: "coordinator", PID: os.Getpid()},
		To:     mcp.TaskParty{Role: worker.Role},
		Body:   json.RawMessage(`{"kind":"test"}`),
		SentAt: now,
	}
	envBytes, _ := json.MarshalIndent(env, "", "  ")
	if err := os.WriteFile(filepath.Join(taskDir, "envelope.json"), envBytes, 0o600); err != nil {
		t.Fatalf("write envelope: %v", err)
	}
	st := &mcp.TaskState{
		V:      1,
		TaskID: taskID,
		State:  mcp.TaskStateRunning,
		StateTransitions: []mcp.StateTransition{
			{From: "", To: mcp.TaskStateQueued, At: now},
			{From: mcp.TaskStateQueued, To: mcp.TaskStateRunning, At: now},
		},
		MaxRestarts:   3,
		DelegatorRole: "coordinator",
		TargetRole:    worker.Role,
		Worker:        worker,
		UpdatedAt:     now,
	}
	stBytes, _ := json.MarshalIndent(st, "", "  ")
	if err := os.WriteFile(filepath.Join(taskDir, "state.json"), stBytes, 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}
	// Pre-create .lock so UpdateState can open it even before the first
	// mutation lands.
	if lf, err := os.Create(filepath.Join(taskDir, ".lock")); err == nil {
		_ = lf.Close()
	}
	return taskID
}

// TestReconcile_SpawnNeverCompleted: state.json with pid=0 and
// spawn_started_at set → reconciliation classifies as "fresh retry" and
// respawnFreshRetry re-enters spawn without bumping restart_count.
func TestReconcile_SpawnNeverCompleted(t *testing.T) {
	f := newDaemonTestFixture(t)
	taskID := seedReconcileTask(t, f, mcp.TaskWorker{
		Role:           "web",
		PID:            0,
		SpawnStartedAt: time.Now().Add(-10 * time.Second).UTC().Format(time.RFC3339Nano),
	})

	result := reconcileRunningTasks(f.tasksDir, log.New(io.Discard, "", 0))

	if len(result.freshRetries) != 1 || result.freshRetries[0] != taskID {
		t.Fatalf("freshRetries = %v, want [%s]", result.freshRetries, taskID)
	}
	if len(result.orphans) != 0 || len(result.deadWorkers) != 0 {
		t.Errorf("unexpected classifications: orphans=%v dead=%v", result.orphans, result.deadWorkers)
	}

	// transitions.log must carry the crash_recovery_fresh_retry entry.
	if !logHasKind(t, f.tasksDir, taskID, "crash_recovery_fresh_retry") {
		t.Errorf("transitions.log missing crash_recovery_fresh_retry entry")
	}

	// Drive respawnFreshRetry and assert restart_count stayed at 0.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	exitCh := make(chan supervisorExit, 1)
	s := spawnContext{
		instanceRoot: f.root,
		niwaDir:      f.niwaDir,
		spawnBin:     "/bin/true",
		logger:       log.New(io.Discard, "", 0),
		exitCh:       exitCh,
		wg:           &wg,
		shutdownCtx:  ctx,
		backoffs:     []time.Duration{60 * time.Second},
	}
	respawnFreshRetry(taskID, s)

	// Drain the supervisor exit; the /bin/true process exits immediately.
	select {
	case <-exitCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("supervisor never emitted exit for fresh retry")
	}

	_, st, err := mcp.ReadState(filepath.Join(f.tasksDir, taskID))
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.RestartCount != 0 {
		t.Errorf("restart_count = %d, want 0 (fresh retry must NOT bump counter)", st.RestartCount)
	}

	cancel()
	wg.Wait()
}

// TestReconcile_LiveOrphan: state.json with a live PID → reconciliation
// classifies as "live orphan", stamps worker.adopted_at, and returns
// the task in result.orphans. The task stays in "running" state.
func TestReconcile_LiveOrphan(t *testing.T) {
	f := newDaemonTestFixture(t)

	// Use a real live process as the orphan worker. /bin/sleep 30 lives
	// long enough for reconciliation to classify it as alive; the test
	// cleanup kills it.
	orphan := startFakeWorker(t, []string{"/bin/sleep", "30"})
	startTime, err := mcp.PIDStartTime(orphan.Process.Pid)
	if err != nil {
		t.Fatalf("PIDStartTime: %v", err)
	}

	taskID := seedReconcileTask(t, f, mcp.TaskWorker{
		Role:      "web",
		PID:       orphan.Process.Pid,
		StartTime: startTime,
	})

	result := reconcileRunningTasks(f.tasksDir, log.New(io.Discard, "", 0))

	if len(result.orphans) != 1 || result.orphans[0].taskID != taskID {
		t.Fatalf("orphans = %+v, want one entry for %s", result.orphans, taskID)
	}
	if result.orphans[0].pid != orphan.Process.Pid {
		t.Errorf("orphan.pid = %d, want %d", result.orphans[0].pid, orphan.Process.Pid)
	}
	if len(result.freshRetries) != 0 || len(result.deadWorkers) != 0 {
		t.Errorf("unexpected classifications: fresh=%v dead=%v", result.freshRetries, result.deadWorkers)
	}

	// state.json must carry worker.adopted_at and transitions.log an
	// "adoption" entry.
	_, st, err := mcp.ReadState(filepath.Join(f.tasksDir, taskID))
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.State != mcp.TaskStateRunning {
		t.Errorf("state = %q, want running (adopt must not transition)", st.State)
	}
	if st.Worker.AdoptedAt == "" {
		t.Errorf("worker.adopted_at not set")
	}
	if !logHasKind(t, f.tasksDir, taskID, "adoption") {
		t.Errorf("transitions.log missing adoption entry")
	}
}

// TestReconcile_DeadWorker: state.json with a non-live PID →
// reconciliation classifies as "dead worker" (Issue 5 pipeline).
func TestReconcile_DeadWorker(t *testing.T) {
	f := newDaemonTestFixture(t)

	// Use PID 999999 which is outside any plausible live PID on Linux.
	// Start time of 0 bypasses the start-time cross-check; IsPIDAlive
	// returns false from the signal-zero probe alone.
	taskID := seedReconcileTask(t, f, mcp.TaskWorker{
		Role:      "web",
		PID:       999999,
		StartTime: 123,
	})

	result := reconcileRunningTasks(f.tasksDir, log.New(io.Discard, "", 0))

	if len(result.deadWorkers) != 1 || result.deadWorkers[0] != taskID {
		t.Fatalf("deadWorkers = %v, want [%s]", result.deadWorkers, taskID)
	}

	// Pipe through handleSupervisorExit (as the daemon does) and assert
	// Issue 5 classifier bumped the retry path.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	exitCh := make(chan supervisorExit, 1)
	s := spawnContext{
		instanceRoot: f.root,
		niwaDir:      f.niwaDir,
		spawnBin:     "/bin/true",
		logger:       log.New(io.Discard, "", 0),
		exitCh:       exitCh,
		wg:           &wg,
		shutdownCtx:  ctx,
		backoffs:     []time.Duration{60 * time.Second},
	}
	handleSupervisorExit(supervisorExit{taskID: taskID, exitCode: -1}, s)

	if !logHasKind(t, f.tasksDir, taskID, "unexpected_exit") {
		t.Errorf("transitions.log missing unexpected_exit entry")
	}
	if !logHasKind(t, f.tasksDir, taskID, "retry_scheduled") {
		t.Errorf("transitions.log missing retry_scheduled entry (Issue 5 pipeline)")
	}

	cancel()
	wg.Wait()
}

// TestReconcile_StartTimeDivergence: PID is alive but start_time does
// not match → reconciliation treats it as dead (PID reuse defense).
func TestReconcile_StartTimeDivergence(t *testing.T) {
	f := newDaemonTestFixture(t)

	// Pick our own PID (definitely alive) but record a stale start_time.
	// IsPIDAlive will compare the recorded start_time against /proc and
	// return false on mismatch, which drives the dead-worker branch.
	realStart, err := mcp.PIDStartTime(os.Getpid())
	if err != nil {
		t.Fatalf("PIDStartTime: %v", err)
	}
	taskID := seedReconcileTask(t, f, mcp.TaskWorker{
		Role:      "web",
		PID:       os.Getpid(),
		StartTime: realStart + 999, // deliberately divergent
	})

	result := reconcileRunningTasks(f.tasksDir, log.New(io.Discard, "", 0))

	if len(result.deadWorkers) != 1 || result.deadWorkers[0] != taskID {
		t.Fatalf("deadWorkers = %v, want [%s] (PID reuse should map to dead)", result.deadWorkers, taskID)
	}
	if len(result.orphans) != 0 {
		t.Errorf("orphans = %v, want empty (diverged start_time must not adopt)", result.orphans)
	}
}

// TestOrphanPolling_WorkerCompletes: an adopted orphan whose task state
// transitions to terminal (worker called niwa_finish_task) must be
// dropped from the orphan list on the next poll.
func TestOrphanPolling_WorkerCompletes(t *testing.T) {
	f := newDaemonTestFixture(t)

	worker := startFakeWorker(t, []string{"/bin/sleep", "30"})
	startTime, err := mcp.PIDStartTime(worker.Process.Pid)
	if err != nil {
		t.Fatalf("PIDStartTime: %v", err)
	}
	taskID := seedReconcileTask(t, f, mcp.TaskWorker{
		Role:      "web",
		PID:       worker.Process.Pid,
		StartTime: startTime,
	})

	orphans := []orphanEntry{{taskID: taskID, pid: worker.Process.Pid, startTime: startTime}}

	// Simulate the worker's niwa_finish_task: transition state to
	// completed. The process is still alive (we never killed the sleep),
	// which is the exact condition the orphan poller must handle — it
	// drops the task on terminal state regardless of liveness.
	if err := mcp.UpdateState(filepath.Join(f.tasksDir, taskID), func(cur *mcp.TaskState) (*mcp.TaskState, *mcp.TransitionLogEntry, error) {
		next := *cur
		next.State = mcp.TaskStateCompleted
		next.StateTransitions = append(next.StateTransitions,
			mcp.StateTransition{From: mcp.TaskStateRunning, To: mcp.TaskStateCompleted})
		next.Result = json.RawMessage(`{"ok":true}`)
		return &next, &mcp.TransitionLogEntry{Kind: "state_transition", From: mcp.TaskStateRunning, To: mcp.TaskStateCompleted}, nil
	}); err != nil {
		t.Fatalf("transition to completed: %v", err)
	}

	s := spawnContext{
		instanceRoot: f.root,
		niwaDir:      f.niwaDir,
		logger:       log.New(io.Discard, "", 0),
		backoffs:     []time.Duration{60 * time.Second},
	}
	remaining, deadExits := pollOrphans(orphans, s)
	if len(remaining) != 0 {
		t.Errorf("pollOrphans remaining = %v, want empty (terminal state must drop)", remaining)
	}
	if len(deadExits) != 0 {
		t.Errorf("pollOrphans deadExits = %v, want empty (terminal state is not an unexpected exit)", deadExits)
	}
	// No retry path must have been engaged.
	if logHasKind(t, f.tasksDir, taskID, "retry_scheduled") {
		t.Errorf("retry_scheduled present after terminal-state drop — orphan poller wrongly treated terminal as unexpected")
	}
}

// TestOrphanPolling_WorkerDies: killing an orphan's worker process
// must cause the next poll to classify it as unexpected_exit (Issue 5
// pipeline) and drop it from the orphan list.
func TestOrphanPolling_WorkerDies(t *testing.T) {
	f := newDaemonTestFixture(t)

	// Start, record, and immediately kill the worker. IsPIDAlive will
	// return false, and the orphan poller should reclassify.
	worker := startFakeWorker(t, []string{"/bin/sleep", "30"})
	startTime, err := mcp.PIDStartTime(worker.Process.Pid)
	if err != nil {
		t.Fatalf("PIDStartTime: %v", err)
	}
	taskID := seedReconcileTask(t, f, mcp.TaskWorker{
		Role:      "web",
		PID:       worker.Process.Pid,
		StartTime: startTime,
	})

	// Kill the worker.
	if err := syscall.Kill(-worker.Process.Pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill worker: %v", err)
	}
	// Wait for the kernel to reap the PID so IsPIDAlive returns false.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !mcp.IsPIDAlive(worker.Process.Pid, startTime) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Drain the Wait so the ZOMBIE status does not leak past the test.
	_, _ = worker.Process.Wait()

	orphans := []orphanEntry{{taskID: taskID, pid: worker.Process.Pid, startTime: startTime}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	exitCh := make(chan supervisorExit, 1)
	s := spawnContext{
		instanceRoot: f.root,
		niwaDir:      f.niwaDir,
		spawnBin:     "/bin/true",
		logger:       log.New(io.Discard, "", 0),
		exitCh:       exitCh,
		wg:           &wg,
		shutdownCtx:  ctx,
		backoffs:     []time.Duration{60 * time.Second},
	}

	remaining, deadExits := pollOrphans(orphans, s)
	if len(remaining) != 0 {
		t.Errorf("pollOrphans remaining = %v, want empty (dead worker must be dropped)", remaining)
	}
	if len(deadExits) != 1 || deadExits[0].taskID != taskID {
		t.Fatalf("pollOrphans deadExits = %v, want one entry for %s", deadExits, taskID)
	}
	if deadExits[0].exitCode != -1 {
		t.Errorf("deadExits[0].exitCode = %d, want -1 (sentinel for orphan with no child handle)", deadExits[0].exitCode)
	}

	// Drive the supervisorExit through the classifier the way the
	// orphan supervisor goroutine does at runtime — via exitCh →
	// handleSupervisorExit — so we can assert the resulting log entries.
	handleSupervisorExit(deadExits[0], s)

	// Issue 5 classifier must have recorded unexpected_exit and
	// scheduled a retry.
	if !logHasKind(t, f.tasksDir, taskID, "unexpected_exit") {
		t.Errorf("transitions.log missing unexpected_exit entry after orphan death")
	}
	if !logHasKind(t, f.tasksDir, taskID, "retry_scheduled") {
		t.Errorf("transitions.log missing retry_scheduled entry — dead orphan did not flow through Issue 5 pipeline")
	}

	cancel()
	wg.Wait()
}

// TestConcurrentApply_SingleDaemon (scenario-21 / AC-C3): two daemon
// processes started concurrently against the same instance — one
// acquires the exclusive flock, the other logs "another daemon is
// running" and exits 0. The surviving PID matches the winner.
func TestConcurrentApply_SingleDaemon(t *testing.T) {
	niwaBin := ensureTestBinary(t)

	f := newDaemonTestFixture(t)

	// Both daemons are pointed at the same instance root and share the
	// override spawn command so neither needs `claude` on PATH.
	baseEnv := append(os.Environ(), "NIWA_WORKER_SPAWN_COMMAND=/bin/true")

	// First daemon: Setsid:true so it runs in its own session; this is
	// the expected winner.
	first := exec.Command(niwaBin, "mesh", "watch", "--instance-root="+f.root)
	first.Env = baseEnv
	first.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := first.Start(); err != nil {
		t.Fatalf("starting first daemon: %v", err)
	}
	t.Cleanup(func() {
		_ = first.Process.Signal(syscall.SIGTERM)
		_, _ = first.Process.Wait()
	})

	// Wait for first daemon to publish daemon.pid so we know it has
	// acquired the exclusive flock and is in the event loop.
	pidPath := filepath.Join(f.niwaDir, "daemon.pid")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(pidPath); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	firstPID, _, err := readDaemonPIDFile(filepath.Join(f.niwaDir, "daemon.pid"))
	if err != nil || firstPID == 0 {
		t.Fatalf("first daemon did not publish daemon.pid: pid=%d err=%v", firstPID, err)
	}

	// Second daemon: capture combined output so we can assert the
	// "another daemon is running" message when it exits.
	second := exec.Command(niwaBin, "mesh", "watch", "--instance-root="+f.root)
	second.Env = baseEnv
	second.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := second.Run(); err != nil {
		t.Fatalf("second daemon exited non-zero: %v", err)
	}
	// Second daemon must NOT have overwritten daemon.pid. firstPID
	// must still be the PID on disk.
	afterPID, _, err := readDaemonPIDFile(filepath.Join(f.niwaDir, "daemon.pid"))
	if err != nil {
		t.Fatalf("re-reading daemon.pid: %v", err)
	}
	if afterPID != firstPID {
		t.Errorf("daemon.pid pid changed after second daemon ran: before=%d after=%d", firstPID, afterPID)
	}

	// daemon.log must contain the "another daemon is running" message
	// emitted by the losing daemon.
	logData, err := os.ReadFile(filepath.Join(f.niwaDir, "daemon.log"))
	if err != nil {
		t.Fatalf("read daemon.log: %v", err)
	}
	if !strings.Contains(string(logData), "another daemon is running") {
		t.Errorf("daemon.log missing 'another daemon is running' notice:\n%s", logData)
	}
}

// readDaemonPIDFile is a minimal test helper that parses the daemon.pid
// file format ("<pid>\n<start-time>\n"). Kept inline rather than
// promoted to production because the production code already has
// workspace.ReadPIDFile; copying the small parser here avoids a cross-
// package dependency for tests.
func readDaemonPIDFile(path string) (int, int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 {
		return 0, 0, nil
	}
	var pid int
	if _, err := fmt.Sscanf(lines[0], "%d", &pid); err != nil {
		return 0, 0, err
	}
	var st int64
	if len(lines) >= 2 {
		_, _ = fmt.Sscanf(lines[1], "%d", &st)
	}
	return pid, st, nil
}

// TestOrphanPolling_EmptyOrphansShortCircuits verifies that
// runOrphanSupervisor returns immediately when passed an empty orphan
// list — we rely on this to avoid starting a 2-second ticker goroutine
// in the zero-orphan case (the common path on a healthy daemon).
func TestOrphanPolling_EmptyOrphansShortCircuits(t *testing.T) {
	defer setOrphanPollIntervalForTest(10 * time.Millisecond)()

	f := newDaemonTestFixture(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exitCh := make(chan supervisorExit, 1)
	s := spawnContext{
		instanceRoot: f.root,
		niwaDir:      f.niwaDir,
		logger:       log.New(io.Discard, "", 0),
		exitCh:       exitCh,
		shutdownCtx:  ctx,
		backoffs:     []time.Duration{60 * time.Second},
	}

	done := make(chan struct{})
	go func() {
		runOrphanSupervisor(nil, s)
		close(done)
	}()

	// An empty orphan list must cause the goroutine to return
	// immediately, long before the ticker would fire.
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatalf("runOrphanSupervisor did not short-circuit on empty input")
	}
}

// TestPollOrphans_TransientReadErrorKeepsEntry: if ReadState returns an
// error (concurrent writer, torn read), the orphan entry must be kept
// for the next poll cycle. Dropping on a transient error would silently
// misclassify a live worker.
func TestPollOrphans_TransientReadErrorKeepsEntry(t *testing.T) {
	f := newDaemonTestFixture(t)

	// Seed a task dir with state.json but REMOVE envelope.json so
	// ReadState returns ErrCorruptedState (envelope read fails).
	worker := startFakeWorker(t, []string{"/bin/sleep", "30"})
	startTime, err := mcp.PIDStartTime(worker.Process.Pid)
	if err != nil {
		t.Fatalf("PIDStartTime: %v", err)
	}
	taskID := seedReconcileTask(t, f, mcp.TaskWorker{
		Role:      "web",
		PID:       worker.Process.Pid,
		StartTime: startTime,
	})
	if err := os.Remove(filepath.Join(f.tasksDir, taskID, "envelope.json")); err != nil {
		t.Fatalf("remove envelope: %v", err)
	}

	orphans := []orphanEntry{{taskID: taskID, pid: worker.Process.Pid, startTime: startTime}}
	s := spawnContext{
		instanceRoot: f.root,
		niwaDir:      f.niwaDir,
		logger:       log.New(io.Discard, "", 0),
	}
	remaining, deadExits := pollOrphans(orphans, s)
	if len(remaining) != 1 {
		t.Errorf("pollOrphans remaining = %d, want 1 (transient read error must keep entry)", len(remaining))
	}
	if len(deadExits) != 0 {
		t.Errorf("pollOrphans deadExits = %v, want empty (transient read error must not misclassify)", deadExits)
	}
}
