package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// registerQuestionWaiter is a test helper that inserts a buffered channel into
// s.questionWaiters[role] and returns the channel plus a cleanup func.
func registerQuestionWaiter(s *Server, role string) (chan questionEvent, func()) {
	ch := make(chan questionEvent, 1)
	s.waitersMu.Lock()
	s.questionWaiters[role] = ch
	s.waitersMu.Unlock()
	return ch, func() {
		s.waitersMu.Lock()
		delete(s.questionWaiters, role)
		s.waitersMu.Unlock()
	}
}

// writeTaskAskFile writes a task.ask Message file to dir and returns its path
// and the file base name. taskID is set on the message as TaskID.
func writeTaskAskFile(t *testing.T, dir string, fromRole, toRole, taskID string) (path, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	msgID := newUUID()
	body := json.RawMessage(`{"ask_task_id":"` + taskID + `","from_role":"` + fromRole + `","question":{"text":"ok?"}}`)
	msg := Message{
		V:      1,
		ID:     msgID,
		Type:   "task.ask",
		TaskID: taskID,
		From:   MessageFrom{Role: fromRole},
		To:     MessageTo{Role: toRole},
		SentAt: time.Now().UTC().Format(time.RFC3339),
		Body:   body,
	}
	if errTR := writeMessageAtomic(dir, msgID, msg); errTR.IsError {
		t.Fatalf("writeMessageAtomic: %s", errTR.Content[0].Text)
	}
	name = msgID + ".json"
	path = filepath.Join(dir, name)
	return path, name
}

// TestNotifyNewFile_TaskAskDispatchesToQuestionWaiter verifies that
// notifyNewFile detects type == "task.ask", dispatches a questionEvent to the
// registered questionWaiters[to.role] channel, and moves the file to
// inbox/read/ only after the send succeeds.
func TestNotifyNewFile_TaskAskDispatchesToQuestionWaiter(t *testing.T) {
	s := newTestServer(t, "coordinator")
	inboxDir := filepath.Join(s.instanceRoot, ".niwa", "roles", "coordinator", "inbox")

	taskID := NewTaskID()
	path, name := writeTaskAskFile(t, inboxDir, "web", "coordinator", taskID)

	ch, cancel := registerQuestionWaiter(s, "coordinator")
	defer cancel()

	s.notifyNewFile(path, name)

	// Channel must receive the questionEvent immediately (buffered, size 1).
	select {
	case evt := <-ch:
		if evt.AskTaskID != taskID {
			t.Errorf("evt.AskTaskID = %q, want %q", evt.AskTaskID, taskID)
		}
		if evt.FromRole != "web" {
			t.Errorf("evt.FromRole = %q, want web", evt.FromRole)
		}
	case <-time.After(time.Second):
		t.Fatal("questionWaiter did not receive questionEvent")
	}

	// File must have been moved to inbox/read/.
	readDir := filepath.Join(inboxDir, "read")
	if _, err := os.Stat(filepath.Join(readDir, name)); err != nil {
		t.Errorf("file not in inbox/read/: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("original file still present in inbox after successful send")
	}
}

// TestNotifyNewFile_TaskAskNoWaiterLeavesFile verifies that a task.ask file is
// left in the inbox (not moved) when no questionWaiter is registered for the
// target role.
func TestNotifyNewFile_TaskAskNoWaiterLeavesFile(t *testing.T) {
	s := newTestServer(t, "coordinator")
	inboxDir := filepath.Join(s.instanceRoot, ".niwa", "roles", "coordinator", "inbox")

	taskID := NewTaskID()
	path, name := writeTaskAskFile(t, inboxDir, "web", "coordinator", taskID)

	// No waiter registered.
	s.notifyNewFile(path, name)

	// File must still be in the inbox.
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file removed from inbox even though no waiter was registered: %v", err)
	}

	// read/ directory must not contain the file.
	readPath := filepath.Join(inboxDir, "read", name)
	if _, err := os.Stat(readPath); err == nil {
		t.Error("file appeared in inbox/read/ despite no waiter")
	}
}

// TestNotifyNewFile_TerminalDeferredMoveToRead is a regression test for the
// deferred-move-to-read fix. It verifies that a task.completed file is moved
// to inbox/read/ ONLY after the awaitWaiter channel receives the event.
// Previously the file was moved before the send, which could permanently drop
// events when the channel was full.
func TestNotifyNewFile_TerminalDeferredMoveToRead(t *testing.T) {
	s := newTestServer(t, "coordinator")
	taskID := NewTaskID()

	inboxDir := filepath.Join(s.instanceRoot, ".niwa", "roles", "coordinator", "inbox")
	_ = os.MkdirAll(inboxDir, 0o700)

	msg := Message{
		V:      1,
		ID:     newUUID(),
		Type:   "task.completed",
		TaskID: taskID,
		From:   MessageFrom{Role: "web"},
		To:     MessageTo{Role: "coordinator"},
		SentAt: time.Now().UTC().Format(time.RFC3339),
		Body:   json.RawMessage(`{"task_id":"` + taskID + `","result":{"ok":true}}`),
	}
	if errTR := writeMessageAtomic(inboxDir, msg.ID, msg); errTR.IsError {
		t.Fatalf("seed: %s", errTR.Content[0].Text)
	}
	path := filepath.Join(inboxDir, msg.ID+".json")
	name := msg.ID + ".json"

	// Register an awaiter and immediately drain it in a goroutine, so the
	// channel is empty when notifyNewFile sends.
	ch, cancel := s.registerAwaitWaiter(taskID)
	defer cancel()

	received := make(chan taskEvent, 1)
	go func() {
		select {
		case evt := <-ch:
			received <- evt
		case <-time.After(2 * time.Second):
		}
	}()

	s.notifyNewFile(path, name)

	select {
	case evt := <-received:
		if evt.Kind != EvtCompleted || evt.TaskID != taskID {
			t.Errorf("evt = %+v, want kind=completed task=%s", evt, taskID)
		}
	case <-time.After(time.Second):
		t.Fatal("awaitWaiter did not receive task.completed event")
	}

	// File must be in inbox/read/ after the successful send.
	readDir := filepath.Join(inboxDir, "read")
	if _, err := os.Stat(filepath.Join(readDir, name)); err != nil {
		t.Errorf("file not in inbox/read/ after successful send: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("original file still in inbox after send")
	}
}
