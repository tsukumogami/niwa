package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/mcp"
)

// writeReportProgressFixture creates a minimal task dir with envelope.json and
// state.json. The Worker.Role is set to targetRole so ownership checks pass by
// default.
func writeReportProgressFixture(t *testing.T, instanceRoot, targetRole, state string) string {
	t.Helper()
	taskID := mcp.NewTaskID()
	dir := filepath.Join(instanceRoot, ".niwa", "tasks", taskID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)

	env := mcp.TaskEnvelope{
		V:      1,
		ID:     taskID,
		From:   mcp.TaskParty{Role: "coordinator", PID: 1000},
		To:     mcp.TaskParty{Role: targetRole},
		Body:   json.RawMessage(`{}`),
		SentAt: now,
	}
	envBytes, _ := json.MarshalIndent(env, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "envelope.json"), envBytes, 0o600); err != nil {
		t.Fatalf("write envelope: %v", err)
	}

	st := mcp.TaskState{
		V:                1,
		TaskID:           taskID,
		State:            state,
		StateTransitions: []mcp.StateTransition{{From: "", To: mcp.TaskStateQueued, At: now}},
		MaxRestarts:      3,
		DelegatorRole:    "coordinator",
		TargetRole:       targetRole,
		Worker:           mcp.TaskWorker{Role: targetRole},
		UpdatedAt:        now,
	}
	stBytes, _ := json.MarshalIndent(st, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "state.json"), stBytes, 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	return taskID
}

func TestMeshReportProgress_NoopWhenTaskIDUnset(t *testing.T) {
	t.Setenv("NIWA_TASK_ID", "")
	t.Setenv("NIWA_SESSION_ROLE", "web")
	t.Setenv("NIWA_INSTANCE_ROOT", t.TempDir())

	if err := runMeshReportProgress(nil, nil); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestMeshReportProgress_OwnershipCheckPass(t *testing.T) {
	root := t.TempDir()
	taskID := writeReportProgressFixture(t, root, "web", mcp.TaskStateRunning)

	t.Setenv("NIWA_TASK_ID", taskID)
	t.Setenv("NIWA_SESSION_ROLE", "web")
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	if err := runMeshReportProgress(nil, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	taskDir := filepath.Join(root, ".niwa", "tasks", taskID)
	_, st, err := mcp.ReadState(taskDir)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.LastProgress == nil {
		t.Fatal("expected last_progress to be set")
	}
	if st.LastProgress.At == "" {
		t.Error("expected last_progress.at to be non-empty")
	}
}

func TestMeshReportProgress_OwnershipCheckFail_Role(t *testing.T) {
	root := t.TempDir()
	taskID := writeReportProgressFixture(t, root, "web", mcp.TaskStateRunning)

	t.Setenv("NIWA_TASK_ID", taskID)
	t.Setenv("NIWA_SESSION_ROLE", "other-role") // mismatch
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	err := runMeshReportProgress(nil, nil)
	if err == nil {
		t.Fatal("expected error for role mismatch, got nil")
	}
}

func TestMeshReportProgress_TerminalTaskExitsZero(t *testing.T) {
	root := t.TempDir()
	taskID := writeReportProgressFixture(t, root, "web", mcp.TaskStateCompleted)

	t.Setenv("NIWA_TASK_ID", taskID)
	t.Setenv("NIWA_SESSION_ROLE", "web")
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	if err := runMeshReportProgress(nil, nil); err != nil {
		t.Fatalf("expected nil for terminal task, got %v", err)
	}
}

func TestMeshReportProgress_InfrastructureError(t *testing.T) {
	root := t.TempDir()

	t.Setenv("NIWA_TASK_ID", "00000000-0000-4000-8000-000000000001")
	t.Setenv("NIWA_SESSION_ROLE", "web")
	t.Setenv("NIWA_INSTANCE_ROOT", root)
	// Task dir does not exist — UpdateState will fail to open the lock file.

	err := runMeshReportProgress(nil, nil)
	if err == nil {
		t.Fatal("expected error for non-existent task dir, got nil")
	}
}

func TestMeshReportProgress_PreservesExistingSummary(t *testing.T) {
	root := t.TempDir()
	taskID := writeReportProgressFixture(t, root, "web", mcp.TaskStateRunning)

	// Pre-populate LastProgress with a summary.
	taskDir := filepath.Join(root, ".niwa", "tasks", taskID)
	_, st, err := mcp.ReadState(taskDir)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	existingSummary := "doing something important"
	if err := mcp.UpdateState(taskDir, func(cur *mcp.TaskState) (*mcp.TaskState, *mcp.TransitionLogEntry, error) {
		next := *cur
		next.LastProgress = &mcp.TaskProgress{Summary: existingSummary, At: time.Now().UTC().Format(time.RFC3339)}
		return &next, nil, nil
	}); err != nil {
		t.Fatalf("pre-populate: %v", err)
	}
	_ = st

	t.Setenv("NIWA_TASK_ID", taskID)
	t.Setenv("NIWA_SESSION_ROLE", "web")
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	if err := runMeshReportProgress(nil, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, after, err := mcp.ReadState(taskDir)
	if err != nil {
		t.Fatalf("ReadState after: %v", err)
	}
	if after.LastProgress == nil {
		t.Fatal("expected last_progress to be set")
	}
	if after.LastProgress.Summary != existingSummary {
		t.Errorf("summary = %q, want %q", after.LastProgress.Summary, existingSummary)
	}
}
