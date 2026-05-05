package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// listSessionsArgs holds optional filter parameters for niwa_list_sessions.
type listSessionsArgs struct {
	Repo   string `json:"repo,omitempty"`
	Status string `json:"status,omitempty"`
}

// handleListSessions implements niwa_list_sessions.
//
// It reads all per-session lifecycle state files, applies optional repo and
// status filters, and returns a JSON array of matching SessionLifecycleState
// objects. An empty array (not null) is returned when no sessions match.
func (s *Server) handleListSessions(args listSessionsArgs) toolResult {
	sessionsDir := filepath.Join(s.instanceRoot, ".niwa", "sessions")
	all, err := ListSessionLifecycleStates(sessionsDir)
	if err != nil {
		return errResult("listing sessions: " + err.Error())
	}
	var filtered []SessionLifecycleState
	for _, st := range all {
		if args.Repo != "" && st.Repo != args.Repo {
			continue
		}
		if args.Status != "" && st.Status != args.Status {
			continue
		}
		filtered = append(filtered, st)
	}
	if filtered == nil {
		filtered = []SessionLifecycleState{}
	}
	data, err := json.Marshal(filtered)
	if err != nil {
		return errResult("marshaling sessions: " + err.Error())
	}
	return textResult(string(data))
}

// createSessionArgs holds parameters for niwa_create_session.
type createSessionArgs struct {
	Repo            string `json:"repo"`
	Purpose         string `json:"purpose"`
	ParentSessionID string `json:"parent_session_id"`
}

// destroySessionArgs holds parameters for niwa_destroy_session.
type destroySessionArgs struct {
	SessionID string `json:"session_id"`
	// Force, when true, deletes the session branch with git branch -D even if
	// the branch has unmerged commits. When false (default), git branch -d is
	// used: the branch is deleted only if already merged; unmerged branches are
	// silently left in place so unfinished work is not discarded.
	Force bool `json:"force"`
}

// scaffoldWorktreeNiwa creates the minimal .niwa layout for a per-session
// worktree. It creates:
//
//   - .niwa/roles/<repo>/inbox/{in-progress,cancelled,expired,read}/
//   - .niwa/tasks/
//   - .niwa/sessions/
//   - .niwa/daemon.pid  (empty placeholder, mode 0600)
//   - .niwa/daemon.log  (empty placeholder, mode 0600)
//
// It does NOT create mcp.json or workspace-context.md — those are main-instance
// artifacts that are not needed in session worktrees.
func scaffoldWorktreeNiwa(worktreePath, repo string) error {
	niwaDir := filepath.Join(worktreePath, ".niwa")
	dirs := []string{
		niwaDir,
		filepath.Join(niwaDir, "tasks"),
		filepath.Join(niwaDir, "sessions"),
		filepath.Join(niwaDir, "roles", repo, "inbox"),
		filepath.Join(niwaDir, "roles", repo, "inbox", "in-progress"),
		filepath.Join(niwaDir, "roles", repo, "inbox", "cancelled"),
		filepath.Join(niwaDir, "roles", repo, "inbox", "expired"),
		filepath.Join(niwaDir, "roles", repo, "inbox", "read"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("scaffoldWorktreeNiwa: creating %s: %w", d, err)
		}
	}
	for _, fname := range []string{"daemon.pid", "daemon.log"} {
		path := filepath.Join(niwaDir, fname)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
			if err != nil {
				return fmt.Errorf("scaffoldWorktreeNiwa: creating %s: %w", fname, err)
			}
			_ = f.Close()
		}
	}
	return nil
}

// findRepoInWorkspace scans instanceRoot two levels deep for a directory
// named repoName that contains a .git entry, returning its absolute path.
// Returns an error if no such directory is found.
func findRepoInWorkspace(instanceRoot, repoName string) (string, error) {
	topEntries, err := os.ReadDir(instanceRoot)
	if err != nil {
		return "", fmt.Errorf("scanning workspace: %w", err)
	}
	for _, top := range topEntries {
		if !top.IsDir() || strings.HasPrefix(top.Name(), ".") {
			continue
		}
		groupDir := filepath.Join(instanceRoot, top.Name())
		subEntries, err := os.ReadDir(groupDir)
		if err != nil {
			continue
		}
		for _, sub := range subEntries {
			if !sub.IsDir() || sub.Name() != repoName {
				continue
			}
			candidate := filepath.Join(groupDir, sub.Name())
			if _, err := os.Stat(filepath.Join(candidate, ".git")); err == nil {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("repo %q not found in workspace %s", repoName, instanceRoot)
}

// handleCreateSession implements niwa_create_session.
//
// It validates the role, generates a session ID, creates a git worktree
// on a new branch, scaffolds the .niwa layout, writes the session state file,
// and starts the per-worktree daemon. On any failure after the worktree is
// created, the worktree is removed before returning.
func (s *Server) handleCreateSession(args createSessionArgs) toolResult {
	if args.Repo == "" {
		return errResultCode("BAD_PAYLOAD", "repo is required")
	}
	if args.Purpose == "" {
		return errResultCode("BAD_PAYLOAD", "purpose is required")
	}
	if s.daemonStarter == nil {
		return errResult("niwa_create_session: daemon starter not configured (internal error)")
	}

	// Validate role directory exists.
	roleDir := filepath.Join(s.instanceRoot, ".niwa", "roles", args.Repo)
	if _, err := os.Stat(roleDir); errors.Is(err, os.ErrNotExist) {
		return errResultCode("UNKNOWN_ROLE", fmt.Sprintf("role %q not found at %s", args.Repo, roleDir))
	}

	// Generate a session ID.
	sessionsDir := filepath.Join(s.instanceRoot, ".niwa", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		return errResult("creating sessions dir: " + err.Error())
	}
	sessionID, err := newSessionLifecycleID(sessionsDir)
	if err != nil {
		return errResult("generating session ID: " + err.Error())
	}

	// Find the actual git repo on disk.
	repoPath, err := findRepoInWorkspace(s.instanceRoot, args.Repo)
	if err != nil {
		return errResultCode("UNKNOWN_ROLE", err.Error())
	}

	// Worktree is placed under <instanceRoot>/.niwa/worktrees/<repo>-<session-id>/.
	worktreesDir := filepath.Join(s.instanceRoot, ".niwa", "worktrees")
	if err := os.MkdirAll(worktreesDir, 0o700); err != nil {
		return errResult("creating worktrees dir: " + err.Error())
	}
	worktreePath := filepath.Join(worktreesDir, args.Repo+"-"+sessionID)
	branchName := "session/" + sessionID

	// Create the worktree on a new branch.
	out, err := exec.Command("git", "-C", repoPath, "worktree", "add", worktreePath, "-b", branchName).CombinedOutput()
	if err != nil {
		return errResult(fmt.Sprintf("git worktree add: %v\n%s", err, out))
	}

	// From here, any failure must clean up the worktree.
	cleanupWorktree := func() {
		_ = exec.Command("git", "-C", repoPath, "worktree", "remove", "--force", worktreePath).Run()
	}

	// Scaffold the .niwa layout in the worktree.
	if err := scaffoldWorktreeNiwa(worktreePath, args.Repo); err != nil {
		cleanupWorktree()
		return errResult("scaffold: " + err.Error())
	}

	// Write the session state file.
	state := NewSessionLifecycleState(sessionID, args.Repo, args.Purpose, args.ParentSessionID, worktreePath)
	if err := WriteSessionLifecycleState(sessionsDir, state); err != nil {
		cleanupWorktree()
		return errResult("writing session state: " + err.Error())
	}

	// Start the per-worktree daemon with NIWA_MAIN_INSTANCE_ROOT and NIWA_SESSION_ID.
	extraEnv := []string{
		"NIWA_MAIN_INSTANCE_ROOT=" + s.instanceRoot,
		"NIWA_SESSION_ID=" + sessionID,
	}
	resp := map[string]string{
		"session_id":    sessionID,
		"worktree_path": worktreePath,
	}
	if err := s.daemonStarter(worktreePath, extraEnv); err != nil {
		// Non-fatal: session state is written; coordinator can retry daemon start.
		// Include a warning so the coordinator knows the daemon did not start.
		fmt.Fprintf(os.Stderr, "niwa_create_session: daemon failed to start at %s: %v\n", worktreePath, err)
		resp["daemon_warning"] = "daemon failed to start: " + err.Error()
	}

	result, _ := json.Marshal(resp)
	return textResult(string(result))
}

// handleDestroySession implements niwa_destroy_session.
//
// It is idempotent: if the session is already ended or abandoned, it returns
// the current state without further action. Otherwise it force-kills running
// workers, writes status="ended", stops the daemon, removes the worktree, and
// deletes the session branch.
func (s *Server) handleDestroySession(args destroySessionArgs) toolResult {
	if s.daemonStopper == nil {
		return errResult("niwa_destroy_session: daemon stopper not configured (internal error)")
	}

	sessionsDir := filepath.Join(s.instanceRoot, ".niwa", "sessions")
	state, err := ReadSessionLifecycleState(sessionsDir, args.SessionID)
	if err != nil {
		return errResultCode("SESSION_NOT_FOUND", err.Error())
	}

	// Idempotent: already terminal.
	if state.Status == SessionStatusEnded || state.Status == SessionStatusAbandoned {
		data, _ := json.Marshal(state)
		return textResult(string(data))
	}

	worktreePath := state.WorktreePath

	// Force-kill workers whose tasks are still running.
	killSessionWorkers(s.instanceRoot, worktreePath, args.SessionID)

	// Write terminal state.
	state.Status = SessionStatusEnded
	if err := WriteSessionLifecycleState(sessionsDir, state); err != nil {
		return errResult("writing session state: " + err.Error())
	}

	// Stop the per-worktree daemon.
	_ = s.daemonStopper(worktreePath)

	// Find the git repo to remove the worktree and delete the branch.
	repoPath, repoErr := findRepoInWorkspace(s.instanceRoot, state.Repo)

	// Remove the worktree directory.
	if repoErr == nil && worktreePath != "" {
		_ = exec.Command("git", "-C", repoPath, "worktree", "remove", "--force", worktreePath).Run()
		// Delete the session branch. With Force=false (default), use git branch -d
		// which only succeeds when the branch is already merged; unmerged branches
		// are left in place so unfinished work is not discarded. With Force=true,
		// git branch -D removes the branch regardless of merge status.
		branchArg := "-d"
		if args.Force {
			branchArg = "-D"
		}
		_ = exec.Command("git", "-C", repoPath, "branch", branchArg, "session/"+args.SessionID).Run()
	}

	data, _ := json.Marshal(state)
	return textResult(string(data))
}

// killSessionWorkers scans the main instance's task directory for running tasks
// whose envelope is present in the session worktree's inbox, sends SIGKILL to
// their worker process groups, and writes each affected task's state to abandoned
// with reason "session_destroyed". Best-effort: errors are ignored.
//
// Task store directories are rooted in the main instance
// (<mainInstanceRoot>/.niwa/tasks/); only the inbox files live in the worktree.
// A task belongs to this session if its in-progress envelope is present at
// <worktreePath>/.niwa/roles/<role>/inbox/in-progress/<taskID>.json.
func killSessionWorkers(mainInstanceRoot, worktreePath, sessionID string) {
	tasksDir := filepath.Join(mainInstanceRoot, ".niwa", "tasks")
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		return
	}
	rolesDir := filepath.Join(worktreePath, ".niwa", "roles")
	now := nowRFC3339Nano()
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		taskID := e.Name()
		taskDir := filepath.Join(tasksDir, taskID)
		_, st, err := ReadState(taskDir)
		if err != nil || st.State != TaskStateRunning {
			continue
		}
		// Check whether this task's envelope is in this session's worktree inbox.
		if !taskInWorktreeInbox(rolesDir, taskID) {
			continue
		}
		if st.Worker.PID > 0 {
			_ = syscall.Kill(-st.Worker.PID, syscall.SIGKILL)
		}
		reason := json.RawMessage(`{"error":"session_destroyed","session_id":` + jsonStr(sessionID) + `}`)
		_ = UpdateState(taskDir, func(cur *TaskState) (*TaskState, *TransitionLogEntry, error) {
			if cur.State != TaskStateRunning {
				return nil, nil, nil
			}
			next := *cur
			next.State = TaskStateAbandoned
			next.UpdatedAt = now
			next.Reason = reason
			next.StateTransitions = append(next.StateTransitions,
				StateTransition{From: TaskStateRunning, To: TaskStateAbandoned, At: now})
			entry := &TransitionLogEntry{
				Kind:   "session_destroyed",
				From:   TaskStateRunning,
				To:     TaskStateAbandoned,
				At:     now,
				Reason: reason,
			}
			return &next, entry, nil
		})
	}
}

// taskInWorktreeInbox reports whether taskID has an in-progress envelope under
// any role's inbox inside rolesDir.
func taskInWorktreeInbox(rolesDir, taskID string) bool {
	roles, err := os.ReadDir(rolesDir)
	if err != nil {
		return false
	}
	for _, role := range roles {
		if !role.IsDir() {
			continue
		}
		p := filepath.Join(rolesDir, role.Name(), "inbox", "in-progress", taskID+".json")
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// jsonStr returns a JSON-encoded string literal for s.
func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// nowRFC3339Nano returns the current UTC time in RFC3339Nano format.
func nowRFC3339Nano() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
