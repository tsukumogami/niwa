package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeSessionsJSON writes sessions.json for the test's instanceRoot.
func writeSessionsJSON(t *testing.T, instanceRoot string, sessions []SessionEntry) {
	t.Helper()
	sessionsDir := filepath.Join(instanceRoot, ".niwa", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	reg := SessionRegistry{Sessions: sessions}
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		t.Fatalf("marshal sessions: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionsDir, "sessions.json"), data, 0o600); err != nil {
		t.Fatalf("write sessions.json: %v", err)
	}
}

// listInboxFiles returns non-directory .json filenames in inboxDir.
func listInboxFiles(t *testing.T, inboxDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(inboxDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read inbox: %v", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	return names
}

// TestHandleAsk_LiveCoordinator_WritesTaskAsk verifies that handleAsk writes a
// task.ask notification to the coordinator's inbox and does NOT write a
// task.delegate when a live coordinator session is registered.
func TestHandleAsk_LiveCoordinator_WritesTaskAsk(t *testing.T) {
	s := newTestServer(t, "frontend", "coordinator")
	root := s.instanceRoot

	// Register a live coordinator in sessions.json.
	pid := os.Getpid()
	start, _ := PIDStartTime(pid)
	writeSessionsJSON(t, root, []SessionEntry{{
		ID:           "coord-session",
		Role:         "coordinator",
		PID:          pid,
		StartTime:    start,
		InboxDir:     filepath.Join(root, ".niwa", "roles", "coordinator", "inbox"),
		RegisteredAt: time.Now().UTC().Format(time.RFC3339),
	}})

	coordInbox := filepath.Join(root, ".niwa", "roles", "coordinator", "inbox")

	// Call handleAsk in a goroutine so we can check the inbox without waiting
	// for the timeout.
	done := make(chan toolResult, 1)
	go func() {
		done <- s.handleAsk(askArgs{
			To:             "coordinator",
			Body:           json.RawMessage(`{"text":"approve?"}`),
			TimeoutSeconds: 2,
		})
	}()

	// Wait briefly for handleAsk to write the notification.
	deadline := time.Now().Add(500 * time.Millisecond)
	var files []string
	for time.Now().Before(deadline) {
		files = listInboxFiles(t, coordInbox)
		if len(files) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if len(files) == 0 {
		t.Fatal("expected task.ask notification in coordinator inbox, found none")
	}

	// Read and validate the notification.
	data, err := os.ReadFile(filepath.Join(coordInbox, files[0]))
	if err != nil {
		t.Fatalf("read notification: %v", err)
	}
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal notification: %v", err)
	}
	if msg.Type != "task.ask" {
		t.Errorf("expected type=task.ask, got %q", msg.Type)
	}
	if msg.From.Role != "frontend" {
		t.Errorf("expected from.role=frontend, got %q", msg.From.Role)
	}
	if msg.To.Role != "coordinator" {
		t.Errorf("expected to.role=coordinator, got %q", msg.To.Role)
	}
	if msg.TaskID == "" {
		t.Error("expected non-empty task_id")
	}

	// Validate body fields.
	var body struct {
		AskTaskID string          `json:"ask_task_id"`
		FromRole  string          `json:"from_role"`
		NiwaNote  string          `json:"_niwa_note"`
		Question  json.RawMessage `json:"question"`
	}
	if err := json.Unmarshal(msg.Body, &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if body.AskTaskID == "" {
		t.Error("body.ask_task_id is empty")
	}
	if body.FromRole != "frontend" {
		t.Errorf("body.from_role=%q, want frontend", body.FromRole)
	}
	if body.NiwaNote == "" {
		t.Error("body._niwa_note is empty")
	}
	if string(body.Question) != `{"text":"approve?"}` {
		t.Errorf("body.question=%s, want {\"text\":\"approve?\"}", body.Question)
	}

	// Also verify that no task.delegate was written to the coordinator inbox.
	for _, f := range files {
		d, _ := os.ReadFile(filepath.Join(coordInbox, f))
		var m Message
		if err := json.Unmarshal(d, &m); err == nil && m.Type == "task.delegate" {
			t.Errorf("unexpected task.delegate found in coordinator inbox: %s", f)
		}
	}

	<-done // drain goroutine after timeout
}

// TestHandleAsk_DeadCoordinator_ReturnsNoLiveSession verifies that handleAsk
// returns no_live_session immediately (without writing any task directory) when
// the registered coordinator PID is dead, and that the stale entry is pruned.
func TestHandleAsk_DeadCoordinator_ReturnsNoLiveSession(t *testing.T) {
	s := newTestServer(t, "frontend", "coordinator")
	root := s.instanceRoot

	// Register a stale (dead PID) coordinator entry.
	writeSessionsJSON(t, root, []SessionEntry{{
		ID:           "coord-session-dead",
		Role:         "coordinator",
		PID:          deadPID,
		StartTime:    0,
		InboxDir:     filepath.Join(root, ".niwa", "roles", "coordinator", "inbox"),
		RegisteredAt: time.Now().UTC().Format(time.RFC3339),
	}})

	res := s.handleAsk(askArgs{
		To:             "coordinator",
		Body:           json.RawMessage(`{"text":"hello"}`),
		TimeoutSeconds: 2,
	})

	if res.IsError {
		t.Fatalf("expected non-error result, got error: %s", res.Content[0].Text)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, `"status":"no_live_session"`) {
		t.Errorf("want no_live_session status, got %s", text)
	}
	if !strings.Contains(text, `"role":"coordinator"`) {
		t.Errorf("want role=coordinator in response, got %s", text)
	}

	// No task directory must have been created.
	tasksDir := filepath.Join(root, ".niwa", "tasks")
	entries, _ := os.ReadDir(tasksDir)
	var subdirs []string
	for _, e := range entries {
		if e.IsDir() {
			subdirs = append(subdirs, e.Name())
		}
	}
	if len(subdirs) != 0 {
		t.Errorf("expected zero task subdirectories, found %v", subdirs)
	}

	// Verify the stale entry was pruned.
	sessionsPath := filepath.Join(root, ".niwa", "sessions", "sessions.json")
	regData, err := os.ReadFile(sessionsPath)
	if err != nil {
		t.Fatalf("read sessions.json: %v", err)
	}
	var reg SessionRegistry
	if err := json.Unmarshal(regData, &reg); err != nil {
		t.Fatalf("unmarshal sessions.json: %v", err)
	}
	for _, entry := range reg.Sessions {
		if entry.Role == "coordinator" && entry.PID == deadPID {
			t.Error("stale coordinator entry not pruned from sessions.json")
		}
	}
}

// TestHandleAsk_NoSessions_ReturnsNoLiveSession verifies that handleAsk returns
// no_live_session immediately (without writing any task directory) when
// sessions.json does not exist.
func TestHandleAsk_NoSessions_ReturnsNoLiveSession(t *testing.T) {
	s := newTestServer(t, "frontend", "coordinator")
	root := s.instanceRoot

	// No sessions.json created.
	res := s.handleAsk(askArgs{
		To:             "coordinator",
		Body:           json.RawMessage(`{"text":"hello"}`),
		TimeoutSeconds: 2,
	})

	if res.IsError {
		t.Fatalf("expected non-error result, got error: %s", res.Content[0].Text)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, `"status":"no_live_session"`) {
		t.Errorf("want no_live_session status, got %s", text)
	}

	// No task directory must have been created.
	tasksDir := filepath.Join(root, ".niwa", "tasks")
	entries, _ := os.ReadDir(tasksDir)
	var subdirs []string
	for _, e := range entries {
		if e.IsDir() {
			subdirs = append(subdirs, e.Name())
		}
	}
	if len(subdirs) != 0 {
		t.Errorf("expected zero task subdirectories, found %v", subdirs)
	}
}

// --- handleAsk coordinator routing fix tests (Issue 4) -----------------------

// TestHandleAsk_SessionWorktreeRoutesToMainInstance asserts that when
// mainInstanceRoot is set, a coordinator ask inside a session worktree reads
// sessions.json from the main instance root rather than the worktree root,
// and isKnownRole resolves the coordinator role against the main instance
// even though the worktree never carries a coordinator/ directory. This is
// the Issue 1 contract: roleRoot redirects coordinator targets to the main
// instance for both the role-existence check and the inbox write.
func TestHandleAsk_SessionWorktreeRoutesToMainInstance(t *testing.T) {
	// mainRoot is where the coordinator's sessions.json lives.
	mainRoot := t.TempDir()
	// worktreeRoot simulates the session worktree. NO coordinator/ directory
	// exists here, mirroring scaffoldWorktreeNiwa's actual layout.
	worktreeRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(worktreeRoot, ".niwa", "tasks"), 0o700); err != nil {
		t.Fatalf("mkdir worktree tasks: %v", err)
	}
	// Coordinator role + inbox in main root only — workers reach it via
	// roleRoot's mainInstanceRoot redirect for "coordinator".
	if err := os.MkdirAll(filepath.Join(mainRoot, ".niwa", "roles", "coordinator", "inbox"), 0o700); err != nil {
		t.Fatalf("mkdir main coordinator inbox: %v", err)
	}

	// Register a live coordinator in the main root's sessions.json.
	pid := os.Getpid()
	start, _ := PIDStartTime(pid)
	writeSessionsJSON(t, mainRoot, []SessionEntry{{
		ID:           "coord-session",
		Role:         "coordinator",
		PID:          pid,
		StartTime:    start,
		RegisteredAt: time.Now().UTC().Format(time.RFC3339),
	}})

	// Build a server simulating a worker inside a session worktree.
	s := &Server{
		instanceRoot:     worktreeRoot,
		mainInstanceRoot: mainRoot,
		role:             "frontend",
		seenFiles:        make(map[string]struct{}),
		waiters:          make(map[string]chan toolResult),
		awaitWaiters:     make(map[string]chan taskEvent),
		questionWaiters:  make(map[string]chan questionEvent),
		audit:            NewFileAuditSink(""),
	}
	s.roleInboxDir = filepath.Join(worktreeRoot, ".niwa", "roles", "frontend", "inbox")

	res := s.handleAsk(askArgs{
		To:             "coordinator",
		Body:           json.RawMessage(`{"question":"ready?"}`),
		TimeoutSeconds: 1,
	})
	// We expect a timeout (not no_live_session) because the coordinator was found.
	// The response will be timeout or completed — never no_live_session.
	if res.IsError {
		t.Fatalf("handleAsk error: %s", res.Content[0].Text)
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(res.Content[0].Text), &payload)
	if status, _ := payload["status"].(string); status == "no_live_session" {
		t.Error("handleAsk returned no_live_session; expected coordinator to be found via mainInstanceRoot")
	}

	// Verify the ask notification landed in the main root's coordinator inbox.
	mainCoordInbox := filepath.Join(mainRoot, ".niwa", "roles", "coordinator", "inbox")
	files := listInboxFiles(t, mainCoordInbox)
	if len(files) == 0 {
		t.Error("no ask notification written to main coordinator inbox")
	}
}

// TestSendMessage_SessionWorktreeRoutesToMainInstance asserts the Issue 1
// roleRoot redirect for niwa_send_message: a session worker sending a message
// to the coordinator role writes the message into the main instance's
// coordinator inbox, not into a non-existent worktree-side inbox.
func TestSendMessage_SessionWorktreeRoutesToMainInstance(t *testing.T) {
	mainRoot := t.TempDir()
	worktreeRoot := t.TempDir()
	// No worktree-side coordinator dir — that's the Issue 1 contract.
	if err := os.MkdirAll(filepath.Join(mainRoot, ".niwa", "roles", "coordinator", "inbox"), 0o700); err != nil {
		t.Fatalf("mkdir main coordinator inbox: %v", err)
	}

	s := &Server{
		instanceRoot:     worktreeRoot,
		mainInstanceRoot: mainRoot,
		role:             "frontend",
		seenFiles:        make(map[string]struct{}),
		waiters:          make(map[string]chan toolResult),
		awaitWaiters:     make(map[string]chan taskEvent),
		questionWaiters:  make(map[string]chan questionEvent),
		audit:            NewFileAuditSink(""),
	}

	_, errR := s.sendMessage(sendMessageArgs{
		To:   "coordinator",
		Type: "status.update",
		Body: json.RawMessage(`{"note":"hello"}`),
	})
	if errR.IsError {
		t.Fatalf("sendMessage error: %s", errR.Content[0].Text)
	}

	mainCoordInbox := filepath.Join(mainRoot, ".niwa", "roles", "coordinator", "inbox")
	files := listInboxFiles(t, mainCoordInbox)
	if len(files) != 1 {
		t.Errorf("main coordinator inbox: got %d files, want 1", len(files))
	}

	// And the worktree-side coordinator inbox must NOT have been created.
	worktreeCoordInbox := filepath.Join(worktreeRoot, ".niwa", "roles", "coordinator")
	if _, err := os.Stat(worktreeCoordInbox); err == nil {
		t.Errorf("unexpected worktree-side coordinator dir at %s — roleRoot must not write there", worktreeCoordInbox)
	}
}

// TestHandleAsk_NonSessionNoMainInstanceRoot asserts that when mainInstanceRoot
// is empty (non-session worker), the coordinator lookup uses instanceRoot as before.
func TestHandleAsk_NonSessionNoMainInstanceRoot(t *testing.T) {
	s := newTestServer(t, "frontend", "coordinator")
	// No sessions.json in s.instanceRoot → no_live_session expected.
	res := s.handleAsk(askArgs{
		To:             "coordinator",
		Body:           json.RawMessage(`{"question":"ready?"}`),
		TimeoutSeconds: 1,
	})
	if res.IsError {
		t.Fatalf("handleAsk error: %s", res.Content[0].Text)
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(res.Content[0].Text), &payload)
	if status, _ := payload["status"].(string); status != "no_live_session" {
		t.Errorf("status = %q, want no_live_session (no coordinator registered)", status)
	}
}

// TestHandleFinishTask_AsksAnswerRoutesToSessionWorktreeInbox asserts the
// structural fix for cross-process ask wakeup. Before this fix, when a
// coordinator answered an ask task whose asker lived in a session worktree,
// sendTaskMessage would write task.completed to
// <coordinatorTaskStoreRoot>/.niwa/roles/<askerRole>/inbox/ — i.e. the main
// instance — while the asker's fsnotify watcher was rooted at
// <worktree>/.niwa/roles/<askerRole>/inbox/. The mismatch meant the
// in-memory awaitWaiter never fired and the asker had to time out.
//
// The fix: createAskTaskStore stamps state.SessionID with the asker's
// NIWA_SESSION_ID, and handleFinishTask routes via sendTaskMessageInSession
// when that field is set. This test seeds an ask task with state.SessionID
// pointed at a registered session lifecycle entry, calls handleFinishTask,
// and asserts the answer message landed in the worktree inbox (not the
// main-instance inbox).
//
// AC-S4a in test/functional/features/mesh.feature is the integration-level
// guard. This unit test isolates the routing decision so a regression
// surfaces immediately without spinning up a session daemon.
func TestHandleFinishTask_AsksAnswerRoutesToSessionWorktreeInbox(t *testing.T) {
	mainRoot := t.TempDir()
	worktreeRoot := filepath.Join(mainRoot, ".niwa", "worktrees", "app-deadbeef")
	if err := os.MkdirAll(filepath.Join(worktreeRoot, ".niwa", "roles", "frontend", "inbox"), 0o700); err != nil {
		t.Fatalf("mkdir worktree role inbox: %v", err)
	}
	// The main-instance role inbox exists too (channels create it for both
	// the worktree and the main instance). The test asserts the answer does
	// NOT land here even though the path is reachable.
	if err := os.MkdirAll(filepath.Join(mainRoot, ".niwa", "roles", "frontend", "inbox"), 0o700); err != nil {
		t.Fatalf("mkdir main role inbox: %v", err)
	}

	// Register a session lifecycle entry pointing at the worktree.
	sessionsDir := filepath.Join(mainRoot, ".niwa", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatalf("mkdir sessions dir: %v", err)
	}
	sessionID := "deadbeef"
	sessionState := SessionLifecycleState{
		V:            1,
		SessionID:    sessionID,
		Repo:         "app",
		Status:       SessionStatusActive,
		WorktreePath: worktreeRoot,
		CreationTime: time.Now().UTC().Format(time.RFC3339),
	}
	if err := WriteSessionLifecycleState(sessionsDir, sessionState); err != nil {
		t.Fatalf("write session state: %v", err)
	}

	// Seed an ask task in main-instance tasks dir with state.SessionID set
	// to the asker's session. createAskTaskStore would do this in production;
	// the test writes it directly to keep the test isolated from handleAsk.
	taskID := NewTaskID()
	taskDir := taskDirPath(mainRoot, taskID)
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		t.Fatalf("mkdir task dir: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	env := TaskEnvelope{
		V:      1,
		ID:     taskID,
		From:   TaskParty{Role: "frontend", PID: os.Getpid()},
		To:     TaskParty{Role: "coordinator"},
		Body:   json.RawMessage(`{"question":"go?"}`),
		SentAt: now,
	}
	envBytes, _ := json.MarshalIndent(env, "", "  ")
	if err := os.WriteFile(filepath.Join(taskDir, envelopeFileName), envBytes, 0o600); err != nil {
		t.Fatalf("write envelope: %v", err)
	}
	st := &TaskState{
		V:      1,
		TaskID: taskID,
		State:  TaskStateQueued,
		StateTransitions: []StateTransition{
			{From: "", To: TaskStateQueued, At: now},
		},
		DelegatorRole: "frontend",
		TargetRole:    "coordinator",
		SessionID:     sessionID,
		UpdatedAt:     now,
	}
	stBytes, _ := json.MarshalIndent(st, "", "  ")
	if err := os.WriteFile(filepath.Join(taskDir, stateFileName), stBytes, 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	// Simulate the coordinator finishing the ask task. The coordinator runs
	// at the main instance: instanceRoot = mainRoot, mainInstanceRoot = "".
	coordinator := &Server{
		instanceRoot:    mainRoot,
		role:            "coordinator",
		seenFiles:       make(map[string]struct{}),
		waiters:         make(map[string]chan toolResult),
		awaitWaiters:    make(map[string]chan taskEvent),
		questionWaiters: make(map[string]chan questionEvent),
		audit:           NewFileAuditSink(""),
	}

	res := coordinator.handleFinishTask(finishTaskArgs{
		TaskID:  taskID,
		Outcome: TaskStateCompleted,
		Result:  json.RawMessage(`{"answer":"go"}`),
	})
	if res.IsError {
		t.Fatalf("handleFinishTask returned error: %s", res.Content[0].Text)
	}

	// The answer must land in the WORKTREE inbox, not the main-instance inbox.
	worktreeInbox := filepath.Join(worktreeRoot, ".niwa", "roles", "frontend", "inbox")
	mainInbox := filepath.Join(mainRoot, ".niwa", "roles", "frontend", "inbox")

	worktreeFiles := listInboxFiles(t, worktreeInbox)
	mainFiles := listInboxFiles(t, mainInbox)

	if len(worktreeFiles) != 1 {
		t.Errorf("worktree inbox: got %d files, want 1 — structural routing did not deliver", len(worktreeFiles))
	}
	if len(mainFiles) != 0 {
		t.Errorf("main-instance inbox: got %d files, want 0 — answer leaked to the wrong inbox", len(mainFiles))
	}

	// Verify the message body shape — task.completed with the asker's task id.
	if len(worktreeFiles) > 0 {
		data, err := os.ReadFile(filepath.Join(worktreeInbox, worktreeFiles[0]))
		if err != nil {
			t.Fatalf("read worktree message: %v", err)
		}
		var msg struct {
			Type   string `json:"type"`
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatalf("unmarshal message: %v", err)
		}
		if msg.Type != "task.completed" {
			t.Errorf("message type = %q, want task.completed", msg.Type)
		}
		if msg.TaskID != taskID {
			t.Errorf("message task_id = %q, want %q", msg.TaskID, taskID)
		}
	}
}

// TestHandleFinishTask_NonSessionAskRoutesToMainInstanceInbox asserts the
// fallback path: when state.SessionID is empty (asker is not in a session),
// the answer routes via sendTaskMessage to taskStoreRoot()/.niwa/roles/...
// — i.e. the main-instance inbox — preserving the pre-fix behavior for
// non-session askers.
func TestHandleFinishTask_NonSessionAskRoutesToMainInstanceInbox(t *testing.T) {
	mainRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(mainRoot, ".niwa", "roles", "operator", "inbox"), 0o700); err != nil {
		t.Fatalf("mkdir operator inbox: %v", err)
	}

	taskID := NewTaskID()
	taskDir := taskDirPath(mainRoot, taskID)
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		t.Fatalf("mkdir task dir: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	env := TaskEnvelope{
		V:    1,
		ID:   taskID,
		From: TaskParty{Role: "operator", PID: os.Getpid()},
		To:   TaskParty{Role: "coordinator"},
		Body: json.RawMessage(`{"question":"go?"}`),
	}
	envBytes, _ := json.MarshalIndent(env, "", "  ")
	if err := os.WriteFile(filepath.Join(taskDir, envelopeFileName), envBytes, 0o600); err != nil {
		t.Fatalf("write envelope: %v", err)
	}
	st := &TaskState{
		V:                1,
		TaskID:           taskID,
		State:            TaskStateQueued,
		StateTransitions: []StateTransition{{From: "", To: TaskStateQueued, At: now}},
		DelegatorRole:    "operator",
		TargetRole:       "coordinator",
		// SessionID intentionally empty — non-session asker.
		UpdatedAt: now,
	}
	stBytes, _ := json.MarshalIndent(st, "", "  ")
	if err := os.WriteFile(filepath.Join(taskDir, stateFileName), stBytes, 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	coordinator := &Server{
		instanceRoot:    mainRoot,
		role:            "coordinator",
		seenFiles:       make(map[string]struct{}),
		waiters:         make(map[string]chan toolResult),
		awaitWaiters:    make(map[string]chan taskEvent),
		questionWaiters: make(map[string]chan questionEvent),
		audit:           NewFileAuditSink(""),
	}

	res := coordinator.handleFinishTask(finishTaskArgs{
		TaskID:  taskID,
		Outcome: TaskStateCompleted,
		Result:  json.RawMessage(`{"answer":"go"}`),
	})
	if res.IsError {
		t.Fatalf("handleFinishTask returned error: %s", res.Content[0].Text)
	}

	mainInbox := filepath.Join(mainRoot, ".niwa", "roles", "operator", "inbox")
	files := listInboxFiles(t, mainInbox)
	if len(files) != 1 {
		t.Errorf("main-instance inbox: got %d files, want 1 — non-session routing broke", len(files))
	}
}

// TestHandleAsk_NonCoordinatorTargetUnaffectedByMainInstanceRoot asserts that
// for non-coordinator targets, mainInstanceRoot does not change isKnownRole
// behavior.
func TestHandleAsk_NonCoordinatorTargetUnaffectedByMainInstanceRoot(t *testing.T) {
	s := newTestServer(t, "frontend", "backend")
	s.mainInstanceRoot = t.TempDir() // non-empty but irrelevant for non-coordinator

	res := s.handleAsk(askArgs{
		To:             "backend",
		Body:           json.RawMessage(`{"question":"go?"}`),
		TimeoutSeconds: 1,
	})
	// backend has no live session, so expect no_live_session — not UNKNOWN_ROLE,
	// because the role IS registered, just no coordinator session.
	if res.IsError {
		t.Fatalf("handleAsk error: %s", res.Content[0].Text)
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(res.Content[0].Text), &payload)
	if status, _ := payload["status"].(string); status != "no_live_session" {
		t.Errorf("status = %q, want no_live_session", status)
	}
}
