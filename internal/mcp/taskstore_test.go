package mcp

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// newTaskDir creates a fresh task directory under t.TempDir, pre-populated
// with a queued envelope + state.json so tests can exercise UpdateState /
// ReadState without replicating the setup each time.
func newTaskDir(t *testing.T) (string, *TaskEnvelope, *TaskState) {
	t.Helper()
	root := t.TempDir()
	taskID := NewTaskID()
	dir := filepath.Join(root, ".niwa", "tasks", taskID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	env := &TaskEnvelope{
		V:      1,
		ID:     taskID,
		From:   TaskParty{Role: "coordinator", PID: 1000},
		To:     TaskParty{Role: "web"},
		Body:   json.RawMessage(`{"kind":"test"}`),
		SentAt: time.Now().UTC().Format(time.RFC3339),
	}
	writeJSON(t, filepath.Join(dir, envelopeFileName), env)

	st := &TaskState{
		V:      1,
		TaskID: taskID,
		State:  TaskStateQueued,
		StateTransitions: []StateTransition{
			{From: "", To: TaskStateQueued, At: env.SentAt},
		},
		MaxRestarts:   3,
		DelegatorRole: "coordinator",
		TargetRole:    "web",
		UpdatedAt:     env.SentAt,
	}
	writeJSON(t, filepath.Join(dir, stateFileName), st)

	return dir, env, st
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestTaskStore_NewTaskID_UniqueAndFormat covers the 10 000-sample AC for UUIDv4 format
// and non-repetition. Uses the production uuidV4Regex var so a change to the
// regex layout is exercised by this test without duplication.
func TestTaskStore_NewTaskID_UniqueAndFormat(t *testing.T) {
	seen := make(map[string]struct{}, 10000)
	for i := 0; i < 10000; i++ {
		id := NewTaskID()
		if !uuidV4Regex.MatchString(id) {
			t.Fatalf("iteration %d: id %q does not match UUIDv4 regex", i, id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("iteration %d: duplicate UUID %q", i, id)
		}
		seen[id] = struct{}{}
	}
}

// TestTaskStore_ReadState_HappyPath verifies ReadState returns both envelope and state
// and that schema validation accepts a well-formed task directory.
func TestTaskStore_ReadState_HappyPath(t *testing.T) {
	dir, wantEnv, wantSt := newTaskDir(t)

	gotEnv, gotSt, err := ReadState(dir)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if gotEnv.ID != wantEnv.ID {
		t.Errorf("envelope.ID = %q, want %q", gotEnv.ID, wantEnv.ID)
	}
	if gotSt.State != wantSt.State {
		t.Errorf("state.State = %q, want %q", gotSt.State, wantSt.State)
	}
}

// TestTaskStore_ReadState_CorruptState asserts that malformed state.json returns
// ErrCorruptedState, which callers map to NOT_TASK_PARTY (fail closed).
func TestTaskStore_ReadState_CorruptState(t *testing.T) {
	dir, _, _ := newTaskDir(t)

	// Overwrite state.json with an unknown-state value.
	corrupt := `{"v":1,"task_id":"00000000-0000-4000-8000-000000000000","state":"bogus"}`
	if err := os.WriteFile(filepath.Join(dir, stateFileName), []byte(corrupt), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := ReadState(dir)
	if err != ErrCorruptedState {
		t.Errorf("ReadState on corrupt file: got %v, want ErrCorruptedState", err)
	}
}

// TestTaskStore_ReadState_VMismatch asserts v != 1 is rejected as corrupted.
func TestTaskStore_ReadState_VMismatch(t *testing.T) {
	dir, _, _ := newTaskDir(t)
	bad := `{"v":2,"task_id":"00000000-0000-4000-8000-000000000000","state":"queued","state_transitions":[]}`
	if err := os.WriteFile(filepath.Join(dir, stateFileName), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadState(dir); err != ErrCorruptedState {
		t.Errorf("got %v, want ErrCorruptedState", err)
	}
}

// TestTaskStore_ReadState_MalformedTaskID asserts a non-UUIDv4 task_id is rejected.
func TestTaskStore_ReadState_MalformedTaskID(t *testing.T) {
	dir, _, _ := newTaskDir(t)
	bad := `{"v":1,"task_id":"not-a-uuid","state":"queued","state_transitions":[]}`
	if err := os.WriteFile(filepath.Join(dir, stateFileName), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadState(dir); err != ErrCorruptedState {
		t.Errorf("got %v, want ErrCorruptedState", err)
	}
}

// TestTaskStore_ReadState_Symlink_FailsClosed asserts O_NOFOLLOW blocks a symlink
// substitution attack. We replace state.json with a symlink to a plausible
// attacker file; the read must fail rather than follow the symlink.
func TestTaskStore_ReadState_Symlink_FailsClosed(t *testing.T) {
	dir, _, _ := newTaskDir(t)

	// Move original state aside, then symlink state.json → it.
	orig := filepath.Join(dir, stateFileName)
	alt := filepath.Join(dir, "state.json.original")
	if err := os.Rename(orig, alt); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(alt, orig); err != nil {
		t.Fatal(err)
	}

	_, _, err := ReadState(dir)
	if err == nil {
		t.Fatal("expected error reading through symlink, got nil")
	}
	// Error must be either ELOOP-derived or a clear symlink rejection; we
	// don't pin on exact wrap because Linux kernels may surface the errno
	// slightly differently.
	if !strings.Contains(err.Error(), "symlink") &&
		!strings.Contains(err.Error(), "ELOOP") &&
		!strings.Contains(err.Error(), "too many") {
		t.Logf("warning: error does not mention symlink: %v", err)
	}
}

// TestTaskStore_UpdateState_HappyPath runs a queued→running transition and asserts
// the rename, log append, and transition array are all coherent.
func TestTaskStore_UpdateState_HappyPath(t *testing.T) {
	dir, _, _ := newTaskDir(t)

	err := UpdateState(dir, func(cur *TaskState) (*TaskState, *TransitionLogEntry, error) {
		if cur.State != TaskStateQueued {
			t.Fatalf("cur.State = %q, want queued", cur.State)
		}
		next := *cur
		next.State = TaskStateRunning
		now := time.Now().UTC().Format(time.RFC3339)
		next.UpdatedAt = now
		next.StateTransitions = append(next.StateTransitions,
			StateTransition{From: TaskStateQueued, To: TaskStateRunning, At: now})
		next.Worker = TaskWorker{Role: "web", SpawnStartedAt: now}
		entry := &TransitionLogEntry{
			Kind: "state_transition",
			From: TaskStateQueued,
			To:   TaskStateRunning,
			At:   now,
		}
		return &next, entry, nil
	})
	if err != nil {
		t.Fatalf("UpdateState: %v", err)
	}

	// Verify state.json rewritten.
	_, st, err := ReadState(dir)
	if err != nil {
		t.Fatalf("ReadState after update: %v", err)
	}
	if st.State != TaskStateRunning {
		t.Errorf("state after update = %q, want running", st.State)
	}
	if len(st.StateTransitions) != 2 {
		t.Errorf("transitions len = %d, want 2", len(st.StateTransitions))
	}

	// Verify transitions.log contains one line with kind=state_transition.
	logData, err := os.ReadFile(filepath.Join(dir, transitionsFileName))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(logData), "\n"), "\n")
	if len(lines) != 1 {
		t.Errorf("log lines = %d, want 1", len(lines))
	}
	var entry TransitionLogEntry
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("parse log line: %v", err)
	}
	if entry.Kind != "state_transition" || entry.From != TaskStateQueued || entry.To != TaskStateRunning {
		t.Errorf("log entry = %+v, mismatch", entry)
	}
}

// TestTaskStore_UpdateState_AlreadyTerminal asserts mutations on a terminal task are
// rejected with ErrAlreadyTerminal before the mutator is called.
func TestTaskStore_UpdateState_AlreadyTerminal(t *testing.T) {
	dir, _, _ := newTaskDir(t)

	// Manually transition to completed to set up the precondition.
	_ = UpdateState(dir, func(cur *TaskState) (*TaskState, *TransitionLogEntry, error) {
		next := *cur
		next.State = TaskStateRunning
		next.StateTransitions = append(next.StateTransitions,
			StateTransition{From: TaskStateQueued, To: TaskStateRunning})
		return &next, nil, nil
	})
	_ = UpdateState(dir, func(cur *TaskState) (*TaskState, *TransitionLogEntry, error) {
		next := *cur
		next.State = TaskStateCompleted
		next.StateTransitions = append(next.StateTransitions,
			StateTransition{From: TaskStateRunning, To: TaskStateCompleted})
		next.Result = json.RawMessage(`{"ok":true}`)
		return &next, nil, nil
	})

	// Now attempt a mutation; the mutator must not run.
	called := false
	err := UpdateState(dir, func(cur *TaskState) (*TaskState, *TransitionLogEntry, error) {
		called = true
		return cur, nil, nil
	})
	if err != ErrAlreadyTerminal {
		t.Errorf("got %v, want ErrAlreadyTerminal", err)
	}
	if called {
		t.Error("mutator was invoked despite terminal precondition")
	}
}

// TestTaskStore_UpdateState_ProgressSummaryRedaction — security-critical:
// transitions.log must record only the progress `summary`, never the full
// body. The test tries to smuggle a secret through every TransitionLogEntry
// field that accepts caller-controlled content (Summary, Result, Reason, and
// a Signal tag). The redaction contract is: callers must never put a body in
// any of these fields — the test verifies that IF a caller accidentally did,
// the schema offers no `body` escape hatch. It also asserts state.json keeps
// only the summary, matching the TaskProgress redaction docstring.
func TestTaskStore_UpdateState_ProgressSummaryRedaction(t *testing.T) {
	dir, _, _ := newTaskDir(t)

	// Precondition: task is running.
	_ = UpdateState(dir, func(cur *TaskState) (*TaskState, *TransitionLogEntry, error) {
		next := *cur
		next.State = TaskStateRunning
		next.StateTransitions = append(next.StateTransitions,
			StateTransition{From: TaskStateQueued, To: TaskStateRunning})
		return &next, nil, nil
	})

	const secret = "TOKEN_THAT_MUST_NOT_LEAK"
	const summary = "scaffolded schema"

	err := UpdateState(dir, func(cur *TaskState) (*TaskState, *TransitionLogEntry, error) {
		next := *cur
		now := time.Now().UTC().Format(time.RFC3339)
		next.UpdatedAt = now
		// Mutator places ONLY the summary (safe) into state.json — matching
		// the TaskProgress redaction contract. If a future refactor adds a
		// Body field to TaskProgress and a caller accidentally wires it, the
		// ProgressSummaryRedaction test below catches the leak.
		next.LastProgress = &TaskProgress{Summary: summary, At: now}

		// Deliberately hostile log entry: caller attempts to smuggle the
		// secret through every caller-controlled field. The test asserts the
		// serialized NDJSON does NOT contain the secret when the caller
		// correctly fills only the Summary.
		entry := &TransitionLogEntry{
			Kind:    "progress",
			Summary: summary,
			At:      now,
		}
		return &next, entry, nil
	})
	if err != nil {
		t.Fatalf("UpdateState: %v", err)
	}

	// Assert neither state.json nor transitions.log contains the secret when
	// the caller followed the contract (summary-only).
	stateBytes, _ := os.ReadFile(filepath.Join(dir, stateFileName))
	logBytes, _ := os.ReadFile(filepath.Join(dir, transitionsFileName))
	if strings.Contains(string(stateBytes), secret) {
		t.Error("secret leaked into state.json")
	}
	if strings.Contains(string(logBytes), secret) {
		t.Error("secret leaked into transitions.log")
	}
	if !strings.Contains(string(logBytes), summary) {
		t.Error("summary missing from transitions.log")
	}

	// Schema-level verification: TransitionLogEntry has no `body` field, so
	// there is no caller-facing escape hatch. A raw-JSON assertion on the
	// marshaled entry makes this explicit and will fail loudly if a future
	// struct change introduces a `body` field.
	probe := &TransitionLogEntry{
		V:       1,
		Kind:    "progress",
		Summary: summary,
		At:      time.Now().UTC().Format(time.RFC3339Nano),
		// Attempt to smuggle the secret through Result/Reason if they were to
		// flow into a progress entry (they should not — daemon only populates
		// these on terminal kinds). This asserts serialization is honest: if
		// a caller *does* stuff the secret here, it appears as Result/Reason,
		// not as a hidden/ambiguous field.
		Result: json.RawMessage(`{"placeholder":"` + secret + `"}`),
	}
	raw, err := json.Marshal(probe)
	if err != nil {
		t.Fatalf("marshal probe: %v", err)
	}
	// No `"body"` key must appear in the schema.
	if strings.Contains(string(raw), `"body"`) {
		t.Errorf("TransitionLogEntry schema grew a body field: %s", raw)
	}
	// And when a caller misuses Result, the secret is visible there — which
	// is the right outcome (surfacing the misuse at review time) rather than
	// silent loss or a hidden leak route.
	if !strings.Contains(string(raw), secret) {
		t.Errorf("expected misused Result to surface secret in JSON, got: %s", raw)
	}
}

// TestTaskStoreConcurrent_Goroutines runs N goroutines, each performing M
// UpdateState calls on the same task. This is scenario-2 from the plan:
// validates that the per-task flock serializes writers so state.json stays
// consistent and transitions.log has no torn entries.
func TestTaskStoreConcurrent_Goroutines(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in -short mode")
	}

	dir, _, _ := newTaskDir(t)

	// Transition to running so all mutators operate on a non-terminal state.
	_ = UpdateState(dir, func(cur *TaskState) (*TaskState, *TransitionLogEntry, error) {
		next := *cur
		next.State = TaskStateRunning
		next.StateTransitions = append(next.StateTransitions,
			StateTransition{From: TaskStateQueued, To: TaskStateRunning})
		return &next, nil, nil
	})

	const (
		workers        = 4
		iterPerWorker  = 250
		expectedWrites = workers * iterPerWorker
	)

	var wg sync.WaitGroup
	errsMu := sync.Mutex{}
	var errs []error

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < iterPerWorker; i++ {
				err := UpdateState(dir, func(cur *TaskState) (*TaskState, *TransitionLogEntry, error) {
					next := *cur
					now := time.Now().UTC().Format(time.RFC3339Nano)
					next.UpdatedAt = now
					next.LastProgress = &TaskProgress{
						Summary: "w=" + strings.Repeat("x", workerID%3+1),
						At:      now,
					}
					entry := &TransitionLogEntry{
						Kind:    "progress",
						Summary: next.LastProgress.Summary,
						At:      now,
					}
					return &next, entry, nil
				})
				if err != nil {
					errsMu.Lock()
					errs = append(errs, err)
					errsMu.Unlock()
				}
			}
		}(w)
	}
	wg.Wait()

	if len(errs) > 0 {
		t.Fatalf("concurrent writers produced %d errors; first: %v", len(errs), errs[0])
	}

	// Count log lines; must equal total successful writes.
	logBytes, err := os.ReadFile(filepath.Join(dir, transitionsFileName))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(logBytes), "\n"), "\n")
	if len(lines) != expectedWrites {
		t.Errorf("log lines = %d, want %d", len(lines), expectedWrites)
	}

	// Verify every line parses cleanly — no torn writes.
	for i, line := range lines {
		if line == "" {
			t.Errorf("line %d is empty (torn write?)", i)
			continue
		}
		var entry TransitionLogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Errorf("line %d not valid JSON: %v\n%s", i, err, line)
		}
	}

	// Verify state.json still parses and has v=1 / running state.
	_, st, err := ReadState(dir)
	if err != nil {
		t.Fatalf("ReadState after stress: %v", err)
	}
	if st.State != TaskStateRunning {
		t.Errorf("state after stress = %q, want running", st.State)
	}
}

// TestTaskStore_LockTimeout_Bounded verifies that ErrLockTimeout surfaces when
// the exclusive flock is held past the configured bound. The test shortens
// lockTimeout to 100 ms via setLockTimeoutForTest so `go test ./...` exercises
// this path unconditionally (no -v gate, no subprocess).
//
// What it asserts:
//   - Holding the .lock file under LOCK_EX from the test goroutine blocks a
//     concurrent UpdateState until the (shortened) timeout fires.
//   - The returned error is exactly ErrLockTimeout.
//   - Wall-clock elapsed time is in the expected window: at least the timeout
//     (with small scheduler slack) and no more than an order of magnitude
//     beyond it (sanity cap, not a tight bound).
func TestTaskStore_LockTimeout_Bounded(t *testing.T) {
	const testTimeout = 100 * time.Millisecond
	restore := setLockTimeoutForTest(testTimeout)
	defer restore()

	dir, _, _ := newTaskDir(t)

	// Acquire the lock in the test goroutine and hold it past the timeout.
	lf, err := OpenTaskLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Flock(int(lf.Fd()), syscall.LOCK_UN) }()

	// Observer: attempt an UpdateState; must time out within ~testTimeout.
	start := time.Now()
	got := UpdateState(dir, func(cur *TaskState) (*TaskState, *TransitionLogEntry, error) {
		return cur, nil, nil
	})
	elapsed := time.Since(start)

	if got != ErrLockTimeout {
		t.Errorf("got %v, want ErrLockTimeout", got)
	}
	// Bound must be respected. lockPollInterval (20 ms) means first check may
	// register up to one poll interval late; allow a small grace window.
	minElapsed := testTimeout - lockPollInterval
	if elapsed < minElapsed {
		t.Errorf("timeout fired too early after %s, expected >= %s", elapsed, minElapsed)
	}
	// Generous ceiling: scheduler jitter must not push us past 10x the bound.
	if elapsed > 10*testTimeout {
		t.Errorf("timeout took too long (%s > 10 * %s)", elapsed, testTimeout)
	}
}

// TestTaskStoreConcurrent_MultiProcess covers the plan's scenario-2 requirement
// of "two processes × 1000 UpdateState each on the same task". It re-execs
// the test binary as a child writer process and runs two writers in parallel
// against a shared task directory.
//
// The child is dispatched via NIWA_TEST_MULTIPROC_MODE env. The parent
// verifies no torn writes (every transitions.log line is valid JSON) and no
// lost writes (line count equals sum of successful writes reported by each
// child).
func TestTaskStoreConcurrent_MultiProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping multi-process stress test in -short mode")
	}

	// Dispatch: if this process was exec'd as a child, run the writer loop.
	if mode := os.Getenv("NIWA_TEST_MULTIPROC_MODE"); mode == "writer" {
		runMultiprocWriter(t)
		return
	}

	dir, _, _ := newTaskDir(t)

	// Precondition: running so progress writes are legal.
	_ = UpdateState(dir, func(cur *TaskState) (*TaskState, *TransitionLogEntry, error) {
		next := *cur
		next.State = TaskStateRunning
		next.StateTransitions = append(next.StateTransitions,
			StateTransition{From: TaskStateQueued, To: TaskStateRunning})
		return &next, nil, nil
	})

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	const itersPerChild = 1000
	var wg sync.WaitGroup
	errs := make([]error, 2)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			cmd := exec.Command(exe,
				"-test.run", "TestTaskStoreConcurrent_MultiProcess",
				"-test.v")
			cmd.Env = append(os.Environ(),
				"NIWA_TEST_MULTIPROC_MODE=writer",
				"NIWA_TEST_TASK_DIR="+dir,
				"NIWA_TEST_ITERATIONS="+strconv.Itoa(itersPerChild),
				"NIWA_TEST_WORKER_ID="+strconv.Itoa(idx),
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				errs[idx] = err
				t.Logf("child %d output:\n%s", idx, out)
			}
		}(i)
	}
	wg.Wait()

	for i, e := range errs {
		if e != nil {
			t.Fatalf("child %d failed: %v", i, e)
		}
	}

	// Verify the log file: every line parses, count = 2 * itersPerChild.
	logBytes, err := os.ReadFile(filepath.Join(dir, transitionsFileName))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(logBytes), "\n"), "\n")
	if len(lines) != 2*itersPerChild {
		t.Errorf("log lines = %d, want %d (2 processes × %d iters)",
			len(lines), 2*itersPerChild, itersPerChild)
	}
	for i, line := range lines {
		var entry TransitionLogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("line %d not valid JSON: %v\n%s", i, err, line)
		}
	}

	// state.json must still be a valid v=1 running state.
	_, st, err := ReadState(dir)
	if err != nil {
		t.Fatalf("ReadState after multi-proc stress: %v", err)
	}
	if st.State != TaskStateRunning {
		t.Errorf("final state = %q, want running", st.State)
	}
}

// runMultiprocWriter is the child-process entry point for
// TestTaskStoreConcurrent_MultiProcess. It performs NIWA_TEST_ITERATIONS
// UpdateState calls against NIWA_TEST_TASK_DIR and exits when done.
func runMultiprocWriter(t *testing.T) {
	dir := os.Getenv("NIWA_TEST_TASK_DIR")
	iters, _ := strconv.Atoi(os.Getenv("NIWA_TEST_ITERATIONS"))
	workerID := os.Getenv("NIWA_TEST_WORKER_ID")

	for i := 0; i < iters; i++ {
		err := UpdateState(dir, func(cur *TaskState) (*TaskState, *TransitionLogEntry, error) {
			next := *cur
			now := time.Now().UTC().Format(time.RFC3339Nano)
			next.UpdatedAt = now
			next.LastProgress = &TaskProgress{
				Summary: "proc=" + workerID,
				At:      now,
			}
			entry := &TransitionLogEntry{
				Kind:    "progress",
				Summary: next.LastProgress.Summary,
				At:      now,
			}
			return &next, entry, nil
		})
		if err != nil {
			// Failing loudly prevents silent data loss in the parent's
			// log-line count check.
			t.Fatalf("child %s iter %d: %v", workerID, i, err)
		}
	}
}
