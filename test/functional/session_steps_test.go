package functional

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tsukumogami/niwa/internal/mcp"
)

// session_steps_test.go holds step definitions for niwa_create_session and
// niwa_destroy_session functional tests (Issue #97).

// callMCPToolWithDaemonEnv is identical to callMCPToolAsRole except it does
// NOT strip the fake-worker and daemon-spawn env vars
// (NIWA_WORKER_SPAWN_COMMAND, NIWA_FAKE_SCENARIO, NIWA_FAKE_TEST_BINARY,
// NIWA_FAKE_CLAUDE_SESSION_ID). Use this when calling tools that spawn a
// daemon (e.g. niwa_create_session) so the daemon inherits the test env.
func callMCPToolWithDaemonEnv(s *testState, instanceRoot, role, taskID, toolName, argsJSON string) (string, error) {
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0.0.1"}}}` + "\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"` + toolName + `","arguments":` + argsJSON + `}}` + "\n"

	env := s.buildEnv()
	envMap := make(map[string]string)
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			envMap[kv[:i]] = kv[i+1:]
		}
	}
	envMap["NIWA_INSTANCE_ROOT"] = instanceRoot
	envMap["NIWA_SESSION_ROLE"] = role
	if taskID != "" {
		envMap["NIWA_TASK_ID"] = taskID
	} else {
		delete(envMap, "NIWA_TASK_ID")
	}
	// Intentionally keep NIWA_WORKER_SPAWN_COMMAND, NIWA_FAKE_SCENARIO,
	// NIWA_FAKE_TEST_BINARY, and NIWA_FAKE_CLAUDE_SESSION_ID so the daemon
	// spawned by this call inherits the fake-worker environment.

	envSlice := make([]string, 0, len(envMap))
	for k, v := range envMap {
		envSlice = append(envSlice, k+"="+v)
	}

	cmd := exec.Command(s.binPath, "mcp-serve")
	cmd.Env = envSlice
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return "", fmt.Errorf("mcp-serve failed: %w\nstderr: %s", err, stderr.String())
		}
	}
	return stdout.String(), nil
}

// iCallCreateSession calls niwa_create_session via the MCP interface and
// stores the session_id and worktree_path in the scenario state.
func iCallCreateSession(ctx context.Context, repo, purpose, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	args := fmt.Sprintf(`{"repo":%q,"purpose":%q}`, repo, purpose)
	out, err := callMCPToolWithDaemonEnv(s, instRoot, "coordinator", "", "niwa_create_session", args)
	if err != nil {
		return ctx, fmt.Errorf("callMCPToolWithDaemonEnv niwa_create_session: %w", err)
	}
	// Extract the content text from the MCP response JSON.
	payload, extractErr := extractMCPContentText(out)
	if extractErr != nil {
		return ctx, fmt.Errorf("extracting content from create_session response: %w\nraw: %s", extractErr, out)
	}
	var resp map[string]string
	if err := json.Unmarshal([]byte(payload), &resp); err != nil {
		return ctx, fmt.Errorf("parsing create_session payload %q: %w", payload, err)
	}
	if resp["session_id"] == "" {
		return ctx, fmt.Errorf("niwa_create_session returned empty session_id; payload: %s", payload)
	}
	s.lastSessionID = resp["session_id"]
	s.lastSessionWorktreePath = resp["worktree_path"]
	return ctx, nil
}

// iCallDestroySession calls niwa_destroy_session via the MCP interface using
// the session_id stored by the previous iCallCreateSession step.
func iCallDestroySession(ctx context.Context, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.lastSessionID == "" {
		return ctx, fmt.Errorf("no session_id stored; call niwa_create_session first")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	args := fmt.Sprintf(`{"session_id":%q,"force":true}`, s.lastSessionID)
	out, err := callMCPToolAsRole(s, instRoot, "coordinator", "", "niwa_destroy_session", args)
	if err != nil {
		return ctx, fmt.Errorf("callMCPToolAsRole niwa_destroy_session: %w", err)
	}
	// Verify the response is not an error.
	if isErrorResult(out) {
		return ctx, fmt.Errorf("niwa_destroy_session returned error; raw: %s", out)
	}
	return ctx, nil
}

// theSessionIsActiveInInstance asserts the session state file has status="active".
func theSessionIsActiveInInstance(ctx context.Context, instance string) error {
	return assertSessionStatus(ctx, instance, mcp.SessionStatusActive)
}

// theSessionIsEndedInInstance asserts the session state file has status="ended".
func theSessionIsEndedInInstance(ctx context.Context, instance string) error {
	return assertSessionStatus(ctx, instance, mcp.SessionStatusEnded)
}

func assertSessionStatus(ctx context.Context, instance string, want string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionID == "" {
		return fmt.Errorf("no session_id stored")
	}
	sessionsDir := filepath.Join(s.workspaceRoot, instance, ".niwa", "sessions")
	state, err := mcp.ReadSessionLifecycleState(sessionsDir, s.lastSessionID)
	if err != nil {
		return fmt.Errorf("ReadSessionLifecycleState: %w", err)
	}
	if state.Status != want {
		return fmt.Errorf("session status = %q, want %q", state.Status, want)
	}
	return nil
}

// theSessionWorktreeExistsInInstance asserts the worktree directory was created.
func theSessionWorktreeExistsInInstance(ctx context.Context, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionWorktreePath == "" {
		return fmt.Errorf("no worktree path stored")
	}
	if fi, err := os.Stat(s.lastSessionWorktreePath); err != nil {
		return fmt.Errorf("session worktree missing at %s: %w", s.lastSessionWorktreePath, err)
	} else if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", s.lastSessionWorktreePath)
	}
	return nil
}

// theSessionScaffoldDirExistsInWorktree asserts that relPath exists inside the
// session's worktree directory.
func theSessionScaffoldDirExistsInWorktree(ctx context.Context, relPath string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionWorktreePath == "" {
		return fmt.Errorf("no worktree path stored")
	}
	full := filepath.Join(s.lastSessionWorktreePath, relPath)
	if fi, err := os.Stat(full); err != nil {
		return fmt.Errorf("scaffold dir %q missing: %w", relPath, err)
	} else if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", full)
	}
	return nil
}

// extractMCPContentText pulls the text content from a raw MCP JSON-RPC
// response stream. The tools/call response embeds a content array whose
// first element has a "text" field.
func extractMCPContentText(raw string) (string, error) {
	// Find the tools/call result line (id:2).
	for _, line := range splitLines(raw) {
		var msg struct {
			ID     int `json:"id"`
			Result struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		if msg.ID == 2 && len(msg.Result.Content) > 0 {
			return msg.Result.Content[0].Text, nil
		}
	}
	// Fall back: try to match content[0].text via regex for escaped strings.
	re := regexp.MustCompile(`"text"\s*:\s*"((?:[^"\\]|\\.)*)"`)
	m := re.FindStringSubmatch(raw)
	if m == nil {
		return "", fmt.Errorf("no content text found in response")
	}
	// Unescape the JSON string value.
	var text string
	if err := json.Unmarshal([]byte(`"` + m[1] + `"`), &text); err != nil {
		return m[1], nil
	}
	return text, nil
}

// isErrorResult checks if the raw MCP response indicates a tool error.
func isErrorResult(raw string) bool {
	for _, line := range splitLines(raw) {
		var msg struct {
			ID     int `json:"id"`
			Result struct {
				IsError bool `json:"isError"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		if msg.ID == 2 {
			return msg.Result.IsError
		}
	}
	return false
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if start < i {
				lines = append(lines, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// iRunNiwaGoWithLastSessionID runs "niwa go <repo> <session-id>" using the
// session ID stored by a preceding create step.
func iRunNiwaGoWithLastSessionID(ctx context.Context, repo string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.lastSessionID == "" {
		return ctx, fmt.Errorf("no session_id stored; call a create session step first")
	}
	return ctx, runNiwa(s, s.homeDir, "niwa go "+repo+" "+s.lastSessionID)
}

// theResponseFileContainsLastSessionWorktreePath verifies that the response
// file contains the worktree path stored by a preceding create step.
func theResponseFileContainsLastSessionWorktreePath(ctx context.Context, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionWorktreePath == "" {
		return fmt.Errorf("no session worktree path stored; call a create session step first")
	}
	path, ok := s.envOverrides["NIWA_RESPONSE_FILE"]
	if !ok {
		return fmt.Errorf("NIWA_RESPONSE_FILE not set in this scenario")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading response file %s: %w", path, err)
	}
	want := s.lastSessionWorktreePath + "\n"
	got := string(data)
	if got != want {
		return fmt.Errorf("response file content mismatch:\n  want: %q\n  got:  %q", want, got)
	}
	return nil
}

// theResponseFileContainsPathUnderWorktreesDir verifies that the response
// file written by niwa session create contains a path under the instance's
// .niwa/worktrees/ directory. Used by AC-S5a where the session ID is not
// known ahead of time (CLI creates and returns it at runtime).
func theResponseFileContainsPathUnderWorktreesDir(ctx context.Context, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path, ok := s.envOverrides["NIWA_RESPONSE_FILE"]
	if !ok {
		return fmt.Errorf("NIWA_RESPONSE_FILE not set in this scenario")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading response file %s: %w", path, err)
	}
	got := strings.TrimRight(string(data), "\n")
	instRoot := filepath.Join(s.workspaceRoot, instance)
	worktreesDir := filepath.Join(instRoot, ".niwa", "worktrees")
	if !strings.HasPrefix(got, worktreesDir) {
		return fmt.Errorf("response file path %q is not under worktrees dir %q", got, worktreesDir)
	}
	if fi, err := os.Stat(got); err != nil {
		return fmt.Errorf("response file path %q does not exist: %w", got, err)
	} else if !fi.IsDir() {
		return fmt.Errorf("response file path %q is not a directory", got)
	}
	return nil
}

// aSessionLifecycleStateExistsForRepo verifies that at least one session
// state file exists for the named repo with the expected status.
func aSessionLifecycleStateExistsForRepo(ctx context.Context, repo, status, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	sessionsDir := filepath.Join(instRoot, ".niwa", "sessions")
	all, err := mcp.ListSessionLifecycleStates(sessionsDir)
	if err != nil {
		return fmt.Errorf("listing session states: %w", err)
	}
	for _, st := range all {
		if st.Repo == repo && st.Status == status {
			return nil
		}
	}
	return fmt.Errorf("no session with repo=%q status=%q found in %s; got %d sessions",
		repo, status, sessionsDir, len(all))
}

// iDelegateTaskToSessionRole calls niwa_delegate with session_id set to
// s.lastSessionID, routing the task to the session's worktree daemon.
// Stores the returned task_id in s.lastTaskID.
func iDelegateTaskToSessionRole(ctx context.Context, role, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.lastSessionID == "" {
		return ctx, fmt.Errorf("no session_id stored; call niwa_create_session first")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	args := fmt.Sprintf(`{"to":%q,"session_id":%q,"body":{"action":"test"},"mode":"async"}`, role, s.lastSessionID)
	out, err := callMCPToolAsRole(s, instRoot, "coordinator", "", "niwa_delegate", args)
	if err != nil {
		return ctx, fmt.Errorf("niwa_delegate to session: %w", err)
	}
	if isErrorResult(out) {
		return ctx, fmt.Errorf("niwa_delegate returned error: %s", out)
	}
	payload, err := extractMCPContentText(out)
	if err != nil {
		return ctx, fmt.Errorf("extracting task_id: %w", err)
	}
	var resp struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(payload), &resp); err != nil {
		return ctx, fmt.Errorf("parsing delegate response %q: %w", payload, err)
	}
	if resp.TaskID == "" {
		return ctx, fmt.Errorf("niwa_delegate returned empty task_id; payload: %s", payload)
	}
	s.lastTaskID = resp.TaskID
	return ctx, nil
}

func iDelegateTaskToSessionRoleExpectingError(ctx context.Context, role, sessionID, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	args := fmt.Sprintf(`{"to":%q,"session_id":%q,"body":{"action":"test"},"mode":"async"}`, role, sessionID)
	out, err := callMCPToolAsRole(s, instRoot, "coordinator", "", "niwa_delegate", args)
	if err != nil {
		return ctx, fmt.Errorf("callMCPToolAsRole: %w", err)
	}
	s.stdout = out
	return ctx, nil
}

// iTryToDelegateTaskToSessionRole calls niwa_delegate using s.lastSessionID
// and stores the raw MCP response in s.stdout without failing on errors.
// Use this for scenarios that assert a specific error code in the response.
func iTryToDelegateTaskToSessionRole(ctx context.Context, role, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.lastSessionID == "" {
		return ctx, fmt.Errorf("no session_id stored; call niwa_create_session first")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	args := fmt.Sprintf(`{"to":%q,"session_id":%q,"body":{"action":"test"},"mode":"async"}`, role, s.lastSessionID)
	out, err := callMCPToolAsRole(s, instRoot, "coordinator", "", "niwa_delegate", args)
	if err != nil {
		return ctx, fmt.Errorf("callMCPToolAsRole: %w", err)
	}
	s.stdout = out
	return ctx, nil
}

// theLastMCPResponseContainsCode asserts that s.stdout contains the given
// error code string (e.g. "SESSION_NOT_FOUND").
func theLastMCPResponseContainsCode(ctx context.Context, code string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if !strings.Contains(s.stdout, code) {
		return fmt.Errorf("response does not contain code %q:\n%s", code, s.stdout)
	}
	return nil
}

// iDelegateTaskToRoleWithoutSessionID calls niwa_delegate with no session_id
// and no read_only flag, storing the raw MCP response in s.stdout without
// failing. Use this to assert SESSION_REQUIRED or other error responses.
func iDelegateTaskToRoleWithoutSessionID(ctx context.Context, role, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	args := fmt.Sprintf(`{"to":%q,"body":{"action":"test"},"mode":"async"}`, role)
	out, err := callMCPToolAsRole(s, instRoot, "coordinator", "", "niwa_delegate", args)
	if err != nil {
		return ctx, fmt.Errorf("callMCPToolAsRole: %w", err)
	}
	s.stdout = out
	return ctx, nil
}

// iDelegateReadOnlyTaskToRole calls niwa_delegate with read_only:true and no
// session_id, routing the task to the main clone daemon. Stores the returned
// task_id in s.lastTaskID.
func iDelegateReadOnlyTaskToRole(ctx context.Context, role, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	args := fmt.Sprintf(`{"to":%q,"body":{"action":"test"},"mode":"async","read_only":true}`, role)
	out, err := callMCPToolAsRole(s, instRoot, "coordinator", "", "niwa_delegate", args)
	if err != nil {
		return ctx, fmt.Errorf("callMCPToolAsRole: %w", err)
	}
	if isErrorResult(out) {
		return ctx, fmt.Errorf("read_only delegate returned error: %s", out)
	}
	payload, err := extractMCPContentText(out)
	if err != nil {
		return ctx, fmt.Errorf("extracting task_id from read_only delegate: %w", err)
	}
	var resp struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(payload), &resp); err != nil {
		return ctx, fmt.Errorf("parsing read_only delegate response %q: %w", payload, err)
	}
	if resp.TaskID == "" {
		return ctx, fmt.Errorf("read_only delegate returned empty task_id; payload: %s", payload)
	}
	s.lastTaskID = resp.TaskID
	return ctx, nil
}

// iDelegateTaskToSessionRoleWithReadOnly calls niwa_delegate with both
// session_id=s.lastSessionID and read_only:true. session_id takes precedence
// so the task routes to the session worktree daemon. Stores task_id in s.lastTaskID.
func iDelegateTaskToSessionRoleWithReadOnly(ctx context.Context, role, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.lastSessionID == "" {
		return ctx, fmt.Errorf("no session_id stored; call niwa_create_session first")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	args := fmt.Sprintf(`{"to":%q,"session_id":%q,"body":{"action":"test"},"mode":"async","read_only":true}`, role, s.lastSessionID)
	out, err := callMCPToolAsRole(s, instRoot, "coordinator", "", "niwa_delegate", args)
	if err != nil {
		return ctx, fmt.Errorf("callMCPToolAsRole: %w", err)
	}
	if isErrorResult(out) {
		return ctx, fmt.Errorf("delegate with session_id+read_only returned error: %s", out)
	}
	payload, err := extractMCPContentText(out)
	if err != nil {
		return ctx, fmt.Errorf("extracting task_id: %w", err)
	}
	var resp struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(payload), &resp); err != nil {
		return ctx, fmt.Errorf("parsing delegate response %q: %w", payload, err)
	}
	if resp.TaskID == "" {
		return ctx, fmt.Errorf("delegate returned empty task_id; payload: %s", payload)
	}
	s.lastTaskID = resp.TaskID
	return ctx, nil
}

// noTaskFilesExistInInstance checks that .niwa/tasks/ in the given instance
// contains no .json files (i.e. no task was created).
func noTaskFilesExistInInstance(ctx context.Context, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	tasksDir := filepath.Join(s.workspaceRoot, instance, ".niwa", "tasks")
	entries, err := os.ReadDir(tasksDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading tasks dir %s: %w", tasksDir, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		subEntries, _ := os.ReadDir(filepath.Join(tasksDir, e.Name()))
		for _, se := range subEntries {
			if strings.HasSuffix(se.Name(), ".json") {
				return fmt.Errorf("unexpected task file %s/%s in tasks dir", e.Name(), se.Name())
			}
		}
	}
	return nil
}

// theTaskWasRoutedThroughLastSessionID verifies that the task recorded in
// s.lastTaskID has session_id == s.lastSessionID in its state.json.
//
// Task state always lives in the main instance root
// (<instanceRoot>/.niwa/tasks/<taskID>/state.json) regardless of which daemon
// processed the task — both main-clone routing and session-worktree routing
// write state there. The session_id field is written by createTaskEnvelope
// only when session routing is active, so its presence (and value) proves
// which routing path was taken, not just that the task completed.
func theTaskWasRoutedThroughLastSessionID(ctx context.Context, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastTaskID == "" {
		return fmt.Errorf("no task_id stored; delegate a task first")
	}
	if s.lastSessionID == "" {
		return fmt.Errorf("no session_id stored; call niwa_create_session first")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	stateFile := filepath.Join(instRoot, ".niwa", "tasks", s.lastTaskID, "state.json")
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return fmt.Errorf("reading state.json for task %s: %w", s.lastTaskID, err)
	}
	var st struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return fmt.Errorf("parsing state.json: %w", err)
	}
	if st.SessionID != s.lastSessionID {
		return fmt.Errorf("task %s was not routed through session %q: state.json has session_id=%q",
			s.lastTaskID, s.lastSessionID, st.SessionID)
	}
	return nil
}

// theSessionClaudeConversationIDIsSet asserts that the session state file for
// s.lastSessionID in instance has a non-empty ClaudeConversationID.
func theSessionClaudeConversationIDIsSet(ctx context.Context, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionID == "" {
		return fmt.Errorf("no session_id stored")
	}
	sessionsDir := filepath.Join(s.workspaceRoot, instance, ".niwa", "sessions")
	state, err := mcp.ReadSessionLifecycleState(sessionsDir, s.lastSessionID)
	if err != nil {
		return fmt.Errorf("ReadSessionLifecycleState: %w", err)
	}
	if state.ClaudeConversationID == "" {
		return fmt.Errorf("session %s has empty ClaudeConversationID", s.lastSessionID)
	}
	return nil
}

func theSessionClaudeConversationIDEquals(ctx context.Context, wantID, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionID == "" {
		return fmt.Errorf("no session_id stored")
	}
	sessionsDir := filepath.Join(s.workspaceRoot, instance, ".niwa", "sessions")
	state, err := mcp.ReadSessionLifecycleState(sessionsDir, s.lastSessionID)
	if err != nil {
		return fmt.Errorf("ReadSessionLifecycleState: %w", err)
	}
	if state.ClaudeConversationID != wantID {
		return fmt.Errorf("ClaudeConversationID = %q, want %q", state.ClaudeConversationID, wantID)
	}
	return nil
}

func theWorkerInSessionWasSpawnedWith(ctx context.Context, want string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionWorktreePath == "" {
		return fmt.Errorf("no session worktree path stored")
	}
	argsPath := filepath.Join(s.lastSessionWorktreePath, ".niwa", ".test", "worker_spawn_args.txt")
	data, err := os.ReadFile(argsPath)
	if err != nil {
		return fmt.Errorf("reading session worker spawn args: %w", err)
	}
	if !strings.Contains(string(data), want) {
		return fmt.Errorf("session worker spawn args do not contain %q:\n%s", want, string(data))
	}
	return nil
}

func theWorkerInSessionWasNotSpawnedWith(ctx context.Context, unwanted string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionWorktreePath == "" {
		return fmt.Errorf("no session worktree path stored")
	}
	argsPath := filepath.Join(s.lastSessionWorktreePath, ".niwa", ".test", "worker_spawn_args.txt")
	data, err := os.ReadFile(argsPath)
	if err != nil {
		return fmt.Errorf("reading session worker spawn args: %w", err)
	}
	if strings.Contains(string(data), unwanted) {
		return fmt.Errorf("session worker spawn args contain unexpected %q:\n%s", unwanted, string(data))
	}
	return nil
}

func theMainCloneIsOnBranch(ctx context.Context, repoName, instance, branch string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	repoPath, err := findRepoPathInInstance(instRoot, repoName)
	if err != nil {
		return err
	}
	out, err := runGitInDir(repoPath, "branch", "--show-current")
	if err != nil {
		return fmt.Errorf("git branch --show-current: %w", err)
	}
	got := strings.TrimSpace(out)
	if got != branch {
		return fmt.Errorf("repo %q is on branch %q, want %q", repoName, got, branch)
	}
	return nil
}

func theSessionBranchExistsInRepo(ctx context.Context, repoName, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionID == "" {
		return fmt.Errorf("no session_id stored")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	repoPath, err := findRepoPathInInstance(instRoot, repoName)
	if err != nil {
		return err
	}
	branchRef := "session/" + s.lastSessionID
	if _, err := runGitInDir(repoPath, "rev-parse", "--verify", branchRef); err != nil {
		return fmt.Errorf("branch %q does not exist in repo %q: %w", branchRef, repoName, err)
	}
	return nil
}

func theSessionBranchDoesNotExistInRepo(ctx context.Context, repoName, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionID == "" {
		return fmt.Errorf("no session_id stored")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	repoPath, err := findRepoPathInInstance(instRoot, repoName)
	if err != nil {
		return err
	}
	branchRef := "session/" + s.lastSessionID
	if _, err := runGitInDir(repoPath, "rev-parse", "--verify", branchRef); err == nil {
		return fmt.Errorf("branch %q still exists in repo %q after expected deletion", branchRef, repoName)
	}
	return nil
}

func theSessionWorktreeDirectoryDoesNotExist(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionWorktreePath == "" {
		return fmt.Errorf("no worktree path stored")
	}
	if _, err := os.Stat(s.lastSessionWorktreePath); err == nil {
		return fmt.Errorf("session worktree directory still exists at %s", s.lastSessionWorktreePath)
	}
	return nil
}

func iSetFakeClaudeSessionID(ctx context.Context, id string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	s.envOverrides["NIWA_FAKE_CLAUDE_SESSION_ID"] = id
	return ctx, nil
}

// findRepoPathInInstance scans instanceRoot two levels deep for a directory
// named repoName that contains a .git entry.
func findRepoPathInInstance(instanceRoot, repoName string) (string, error) {
	groups, err := os.ReadDir(instanceRoot)
	if err != nil {
		return "", fmt.Errorf("reading instance root %s: %w", instanceRoot, err)
	}
	for _, g := range groups {
		if !g.IsDir() || strings.HasPrefix(g.Name(), ".") {
			continue
		}
		groupDir := filepath.Join(instanceRoot, g.Name())
		entries, err := os.ReadDir(groupDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || e.Name() != repoName {
				continue
			}
			candidate := filepath.Join(groupDir, e.Name())
			if _, err := os.Stat(filepath.Join(candidate, ".git")); err == nil {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("repo %q not found in instance %s", repoName, instanceRoot)
}

// runGitInDir runs a git command in dir and returns combined stdout/stderr.
func runGitInDir(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}
