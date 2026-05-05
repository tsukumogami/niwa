package functional

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tsukumogami/niwa/internal/mcp"
)

// session_steps_test.go holds step definitions for niwa_create_session and
// niwa_destroy_session functional tests (Issue #97).

// iCallCreateSession calls niwa_create_session via the MCP interface and
// stores the session_id and worktree_path in the scenario state.
func iCallCreateSession(ctx context.Context, repo, purpose, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	args := fmt.Sprintf(`{"repo":%q,"purpose":%q}`, repo, purpose)
	out, err := callMCPToolAsRole(s, instRoot, "coordinator", "", "niwa_create_session", args)
	if err != nil {
		return ctx, fmt.Errorf("callMCPToolAsRole niwa_create_session: %w", err)
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
