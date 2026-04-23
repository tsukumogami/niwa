package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeTaskFixture creates `.niwa/tasks/<taskID>/` under instanceRoot with
// envelope + state populated from supplied fields. Leaves worker PID/start
// unset so tests can opt into the Linux hardening check explicitly.
func writeTaskFixture(t *testing.T, instanceRoot, delegatorRole, targetRole, state string) (taskID string) {
	t.Helper()
	taskID = NewTaskID()
	dir := filepath.Join(instanceRoot, ".niwa", "tasks", taskID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	env := TaskEnvelope{
		V:      1,
		ID:     taskID,
		From:   TaskParty{Role: delegatorRole, PID: 1000},
		To:     TaskParty{Role: targetRole},
		Body:   json.RawMessage(`{}`),
		SentAt: time.Now().UTC().Format(time.RFC3339),
	}
	envBytes, _ := json.MarshalIndent(env, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, envelopeFileName), envBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	st := TaskState{
		V:                1,
		TaskID:           taskID,
		State:            state,
		StateTransitions: []StateTransition{{From: "", To: TaskStateQueued, At: env.SentAt}},
		MaxRestarts:      3,
		DelegatorRole:    delegatorRole,
		TargetRole:       targetRole,
		UpdatedAt:        env.SentAt,
	}
	if state == TaskStateRunning || isTaskStateTerminal(state) {
		st.Worker = TaskWorker{Role: targetRole}
	}
	stBytes, _ := json.MarshalIndent(st, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, stateFileName), stBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return taskID
}

// errorCode extracts the structured error_code from an errResultCode-shaped
// toolResult. Returns "" when the result is not an error.
func errorCode(r *toolResult) string {
	if r == nil || !r.IsError || len(r.Content) == 0 {
		return ""
	}
	text := r.Content[0].Text
	const prefix = "error_code: "
	idx := strings.Index(text, prefix)
	if idx < 0 {
		return ""
	}
	rest := text[idx+len(prefix):]
	if nl := strings.Index(rest, "\n"); nl >= 0 {
		rest = rest[:nl]
	}
	return rest
}

// TestAuthorizeTaskCall_Delegator_HappyPath — the delegator role matches envelope.from.
func TestAuthorizeTaskCall_Delegator_HappyPath(t *testing.T) {
	root := t.TempDir()
	id := writeTaskFixture(t, root, "coordinator", "web", TaskStateQueued)

	env, st, errR := authorizeTaskCall(
		authIdentity{InstanceRoot: root, Role: "coordinator"},
		id, kindDelegator)
	if errR != nil {
		t.Fatalf("unexpected auth error: %s", errR.Content[0].Text)
	}
	if env.ID != id || st.TaskID != id {
		t.Errorf("envelope/state mismatch")
	}
}

// TestAuthorizeTaskCall_Delegator_WrongRole — a non-delegator caller gets NOT_TASK_OWNER.
func TestAuthorizeTaskCall_Delegator_WrongRole(t *testing.T) {
	root := t.TempDir()
	id := writeTaskFixture(t, root, "coordinator", "web", TaskStateQueued)

	_, _, errR := authorizeTaskCall(
		authIdentity{InstanceRoot: root, Role: "web"},
		id, kindDelegator)
	if got := errorCode(errR); got != "NOT_TASK_OWNER" {
		t.Errorf("got %q, want NOT_TASK_OWNER", got)
	}
}

// TestAuthorizeTaskCall_Delegator_Terminal — delegator kind rejects terminal tasks with
// TASK_ALREADY_TERMINAL (cancel / update on a completed task is invalid).
func TestAuthorizeTaskCall_Delegator_Terminal(t *testing.T) {
	root := t.TempDir()
	id := writeTaskFixture(t, root, "coordinator", "web", TaskStateCompleted)

	_, _, errR := authorizeTaskCall(
		authIdentity{InstanceRoot: root, Role: "coordinator"},
		id, kindDelegator)
	if got := errorCode(errR); got != "TASK_ALREADY_TERMINAL" {
		t.Errorf("got %q, want TASK_ALREADY_TERMINAL", got)
	}
}

// TestAuthorizeTaskCall_Executor_Mismatch — mismatched NIWA_TASK_ID yields NOT_TASK_PARTY.
func TestAuthorizeTaskCall_Executor_Mismatch(t *testing.T) {
	root := t.TempDir()
	id := writeTaskFixture(t, root, "coordinator", "web", TaskStateRunning)

	// Caller claims to be the worker but with a wrong task_id.
	_, _, errR := authorizeTaskCall(
		authIdentity{InstanceRoot: root, Role: "web", TaskID: "wrong"},
		id, kindExecutor)
	if got := errorCode(errR); got != "NOT_TASK_PARTY" {
		t.Errorf("got %q, want NOT_TASK_PARTY", got)
	}
}

// TestAuthorizeTaskCall_Executor_MatchingID_NoPPIDCheck — when worker.pid == 0 the Linux
// hardening step is skipped (pre-backfill window), so a caller with the
// correct task_id + role passes.
func TestAuthorizeTaskCall_Executor_MatchingID_NoPPIDCheck(t *testing.T) {
	root := t.TempDir()
	id := writeTaskFixture(t, root, "coordinator", "web", TaskStateRunning)
	// worker.pid = 0 from writeTaskFixture; PPID check skipped.

	_, _, errR := authorizeTaskCall(
		authIdentity{InstanceRoot: root, Role: "web", TaskID: id},
		id, kindExecutor)
	if errR != nil {
		t.Fatalf("unexpected error: %s", errR.Content[0].Text)
	}
}

// TestAuthorizeTaskCall_Party_EitherRolePasses — kindParty accepts both delegator and
// executor. Neither yields NOT_TASK_PARTY.
func TestAuthorizeTaskCall_Party_EitherRolePasses(t *testing.T) {
	root := t.TempDir()
	id := writeTaskFixture(t, root, "coordinator", "web", TaskStateRunning)

	// Delegator passes.
	_, _, errR := authorizeTaskCall(
		authIdentity{InstanceRoot: root, Role: "coordinator"},
		id, kindParty)
	if errR != nil {
		t.Errorf("delegator kindParty: unexpected error %s", errR.Content[0].Text)
	}

	// Executor passes (matching task_id + role, worker.pid=0 so PPID skipped).
	_, _, errR = authorizeTaskCall(
		authIdentity{InstanceRoot: root, Role: "web", TaskID: id},
		id, kindParty)
	if errR != nil {
		t.Errorf("executor kindParty: unexpected error %s", errR.Content[0].Text)
	}

	// Unrelated role fails.
	_, _, errR = authorizeTaskCall(
		authIdentity{InstanceRoot: root, Role: "stranger"},
		id, kindParty)
	if got := errorCode(errR); got != "NOT_TASK_PARTY" {
		t.Errorf("stranger got %q, want NOT_TASK_PARTY", got)
	}
}

// TestAuthorizeTaskCall_Party_AcceptsTerminal — kindParty accepts a completed task so
// niwa_query_task still returns results after completion.
func TestAuthorizeTaskCall_Party_AcceptsTerminal(t *testing.T) {
	root := t.TempDir()
	id := writeTaskFixture(t, root, "coordinator", "web", TaskStateCompleted)

	_, _, errR := authorizeTaskCall(
		authIdentity{InstanceRoot: root, Role: "coordinator"},
		id, kindParty)
	if errR != nil {
		t.Errorf("kindParty on terminal task: unexpected error %s", errR.Content[0].Text)
	}
}

// TestAuthorizeTaskCall_MalformedTaskID — fails closed with NOT_TASK_PARTY without a
// filesystem probe (no leak of which IDs exist).
func TestAuthorizeTaskCall_MalformedTaskID(t *testing.T) {
	root := t.TempDir()
	_, _, errR := authorizeTaskCall(
		authIdentity{InstanceRoot: root, Role: "coordinator"},
		"not-a-uuid", kindParty)
	if got := errorCode(errR); got != "NOT_TASK_PARTY" {
		t.Errorf("got %q, want NOT_TASK_PARTY", got)
	}
}

// TestAuthorizeTaskCall_MissingTask — a well-formed but non-existent task_id yields
// NOT_TASK_PARTY (not a distinguishable "not found" code).
func TestAuthorizeTaskCall_MissingTask(t *testing.T) {
	root := t.TempDir()
	_, _, errR := authorizeTaskCall(
		authIdentity{InstanceRoot: root, Role: "coordinator"},
		NewTaskID(), kindParty)
	if got := errorCode(errR); got != "NOT_TASK_PARTY" {
		t.Errorf("got %q, want NOT_TASK_PARTY", got)
	}
}

// TestPPIDChain_ReturnsParent — scenario-3 foundation. PPIDChain(1) returns
// a non-zero PID corresponding to the test's parent process.
func TestPPIDChain_ReturnsParent(t *testing.T) {
	chain, err := PPIDChain(1)
	if err != nil {
		t.Fatalf("PPIDChain(1): %v", err)
	}
	if len(chain) != 1 {
		t.Errorf("len = %d, want 1", len(chain))
	}
	if chain[0] != os.Getppid() {
		t.Errorf("chain[0] = %d, os.Getppid() = %d", chain[0], os.Getppid())
	}
}

// TestPPIDChain_NegativeN — n <= 0 is a programming error; must surface
// a structured error rather than return an empty chain.
func TestPPIDChain_NegativeN(t *testing.T) {
	if _, err := PPIDChain(0); err == nil {
		t.Error("PPIDChain(0) expected error, got nil")
	}
	if _, err := PPIDChain(-1); err == nil {
		t.Error("PPIDChain(-1) expected error, got nil")
	}
}

// TestAuthorizeTaskCall_Executor_PPIDStartTimeMismatch — when worker.pid/start_time are
// set but do NOT match the current process's parent, the executor check
// fails closed with NOT_TASK_PARTY. Uses a crafted state with impossible
// pid/start_time values so the PPIDChain(1) comparison diverges.
func TestAuthorizeTaskCall_Executor_PPIDStartTimeMismatch(t *testing.T) {
	root := t.TempDir()
	taskID := NewTaskID()
	dir := filepath.Join(root, ".niwa", "tasks", taskID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	env := TaskEnvelope{
		V: 1, ID: taskID,
		From:   TaskParty{Role: "coordinator"},
		To:     TaskParty{Role: "web"},
		Body:   json.RawMessage(`{}`),
		SentAt: time.Now().UTC().Format(time.RFC3339),
	}
	envBytes, _ := json.MarshalIndent(env, "", "  ")
	_ = os.WriteFile(filepath.Join(dir, envelopeFileName), envBytes, 0o600)

	// Deliberately craft a worker entry whose PID is certainly NOT our
	// actual parent; PID 2 (kthreadd on Linux) is system-level and will
	// never match os.Getppid() from a user-space test run.
	st := TaskState{
		V: 1, TaskID: taskID,
		State:            TaskStateRunning,
		StateTransitions: []StateTransition{{From: "", To: TaskStateRunning}},
		MaxRestarts:      3,
		Worker:           TaskWorker{PID: 2, StartTime: 12345, Role: "web"},
		DelegatorRole:    "coordinator",
		TargetRole:       "web",
		UpdatedAt:        env.SentAt,
	}
	stBytes, _ := json.MarshalIndent(st, "", "  ")
	_ = os.WriteFile(filepath.Join(dir, stateFileName), stBytes, 0o600)

	_, _, errR := authorizeTaskCall(
		authIdentity{InstanceRoot: root, Role: "web", TaskID: taskID},
		taskID, kindExecutor)
	if got := errorCode(errR); got != "NOT_TASK_PARTY" {
		t.Errorf("got %q, want NOT_TASK_PARTY (PPID mismatch should fail closed)", got)
	}
}
