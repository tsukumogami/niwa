package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/mcp"
)

// resumeTestFixture is a minimal test harness for retrySpawn resume-path tests.
// It wires up a spawnContext with a fast-exiting spawnBin and a buffered
// exitCh that is never pumped — supervisor goroutine exit events sit in the
// buffer, so they never trigger a second handleSupervisorExit.
type resumeTestFixture struct {
	instanceRoot string
	tasksDir     string
	claudeHome   string // fake home dir for Guard 4 session file lookup
	spawnCtx     spawnContext
}

func newResumeTestFixture(t *testing.T) *resumeTestFixture {
	t.Helper()
	root := t.TempDir()
	niwaDir := filepath.Join(root, ".niwa")
	tasksDir := filepath.Join(niwaDir, "tasks")
	for _, d := range []string{
		filepath.Join(niwaDir, "roles", "coordinator", "inbox", "in-progress"),
		tasksDir,
	} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	claudeHome := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	exitCh := make(chan supervisorExit, 64)
	s := spawnContext{
		instanceRoot:  root,
		niwaDir:       niwaDir,
		spawnBin:      "/bin/true",
		logger:        log.New(io.Discard, "", 0),
		exitCh:        exitCh,
		wg:            &sync.WaitGroup{},
		shutdownCtx:   ctx,
		backoffs:      []time.Duration{time.Minute},
		claudeHomeDir: claudeHome,
	}
	return &resumeTestFixture{
		instanceRoot: root,
		tasksDir:     tasksDir,
		claudeHome:   claudeHome,
		spawnCtx:     s,
	}
}

// makeRunningTask creates a task directory with envelope.json + state.json in
// running state. The caller can set ClaudeSessionID, ResumeCount, and
// MaxResumes via the mutable fields on the returned *mcp.TaskState.
func (f *resumeTestFixture) makeRunningTask(t *testing.T, sessionID string, resumeCount, maxResumes, restartCount int) string {
	t.Helper()
	taskID := mcp.NewTaskID()
	taskDir := filepath.Join(f.tasksDir, taskID)
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		t.Fatalf("mkdir task dir: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)

	env := mcp.TaskEnvelope{
		V: 1, ID: taskID,
		From:   mcp.TaskParty{Role: "coordinator", PID: os.Getpid()},
		To:     mcp.TaskParty{Role: "coordinator"},
		Body:   json.RawMessage(`{}`),
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
		MaxResumes:    maxResumes,
		RestartCount:  restartCount,
		DelegatorRole: "coordinator",
		TargetRole:    "coordinator",
		Worker: mcp.TaskWorker{
			Role:            "coordinator",
			PID:             os.Getpid(),
			StartTime:       1000,
			ClaudeSessionID: sessionID,
			ResumeCount:     resumeCount,
		},
		UpdatedAt: now,
	}
	stBytes, _ := json.MarshalIndent(st, "", "  ")
	if err := os.WriteFile(filepath.Join(taskDir, "state.json"), stBytes, 0o600); err != nil {
		t.Fatalf("write state.json: %v", err)
	}
	return taskID
}

// writeSessionJSONL creates a valid JSONL session file for sessionID under
// claudeHome using the coordinator CWD (instanceRoot).
func (f *resumeTestFixture) writeSessionJSONL(t *testing.T, sessionID string) {
	t.Helper()
	// coordinator CWD == instanceRoot
	encoded := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(f.instanceRoot))
	dir := filepath.Join(f.claudeHome, ".claude", "projects", encoded)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	// A minimal valid JSONL: one JSON object per line.
	content := `{"type":"system","content":"session start"}` + "\n"
	path := filepath.Join(dir, sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write session jsonl: %v", err)
	}
}

// readTaskState reads state.json for taskID and fails the test on error.
func (f *resumeTestFixture) readTaskState(t *testing.T, taskID string) *mcp.TaskState {
	t.Helper()
	taskDir := filepath.Join(f.tasksDir, taskID)
	_, st, err := mcp.ReadState(taskDir)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	return st
}

// --- Tests ---

// TestResume_Guard5Ordering: ResumeCount < MaxResumes, file passes integrity,
// but ClaudeSessionID fails sessionIDRegex → fresh spawn.
// Verifies Guard 5 is evaluated in strict order after Guards 2-4 pass.
func TestResume_Guard5Ordering(t *testing.T) {
	f := newResumeTestFixture(t)
	// "abc" is too short (< 8 chars) → fails sessionIDRegex.
	sessionID := "abc"
	taskID := f.makeRunningTask(t, sessionID, 0, 2, 0)
	f.writeSessionJSONL(t, sessionID)

	retrySpawn(taskID, "coordinator", f.spawnCtx)
	f.spawnCtx.wg.Wait()

	st := f.readTaskState(t, taskID)
	if st.RestartCount != 1 {
		t.Errorf("RestartCount = %d, want 1 (fresh spawn)", st.RestartCount)
	}
	if st.Worker.ResumeCount != 0 {
		t.Errorf("ResumeCount = %d, want 0", st.Worker.ResumeCount)
	}
	if st.Worker.ClaudeSessionID != "" {
		t.Errorf("ClaudeSessionID = %q, want empty", st.Worker.ClaudeSessionID)
	}
}

// TestResume_Guard2Ordering: ResumeCount == effective MaxResumes, file passes
// integrity → fresh spawn. Verifies Guard 2 triggers before Guards 3-5.
func TestResume_Guard2Ordering(t *testing.T) {
	f := newResumeTestFixture(t)
	sessionID := "valid-session-id-abcdef01"
	taskID := f.makeRunningTask(t, sessionID, 2, 2, 1)
	f.writeSessionJSONL(t, sessionID)

	priorRestart := 1
	retrySpawn(taskID, "coordinator", f.spawnCtx)
	f.spawnCtx.wg.Wait()

	st := f.readTaskState(t, taskID)
	if st.RestartCount != priorRestart+1 {
		t.Errorf("RestartCount = %d, want %d (fresh spawn)", st.RestartCount, priorRestart+1)
	}
	if st.Worker.ResumeCount != 0 {
		t.Errorf("ResumeCount = %d, want 0", st.Worker.ResumeCount)
	}
	if st.Worker.ClaudeSessionID != "" {
		t.Errorf("ClaudeSessionID = %q, want empty", st.Worker.ClaudeSessionID)
	}
}

// TestResume_ResumeTaken: ResumeCount=1, MaxResumes=2 → resume path taken.
// ResumeCount increments to 2, RestartCount unchanged.
func TestResume_ResumeTaken(t *testing.T) {
	f := newResumeTestFixture(t)
	sessionID := "valid-session-id-abcdef02"
	priorRestart := 1
	taskID := f.makeRunningTask(t, sessionID, 1, 2, priorRestart)
	f.writeSessionJSONL(t, sessionID)

	retrySpawn(taskID, "coordinator", f.spawnCtx)
	f.spawnCtx.wg.Wait()

	st := f.readTaskState(t, taskID)
	if st.Worker.ResumeCount != 2 {
		t.Errorf("ResumeCount = %d, want 2", st.Worker.ResumeCount)
	}
	if st.RestartCount != priorRestart {
		t.Errorf("RestartCount = %d, want %d (unchanged on resume)", st.RestartCount, priorRestart)
	}
}

// TestResume_SessionIDPreserved: resume path preserves ClaudeSessionID in state.json.
func TestResume_SessionIDPreserved(t *testing.T) {
	f := newResumeTestFixture(t)
	sessionID := "test-session-id-abc01"
	taskID := f.makeRunningTask(t, sessionID, 0, 2, 0)
	f.writeSessionJSONL(t, sessionID)

	retrySpawn(taskID, "coordinator", f.spawnCtx)
	f.spawnCtx.wg.Wait()

	st := f.readTaskState(t, taskID)
	if st.Worker.ClaudeSessionID != sessionID {
		t.Errorf("ClaudeSessionID = %q, want %q", st.Worker.ClaudeSessionID, sessionID)
	}
}

// TestResume_FreshSpawnCounters: fresh spawn (Guard 2) increments RestartCount,
// resets ResumeCount, clears ClaudeSessionID.
func TestResume_FreshSpawnCounters(t *testing.T) {
	f := newResumeTestFixture(t)
	sessionID := "valid-session-fresh-01"
	priorRestart := 2
	taskID := f.makeRunningTask(t, sessionID, 2, 2, priorRestart)
	f.writeSessionJSONL(t, sessionID)

	retrySpawn(taskID, "coordinator", f.spawnCtx)
	f.spawnCtx.wg.Wait()

	st := f.readTaskState(t, taskID)
	if st.RestartCount != priorRestart+1 {
		t.Errorf("RestartCount = %d, want %d", st.RestartCount, priorRestart+1)
	}
	if st.Worker.ResumeCount != 0 {
		t.Errorf("ResumeCount = %d, want 0", st.Worker.ResumeCount)
	}
	if st.Worker.ClaudeSessionID != "" {
		t.Errorf("ClaudeSessionID = %q, want empty", st.Worker.ClaudeSessionID)
	}
}

// TestResume_Guard1_RestartCapExhausted: RestartCount==MaxRestarts, valid
// ClaudeSessionID, ResumeCount=1. handleSupervisorExit must abandon without
// entering retrySpawn; counters stay unchanged.
func TestResume_Guard1_RestartCapExhausted(t *testing.T) {
	f := newResumeTestFixture(t)
	sessionID := "valid-session-id-g1test"
	taskID := f.makeRunningTask(t, sessionID, 1, 2, 3) // RestartCount==MaxRestarts==3
	f.writeSessionJSONL(t, sessionID)

	ex := supervisorExit{taskID: taskID, exitCode: 1}
	handleSupervisorExit(ex, f.spawnCtx)
	f.spawnCtx.wg.Wait()

	st := f.readTaskState(t, taskID)
	// retrySpawn was never called; counters must be unchanged.
	if st.RestartCount != 3 {
		t.Errorf("RestartCount = %d, want 3 (unchanged)", st.RestartCount)
	}
	if st.Worker.ResumeCount != 1 {
		t.Errorf("ResumeCount = %d, want 1 (unchanged)", st.Worker.ResumeCount)
	}
	if st.State != mcp.TaskStateAbandoned {
		t.Errorf("State = %q, want abandoned", st.State)
	}
}

// TestResume_Guard3_EmptySessionID: empty ClaudeSessionID → fresh spawn.
func TestResume_Guard3_EmptySessionID(t *testing.T) {
	f := newResumeTestFixture(t)
	priorRestart := 0
	taskID := f.makeRunningTask(t, "", 0, 2, priorRestart)

	retrySpawn(taskID, "coordinator", f.spawnCtx)
	f.spawnCtx.wg.Wait()

	st := f.readTaskState(t, taskID)
	if st.RestartCount != priorRestart+1 {
		t.Errorf("RestartCount = %d, want %d", st.RestartCount, priorRestart+1)
	}
	if st.Worker.ResumeCount != 0 {
		t.Errorf("ResumeCount = %d, want 0", st.Worker.ResumeCount)
	}
}

// TestResume_Guard4_FileIntegrityFail: session JSONL missing → fresh spawn.
func TestResume_Guard4_FileIntegrityFail(t *testing.T) {
	f := newResumeTestFixture(t)
	sessionID := "valid-session-g4-test01"
	priorRestart := 0
	taskID := f.makeRunningTask(t, sessionID, 0, 2, priorRestart)
	// Intentionally do NOT write session file.

	retrySpawn(taskID, "coordinator", f.spawnCtx)
	f.spawnCtx.wg.Wait()

	st := f.readTaskState(t, taskID)
	if st.RestartCount != priorRestart+1 {
		t.Errorf("RestartCount = %d, want %d (fresh spawn)", st.RestartCount, priorRestart+1)
	}
	if st.Worker.ResumeCount != 0 {
		t.Errorf("ResumeCount = %d, want 0", st.Worker.ResumeCount)
	}
	if st.Worker.ClaudeSessionID != "" {
		t.Errorf("ClaudeSessionID = %q, want empty", st.Worker.ClaudeSessionID)
	}
}

// TestResume_MaxResumesZeroTreatedAsTwo: MaxResumes=0 in state.json is treated
// as effective MaxResumes=2. With ResumeCount=1, resume path is taken.
func TestResume_MaxResumesZeroTreatedAsTwo(t *testing.T) {
	f := newResumeTestFixture(t)
	sessionID := "valid-session-maxr0-01"
	priorRestart := 0
	taskID := f.makeRunningTask(t, sessionID, 1, 0, priorRestart) // MaxResumes=0
	f.writeSessionJSONL(t, sessionID)

	retrySpawn(taskID, "coordinator", f.spawnCtx)
	f.spawnCtx.wg.Wait()

	st := f.readTaskState(t, taskID)
	// effective MaxResumes = 2, ResumeCount was 1 → resume taken (ResumeCount becomes 2)
	if st.Worker.ResumeCount != 2 {
		t.Errorf("ResumeCount = %d, want 2 (resume taken)", st.Worker.ResumeCount)
	}
	if st.RestartCount != priorRestart {
		t.Errorf("RestartCount = %d, want %d (unchanged on resume)", st.RestartCount, priorRestart)
	}
}

// TestResume_NoRestartCountBump: resume path does not increment RestartCount.
func TestResume_NoRestartCountBump(t *testing.T) {
	f := newResumeTestFixture(t)
	sessionID := "valid-session-nobump-01"
	priorRestart := 1
	taskID := f.makeRunningTask(t, sessionID, 0, 2, priorRestart)
	f.writeSessionJSONL(t, sessionID)

	retrySpawn(taskID, "coordinator", f.spawnCtx)
	f.spawnCtx.wg.Wait()

	st := f.readTaskState(t, taskID)
	if st.RestartCount != priorRestart {
		t.Errorf("RestartCount = %d, want %d (unchanged on resume)", st.RestartCount, priorRestart)
	}
}

// TestCheckSessionFileIntegrity_Valid verifies that a JSONL file with a valid
// JSON line passes Guard 4.
func TestCheckSessionFileIntegrity_Valid(t *testing.T) {
	homeDir := t.TempDir()
	cwd := t.TempDir()
	sessionID := "valid-session-integ-01"
	encoded := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(cwd))
	dir := filepath.Join(homeDir, ".claude", "projects", encoded)
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(filepath.Join(dir, sessionID+".jsonl"),
		[]byte(`{"type":"message"}`+"\n"), 0o600)

	if !checkSessionFileIntegrity(homeDir, cwd, sessionID) {
		t.Error("expected valid file to pass integrity check")
	}
}

// TestCheckSessionFileIntegrity_Missing verifies that a missing JSONL file
// fails Guard 4.
func TestCheckSessionFileIntegrity_Missing(t *testing.T) {
	if checkSessionFileIntegrity(t.TempDir(), t.TempDir(), "valid-session-miss01") {
		t.Error("expected missing file to fail integrity check")
	}
}

// TestCheckSessionFileIntegrity_Empty verifies that an empty JSONL file fails
// Guard 4.
func TestCheckSessionFileIntegrity_Empty(t *testing.T) {
	homeDir := t.TempDir()
	cwd := t.TempDir()
	sessionID := "valid-session-empty-01"
	encoded := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(cwd))
	dir := filepath.Join(homeDir, ".claude", "projects", encoded)
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(filepath.Join(dir, sessionID+".jsonl"), []byte{}, 0o600)

	if checkSessionFileIntegrity(homeDir, cwd, sessionID) {
		t.Error("expected empty file to fail integrity check")
	}
}

// TestCheckSessionFileIntegrity_NoJSONLine verifies that a file with no valid
// JSON lines fails Guard 4.
func TestCheckSessionFileIntegrity_NoJSONLine(t *testing.T) {
	homeDir := t.TempDir()
	cwd := t.TempDir()
	sessionID := "valid-session-nojson-1"
	encoded := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(cwd))
	dir := filepath.Join(homeDir, ".claude", "projects", encoded)
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(filepath.Join(dir, sessionID+".jsonl"),
		[]byte("not json at all\n"), 0o600)

	if checkSessionFileIntegrity(homeDir, cwd, sessionID) {
		t.Error("expected file with no JSON lines to fail integrity check")
	}
}
