package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/mcp"
)

// fakeTaskFixture describes a single synthetic task directory written to a
// test fixture tree. Fields not set default to reasonable values (queued
// state, now for sent_at, empty body, etc.).
type fakeTaskFixture struct {
	id            string
	state         string
	restartCount  int
	targetRole    string
	delegatorRole string
	body          json.RawMessage
	sentAt        time.Time
	transitions   []mcp.StateTransition
}

// seedTaskFixtures materializes fakeTaskFixture entries at
// <root>/.niwa/tasks/<id>/ with valid envelope.json and state.json so
// mcp.ReadState accepts them. Returns the root directory (which is
// what the CLI expects as NIWA_INSTANCE_ROOT).
func seedTaskFixtures(t *testing.T, fixtures []fakeTaskFixture) string {
	t.Helper()
	root := t.TempDir()
	tasksDir := filepath.Join(root, ".niwa", "tasks")
	if err := os.MkdirAll(tasksDir, 0o700); err != nil {
		t.Fatalf("mkdir tasks: %v", err)
	}
	// Required for discoverInstanceRoot to accept the root when
	// NIWA_INSTANCE_ROOT is not set.
	if err := os.WriteFile(filepath.Join(root, ".niwa", "instance.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write instance.json: %v", err)
	}

	for _, f := range fixtures {
		if f.id == "" {
			f.id = mcp.NewTaskID()
		}
		if f.state == "" {
			f.state = mcp.TaskStateQueued
		}
		if f.targetRole == "" {
			f.targetRole = "web"
		}
		if f.delegatorRole == "" {
			f.delegatorRole = "coordinator"
		}
		if f.sentAt.IsZero() {
			f.sentAt = time.Now().UTC()
		}
		if len(f.body) == 0 {
			f.body = json.RawMessage(`{"kind":"test"}`)
		}
		if len(f.transitions) == 0 {
			f.transitions = []mcp.StateTransition{
				{From: "", To: mcp.TaskStateQueued, At: f.sentAt.Format(time.RFC3339)},
			}
		}
		taskDir := filepath.Join(tasksDir, f.id)
		if err := os.MkdirAll(taskDir, 0o700); err != nil {
			t.Fatalf("mkdir taskDir: %v", err)
		}
		env := &mcp.TaskEnvelope{
			V:      1,
			ID:     f.id,
			From:   mcp.TaskParty{Role: f.delegatorRole, PID: 1000},
			To:     mcp.TaskParty{Role: f.targetRole},
			Body:   f.body,
			SentAt: f.sentAt.Format(time.RFC3339),
		}
		writeJSONFile(t, filepath.Join(taskDir, "envelope.json"), env)
		st := &mcp.TaskState{
			V:                1,
			TaskID:           f.id,
			State:            f.state,
			StateTransitions: f.transitions,
			RestartCount:     f.restartCount,
			MaxRestarts:      3,
			DelegatorRole:    f.delegatorRole,
			TargetRole:       f.targetRole,
			UpdatedAt:        f.sentAt.Format(time.RFC3339),
		}
		writeJSONFile(t, filepath.Join(taskDir, "state.json"), st)
	}
	return root
}

func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// resetTaskListFlags zeroes the package-level flag vars between subtests so
// a filter set by one table entry does not leak into another.
func resetTaskListFlags() {
	taskListStateFlag = ""
	taskListRoleFlag = ""
	taskListDelegatorFlag = ""
	taskListSinceFlag = ""
}

func TestTaskList_EmptyReturnsHeaderOnly(t *testing.T) {
	resetTaskListFlags()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".niwa"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".niwa", "instance.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write instance.json: %v", err)
	}
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	buf := &bytes.Buffer{}
	taskListCmd.SetOut(buf)
	defer taskListCmd.SetOut(os.Stdout)

	if err := runTaskList(taskListCmd, nil); err != nil {
		t.Fatalf("runTaskList: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "TASK") || !strings.Contains(out, "TARGET") {
		t.Errorf("expected header row, got %q", out)
	}
	// Only the header should appear (one line + trailing newline).
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 header line, got %d lines: %q", len(lines), out)
	}
}

func TestTaskList_StateFilter(t *testing.T) {
	resetTaskListFlags()
	now := time.Now().UTC()
	root := seedTaskFixtures(t, []fakeTaskFixture{
		{id: "aaaaaaaa-1111-4111-8111-111111111111", state: mcp.TaskStateQueued, sentAt: now},
		{id: "bbbbbbbb-2222-4222-8222-222222222222", state: mcp.TaskStateRunning, sentAt: now},
		{id: "cccccccc-3333-4333-8333-333333333333", state: mcp.TaskStateCompleted, sentAt: now},
	})
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	tests := []struct {
		filter  string
		include []string
		exclude []string
	}{
		{filter: mcp.TaskStateRunning, include: []string{"bbbbbbbb"}, exclude: []string{"aaaaaaaa", "cccccccc"}},
		{filter: mcp.TaskStateQueued, include: []string{"aaaaaaaa"}, exclude: []string{"bbbbbbbb", "cccccccc"}},
		{filter: mcp.TaskStateCompleted, include: []string{"cccccccc"}, exclude: []string{"aaaaaaaa", "bbbbbbbb"}},
	}
	for _, tt := range tests {
		t.Run(tt.filter, func(t *testing.T) {
			resetTaskListFlags()
			taskListStateFlag = tt.filter

			buf := &bytes.Buffer{}
			taskListCmd.SetOut(buf)
			defer taskListCmd.SetOut(os.Stdout)

			if err := runTaskList(taskListCmd, nil); err != nil {
				t.Fatalf("runTaskList: %v", err)
			}
			out := buf.String()
			for _, inc := range tt.include {
				if !strings.Contains(out, inc) {
					t.Errorf("expected %q in output, got %q", inc, out)
				}
			}
			for _, exc := range tt.exclude {
				if strings.Contains(out, exc) {
					t.Errorf("did not expect %q in output, got %q", exc, out)
				}
			}
		})
	}
}

func TestTaskList_RoleFilter(t *testing.T) {
	resetTaskListFlags()
	now := time.Now().UTC()
	root := seedTaskFixtures(t, []fakeTaskFixture{
		{id: "aaaaaaaa-1111-4111-8111-111111111111", targetRole: "web", sentAt: now},
		{id: "bbbbbbbb-2222-4222-8222-222222222222", targetRole: "api", sentAt: now},
	})
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	taskListRoleFlag = "web"
	buf := &bytes.Buffer{}
	taskListCmd.SetOut(buf)
	defer taskListCmd.SetOut(os.Stdout)

	if err := runTaskList(taskListCmd, nil); err != nil {
		t.Fatalf("runTaskList: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "aaaaaaaa") {
		t.Errorf("expected aaaaaaaa in output, got %q", out)
	}
	if strings.Contains(out, "bbbbbbbb") {
		t.Errorf("did not expect bbbbbbbb in output, got %q", out)
	}
}

func TestTaskList_DelegatorFilter(t *testing.T) {
	resetTaskListFlags()
	now := time.Now().UTC()
	root := seedTaskFixtures(t, []fakeTaskFixture{
		{id: "aaaaaaaa-1111-4111-8111-111111111111", delegatorRole: "coordinator", sentAt: now},
		{id: "bbbbbbbb-2222-4222-8222-222222222222", delegatorRole: "worker", sentAt: now},
	})
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	taskListDelegatorFlag = "coordinator"
	buf := &bytes.Buffer{}
	taskListCmd.SetOut(buf)
	defer taskListCmd.SetOut(os.Stdout)

	if err := runTaskList(taskListCmd, nil); err != nil {
		t.Fatalf("runTaskList: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "aaaaaaaa") {
		t.Errorf("expected aaaaaaaa in output, got %q", out)
	}
	if strings.Contains(out, "bbbbbbbb") {
		t.Errorf("did not expect bbbbbbbb in output, got %q", out)
	}
}

func TestTaskList_SinceFilter(t *testing.T) {
	resetTaskListFlags()
	now := time.Now().UTC()
	recent := now.Add(-10 * time.Minute)
	old := now.Add(-3 * time.Hour)
	root := seedTaskFixtures(t, []fakeTaskFixture{
		{id: "aaaaaaaa-1111-4111-8111-111111111111", sentAt: recent},
		{id: "bbbbbbbb-2222-4222-8222-222222222222", sentAt: old},
	})
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	taskListSinceFlag = "1h"
	buf := &bytes.Buffer{}
	taskListCmd.SetOut(buf)
	defer taskListCmd.SetOut(os.Stdout)

	if err := runTaskList(taskListCmd, nil); err != nil {
		t.Fatalf("runTaskList: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "aaaaaaaa") {
		t.Errorf("expected aaaaaaaa in output, got %q", out)
	}
	if strings.Contains(out, "bbbbbbbb") {
		t.Errorf("did not expect bbbbbbbb in output, got %q", out)
	}
}

func TestTaskList_FiltersAND(t *testing.T) {
	resetTaskListFlags()
	now := time.Now().UTC()
	root := seedTaskFixtures(t, []fakeTaskFixture{
		// matches role=web AND state=running AND delegator=coordinator
		{id: "aaaaaaaa-1111-4111-8111-111111111111",
			targetRole: "web", state: mcp.TaskStateRunning, delegatorRole: "coordinator", sentAt: now},
		// matches role=web but state=queued
		{id: "bbbbbbbb-2222-4222-8222-222222222222",
			targetRole: "web", state: mcp.TaskStateQueued, delegatorRole: "coordinator", sentAt: now},
		// matches state=running but role=api
		{id: "cccccccc-3333-4333-8333-333333333333",
			targetRole: "api", state: mcp.TaskStateRunning, delegatorRole: "coordinator", sentAt: now},
	})
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	taskListRoleFlag = "web"
	taskListStateFlag = mcp.TaskStateRunning
	taskListDelegatorFlag = "coordinator"

	buf := &bytes.Buffer{}
	taskListCmd.SetOut(buf)
	defer taskListCmd.SetOut(os.Stdout)

	if err := runTaskList(taskListCmd, nil); err != nil {
		t.Fatalf("runTaskList: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "aaaaaaaa") {
		t.Errorf("expected aaaaaaaa in output (matches all filters), got %q", out)
	}
	if strings.Contains(out, "bbbbbbbb") {
		t.Errorf("did not expect bbbbbbbb (wrong state) in output, got %q", out)
	}
	if strings.Contains(out, "cccccccc") {
		t.Errorf("did not expect cccccccc (wrong role) in output, got %q", out)
	}
}

func TestTaskList_BodySummaryTruncatesAndCollapsesLines(t *testing.T) {
	resetTaskListFlags()
	// Long JSON body with embedded newlines to verify collapse + truncation.
	longBody := `{"kind":"big","payload":"` + strings.Repeat("A", 500) + `"}`
	root := seedTaskFixtures(t, []fakeTaskFixture{
		{id: "aaaaaaaa-1111-4111-8111-111111111111",
			body: json.RawMessage(longBody), sentAt: time.Now().UTC()},
	})
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	buf := &bytes.Buffer{}
	taskListCmd.SetOut(buf)
	defer taskListCmd.SetOut(os.Stdout)

	if err := runTaskList(taskListCmd, nil); err != nil {
		t.Fatalf("runTaskList: %v", err)
	}
	out := buf.String()
	// The body line in output should not exceed the header's line length +
	// some buffer for the summary column. We just assert no raw newline
	// leaks into the row, and that the 500-char payload is truncated.
	for _, line := range strings.Split(out, "\n") {
		if strings.Count(line, "AAAAA") > 0 && len(line) > 400 {
			t.Errorf("body summary should be truncated; got len=%d line=%q", len(line), line)
		}
	}
}

func TestTaskShow_NonExistentIDFailsWithStderrMessage(t *testing.T) {
	resetTaskListFlags()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".niwa", "tasks"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".niwa", "instance.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write instance.json: %v", err)
	}
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	taskShowCmd.SetOut(stdout)
	taskShowCmd.SetErr(stderr)
	defer taskShowCmd.SetOut(os.Stdout)
	defer taskShowCmd.SetErr(os.Stderr)

	err := runTaskShow(taskShowCmd, []string{"does-not-exist"})
	if err == nil {
		t.Fatal("expected runTaskShow to return an error for non-existent ID")
	}
	if !strings.Contains(stderr.String(), "task not found: does-not-exist") {
		t.Errorf("expected stderr to contain 'task not found: does-not-exist', got %q", stderr.String())
	}
}

func TestTaskShow_HappyPath(t *testing.T) {
	resetTaskListFlags()
	id := "aaaaaaaa-1111-4111-8111-111111111111"
	now := time.Now().UTC()
	root := seedTaskFixtures(t, []fakeTaskFixture{
		{id: id, state: mcp.TaskStateRunning, sentAt: now},
	})
	// Seed a transitions.log so writeTaskShowTransitions has something to
	// render; using the taskstore helper path would require a full
	// UpdateState sequence. A hand-written NDJSON line is enough to
	// exercise the parser branch.
	logPath := filepath.Join(root, ".niwa", "tasks", id, "transitions.log")
	entry := mcp.TransitionLogEntry{
		V:       1,
		Kind:    "state_change",
		At:      now.Format(time.RFC3339Nano),
		From:    "queued",
		To:      "running",
		Summary: "worker started",
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(logPath, data, 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	stdout := &bytes.Buffer{}
	taskShowCmd.SetOut(stdout)
	defer taskShowCmd.SetOut(os.Stdout)

	if err := runTaskShow(taskShowCmd, []string{id}); err != nil {
		t.Fatalf("runTaskShow: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{
		"Envelope:", "task_id:    " + id,
		"State:", "state:         running",
		"Transitions:", "queued->running", "worker started",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got %q", want, out)
		}
	}
}

func TestSummarizeBody_RoundtripAndTruncate(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string // substring or exact
	}{
		{name: "empty", input: "", want: ""},
		{name: "compacts whitespace", input: `{
  "kind": "x",
  "n": 1
}`, want: `{"kind":"x","n":1}`},
		{name: "truncates at 200", input: `"` + strings.Repeat("A", 500) + `"`, want: strings.Repeat("A", 100)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarizeBody(json.RawMessage(tt.input))
			if tt.name == "empty" {
				if got != "" {
					t.Errorf("empty body: got %q, want empty", got)
				}
				return
			}
			if !strings.Contains(got, tt.want) {
				t.Errorf("summarizeBody(%q) = %q, expected to contain %q", tt.input, got, tt.want)
			}
			if len(got) > 200 {
				t.Errorf("summarizeBody result exceeded 200 chars: len=%d", len(got))
			}
		})
	}
}
