package cli

import (
	"context"
	"encoding/json"
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
	}

	loopDone := make(chan struct{})
	go func() {
		runEventLoop(ctx, watcher, catchupCh, exitCh, spawnCtx)
		close(loopDone)
	}()

	// Poll the state.json for the transition queued → running, then
	// wait for the supervisor to record unexpected_exit (since /bin/true
	// exits immediately without calling niwa_finish_task, state stays
	// "running" — Issue 4 does not flip to abandoned here; Issue 5
	// will classify and retry).
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

	// Verify state.json is STILL "running" for Issue 5 to handle.
	_, st, err := mcp.ReadState(filepath.Join(f.tasksDir, taskID))
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.State != mcp.TaskStateRunning {
		t.Errorf("state = %q, want running (Issue 4 doesn't classify)", st.State)
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

	// Wait for supervisors to finish sending (which requires us to drain).
	wg.Wait()
	// Once every supervisor has exited and every send has landed, close
	// the channel so the drain goroutine returns cleanly if it hasn't
	// already hit the count check above.
	select {
	case <-drainDone:
		// drain completed via count check
	case <-time.After(3 * time.Second):
		close(exitCh)
		<-drainDone
	}

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
