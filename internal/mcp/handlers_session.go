package mcp

import (
	"context"
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

// GitInvoker is the test-injection seam for the session-create pipeline's
// git subprocess calls. It is the structural equivalent of
// workspace.GitInvoker — using a local interface here avoids an import
// cycle (workspace already imports mcp via daemon.go) while letting
// production callers pass workspace.StdGitInvoker() directly. Go's
// structural typing makes the cross-package interface assignment
// compile without an explicit adapter.
type GitInvoker interface {
	CommandContext(ctx context.Context, args ...string) *exec.Cmd
}

// CreateSessionParams collects the inputs to the factored CreateSession
// function. The struct is what the MCP handler builds for the
// `session/<sid>` default and what RunBootstrap builds for the
// `niwa-bootstrap/<sid>` prefix.
//
// BranchPrefix carries the load-bearing decision for R5 (branch name in
// session state). Empty means "session/" (back-compat for every existing
// caller of the MCP handler); a non-empty value (e.g., "niwa-bootstrap/")
// prepends to the generated session ID. The resulting branch name is
// persisted into SessionLifecycleState.BranchName so destroy and the
// push-hint warning resolve to the right ref.
//
// GitInvoker is the seam Issue 4 demands (R22): the worktree-add and
// branch-delete subprocess calls flow through this interface so unit
// tests can record argv and inject failures without forking real git.
// DaemonStarter mirrors the *Server.daemonStarter hook so the factored
// function does not need a *Server receiver.
type CreateSessionParams struct {
	InstanceRoot    string
	Repo            string
	Purpose         string
	ParentSessionID string
	BranchPrefix    string
	GitInvoker      GitInvoker
	DaemonStarter   func(worktreePath string, extraEnv []string) error
}

// listSessionsArgs holds optional filter parameters for niwa_list_sessions.
//
// `attached` and `available` are mutually exclusive. When neither is set,
// rows of any availability are returned (subject to repo/status filters).
type listSessionsArgs struct {
	Repo      string `json:"repo,omitempty"`
	Status    string `json:"status,omitempty"`
	Attached  bool   `json:"attached,omitempty"`
	Available bool   `json:"available,omitempty"`
}

// sessionListEntry is the wire shape returned by niwa_list_sessions: it
// embeds the persisted SessionLifecycleState and adds a computed daemon
// sub-object reflecting the per-worktree daemon's runtime liveness. The
// daemon field is computed at API call time, never persisted, so the
// SessionLifecycleState file's Status field stays single-writer (owned by
// the lifecycle code path that creates and destroys sessions).
//
// Issue 3 / #111: Status alone could not answer "is this session usable?"
// because a crashed daemon leaves Status=active. The daemon sub-object
// closes that gap by surfacing the PID file probe directly.
type sessionListEntry struct {
	SessionLifecycleState
	Daemon DaemonHealth `json:"daemon"`
}

// handleListSessions implements niwa_list_sessions.
//
// It reads all per-session lifecycle state files, applies optional repo,
// status, attached, and available filters, projects each row's attach
// sentinel into the embedded SessionLifecycleState.Attach pointer field,
// and returns a JSON array of sessionListEntry objects (each embedding
// SessionLifecycleState plus a computed daemon sub-object).
//
// An empty array (not null) is returned when no sessions match. The attach
// sub-object is omitted from the JSON (not null) when no live lock is held
// -- the omitempty tag on SessionLifecycleState.Attach plus a nil pointer
// produces an absent key (PRD R12 absent-vs-null contract).
func (s *Server) handleListSessions(args listSessionsArgs) toolResult {
	if args.Attached && args.Available {
		return errResult("attached and available are mutually exclusive")
	}
	sessionsDir := filepath.Join(s.instanceRoot, ".niwa", "sessions")
	all, err := ListSessionLifecycleStates(sessionsDir)
	if err != nil {
		return errResult("listing sessions: " + err.Error())
	}
	filtered := make([]sessionListEntry, 0, len(all))
	for _, st := range all {
		if args.Repo != "" && st.Repo != args.Repo {
			continue
		}
		if args.Status != "" && st.Status != args.Status {
			continue
		}
		// Project the attach sentinel; reapStale=true so reading the list
		// also opportunistically cleans dead-holder sentinels.
		attachState, attachAvail, _ := ReadAttachState(st.WorktreePath, true)
		if args.Attached && attachAvail != AttachAttached {
			continue
		}
		if args.Available && attachAvail != AttachAvailable {
			continue
		}
		if attachAvail == AttachAttached {
			st.Attach = attachState
		}
		filtered = append(filtered, sessionListEntry{
			SessionLifecycleState: st,
			Daemon:                daemonHealthFor(st.WorktreePath),
		})
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

// ErrSessionDaemonSpawnTimeout wraps ErrDaemonSpawnTimeout so callers of
// CreateSession can distinguish the daemon-startup failure from generic
// errors. The exported sentinel preserves the structured error code the
// MCP layer surfaces via errResultCode.
var ErrSessionDaemonSpawnTimeout = ErrDaemonSpawnTimeout

// ErrSessionUnknownRole is returned by CreateSession when the role
// directory under <instanceRoot>/.niwa/roles/<repo> does not exist.
// Callers map this to the UNKNOWN_ROLE structured error code.
var ErrSessionUnknownRole = errors.New("unknown role")

// CreateSession is the factored body of handleCreateSession: it validates
// the role, generates a session ID, creates a git worktree on a new
// branch, scaffolds the .niwa layout, writes the session state file,
// and starts the per-worktree daemon. On any failure after the worktree
// is created the worktree is removed before returning.
//
// The factoring lets workspace.RunBootstrap reuse the exact same body
// without going through the MCP handler envelope. params.BranchPrefix
// controls the branch name: empty == historic `session/<sid>`,
// non-empty == `<prefix><sid>` (e.g., `niwa-bootstrap/<sid>` for the
// bootstrap orchestrator). The chosen branch name is persisted into
// SessionLifecycleState.BranchName via NewSessionLifecycleState so
// destroy and warning paths resolve correctly.
//
// All git invocations route through params.GitInvoker (R22) so unit
// tests can record argv and inject faults without a real git binary.
// Returns (sessionID, worktreePath, branchName, error). A non-nil
// error may have a non-empty branchName when the caller needs to clean
// up after CreateSession partially succeeded (e.g., daemon-spawn
// timeout after the branch was created).
func CreateSession(ctx context.Context, params CreateSessionParams) (sessionID, worktreePath, branchName string, err error) {
	if params.Repo == "" {
		return "", "", "", errors.New("repo is required")
	}
	if params.Purpose == "" {
		return "", "", "", errors.New("purpose is required")
	}
	if params.DaemonStarter == nil {
		return "", "", "", errors.New("daemon starter not configured")
	}
	if params.GitInvoker == nil {
		return "", "", "", errors.New("git invoker not configured")
	}

	// Validate role directory exists.
	roleDir := filepath.Join(params.InstanceRoot, ".niwa", "roles", params.Repo)
	if _, statErr := os.Stat(roleDir); errors.Is(statErr, os.ErrNotExist) {
		return "", "", "", fmt.Errorf("%w: role %q not found at %s", ErrSessionUnknownRole, params.Repo, roleDir)
	}

	// Generate a session ID.
	sessionsDir := filepath.Join(params.InstanceRoot, ".niwa", "sessions")
	if mkErr := os.MkdirAll(sessionsDir, 0o700); mkErr != nil {
		return "", "", "", fmt.Errorf("creating sessions dir: %w", mkErr)
	}
	sid, idErr := newSessionLifecycleID(sessionsDir)
	if idErr != nil {
		return "", "", "", fmt.Errorf("generating session ID: %w", idErr)
	}

	// Find the actual git repo on disk.
	repoPath, repoErr := findRepoInWorkspace(params.InstanceRoot, params.Repo)
	if repoErr != nil {
		return "", "", "", fmt.Errorf("%w: %v", ErrSessionUnknownRole, repoErr)
	}

	// Worktree under <instanceRoot>/.niwa/worktrees/<repo>-<sid>/.
	worktreesDir := filepath.Join(params.InstanceRoot, ".niwa", "worktrees")
	if mkErr := os.MkdirAll(worktreesDir, 0o700); mkErr != nil {
		return "", "", "", fmt.Errorf("creating worktrees dir: %w", mkErr)
	}
	wtPath := filepath.Join(worktreesDir, params.Repo+"-"+sid)
	prefix := params.BranchPrefix
	if prefix == "" {
		prefix = "session/"
	}
	branch := prefix + sid

	// Create the worktree on a new branch via the injected invoker.
	addCmd := params.GitInvoker.CommandContext(ctx, "-C", repoPath, "worktree", "add", wtPath, "-b", branch)
	out, addErr := addCmd.CombinedOutput()
	if addErr != nil {
		return "", "", "", fmt.Errorf("git worktree add: %w\n%s", addErr, out)
	}

	// From here, any failure must clean up the worktree.
	cleanupWorktree := func() {
		removeCmd := params.GitInvoker.CommandContext(ctx, "-C", repoPath, "worktree", "remove", "--force", wtPath)
		_ = removeCmd.Run()
	}

	// Scaffold the .niwa layout in the worktree.
	if scaffoldErr := scaffoldWorktreeNiwa(wtPath, params.Repo); scaffoldErr != nil {
		cleanupWorktree()
		return "", "", branch, fmt.Errorf("scaffold: %w", scaffoldErr)
	}

	// Write the session state file. The branch name is persisted so
	// destroy and the push-hint warning resolve to the right ref —
	// load-bearing for R5 (branch name in session state).
	state := NewSessionLifecycleState(sid, params.Repo, params.Purpose, params.ParentSessionID, wtPath, branch)
	if writeErr := WriteSessionLifecycleState(sessionsDir, state); writeErr != nil {
		cleanupWorktree()
		return "", "", branch, fmt.Errorf("writing session state: %w", writeErr)
	}

	// Start the per-worktree daemon.
	extraEnv := []string{
		"NIWA_MAIN_INSTANCE_ROOT=" + params.InstanceRoot,
		"NIWA_SESSION_ID=" + sid,
	}
	if dErr := params.DaemonStarter(wtPath, extraEnv); dErr != nil {
		if errors.Is(dErr, ErrDaemonSpawnTimeout) {
			cleanupWorktree()
			_ = os.Remove(filepath.Join(sessionsDir, sid+".json"))
			delCmd := params.GitInvoker.CommandContext(ctx, "-C", repoPath, "branch", "-D", branch)
			_ = delCmd.Run()
			return "", "", branch, fmt.Errorf("%w: daemon for session %s did not become ready; check %s/.niwa/daemon.log",
				ErrSessionDaemonSpawnTimeout, sid, wtPath)
		}
		// Non-fatal: the session state is written; coordinator can retry.
		return sid, wtPath, branch, fmt.Errorf("daemon failed to start: %w", dErr)
	}
	return sid, wtPath, branch, nil
}

// handleCreateSession implements niwa_create_session as a thin wrapper
// around CreateSession. BranchPrefix is left empty so the historic
// `session/<sid>` branch name is preserved for every existing caller.
func (s *Server) handleCreateSession(args createSessionArgs) toolResult {
	if s.daemonStarter == nil {
		return errResult("niwa_create_session: daemon starter not configured (internal error)")
	}
	sid, wtPath, _, err := CreateSession(context.Background(), CreateSessionParams{
		InstanceRoot:    s.instanceRoot,
		Repo:            args.Repo,
		Purpose:         args.Purpose,
		ParentSessionID: args.ParentSessionID,
		BranchPrefix:    "",
		GitInvoker:      stdGitInvokerMCP{},
		DaemonStarter:   s.daemonStarter,
	})
	resp := map[string]string{}
	if sid != "" {
		resp["session_id"] = sid
	}
	if wtPath != "" {
		resp["worktree_path"] = wtPath
	}
	if err != nil {
		switch {
		case errors.Is(err, ErrSessionDaemonSpawnTimeout):
			fmt.Fprintf(os.Stderr, "niwa_create_session: daemon spawn timeout at %s; rolled back\n", wtPath)
			return errResultCode("DAEMON_SPAWN_TIMEOUT",
				fmt.Sprintf("daemon for session %s did not become ready within timeout; check %s/.niwa/daemon.log for the spawn trace. The session was rolled back.",
					sid, wtPath))
		case errors.Is(err, ErrSessionUnknownRole):
			return errResultCode("UNKNOWN_ROLE", err.Error())
		}
		// Bad-payload arms (empty repo/purpose) propagate as-is.
		if args.Repo == "" {
			return errResultCode("BAD_PAYLOAD", "repo is required")
		}
		if args.Purpose == "" {
			return errResultCode("BAD_PAYLOAD", "purpose is required")
		}
		// Daemon non-fatal: session state written, response carries a warning.
		if sid != "" {
			fmt.Fprintf(os.Stderr, "niwa_create_session: daemon failed to start at %s: %v\n", wtPath, err)
			resp["daemon_warning"] = err.Error()
			result, _ := json.Marshal(resp)
			return textResult(string(result))
		}
		return errResult(err.Error())
	}
	result, _ := json.Marshal(resp)
	return textResult(string(result))
}

// stdGitInvokerMCP is the production GitInvoker for handleCreateSession.
// Sibling of workspace.StdGitInvoker() — duplicated locally rather than
// imported to keep the mcp→workspace import direction unsullied.
type stdGitInvokerMCP struct{}

// CommandContext delegates to exec.CommandContext(ctx, "git", args...).
func (stdGitInvokerMCP) CommandContext(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "git", args...)
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
		resp := struct {
			SessionID string `json:"session_id"`
			Status    string `json:"status"`
		}{SessionID: state.SessionID, Status: state.Status}
		data, _ := json.Marshal(resp)
		return textResult(string(data))
	}

	worktreePath := state.WorktreePath

	// Reject when an attach lock is held by a live process and force is not
	// set. The structured error code SESSION_ATTACHED is what coordinators
	// pattern-match on; the message text references the recovery command and
	// the holder PID per PRD R13.
	if attachState, attachAvail, _ := ReadAttachState(worktreePath, false); attachAvail == AttachAttached && !args.Force {
		return errResultCode("SESSION_ATTACHED", fmt.Sprintf(
			"session %s is currently attached (pid=%d, started=%s); "+
				"run `niwa session detach %s --force` to release the attach lock first, "+
				"or pass force=true to destroy regardless",
			state.SessionID, attachState.OwnerPID, attachState.StartedAt, state.SessionID,
		))
	}

	// Force-kill workers whose tasks are still running.
	killSessionWorkers(s.instanceRoot, worktreePath, args.SessionID)

	// Auto-cascade change cleanup. F5 changes record OriginatingSession;
	// destroying the session removes the worktree those changes
	// reference, so without this cascade the changes would linger
	// pointing at a worktree_path that no longer exists. Cleanup happens
	// before the terminal-state write so a crash between the two leaves
	// the session in a consistent in-progress state for a retry.
	CancelChangesForSession(s.taskStoreRoot(), args.SessionID,
		"originating_session_destroyed", s.audit)

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
		// Resolve the branch name from session state so bootstrap-created
		// sessions (branch prefix `niwa-bootstrap/`) and historic
		// `session/<sid>` sessions both delete the correct ref.
		// EffectiveBranchName falls back to `session/<sid>` for pre-v1.1
		// state files that pre-date the BranchName field.
		branchName := state.EffectiveBranchName()
		if err := exec.Command("git", "-C", repoPath, "branch", branchArg, branchName).Run(); err != nil && !args.Force {
			state.BranchWarning = fmt.Sprintf(
				"branch %s was not deleted (unmerged commits remain); review and delete manually: git -C %s branch -D %s",
				branchName, repoPath, branchName,
			)
		}
	}

	// Use a dedicated response struct so BranchWarning (which has json:"-" on
	// the disk type) can appear in the wire response without risk of accidental
	// persistence via WriteSessionLifecycleState.
	resp := struct {
		SessionID     string `json:"session_id"`
		Status        string `json:"status"`
		BranchWarning string `json:"branch_warning,omitempty"`
	}{
		SessionID:     state.SessionID,
		Status:        state.Status,
		BranchWarning: state.BranchWarning,
	}
	data, _ := json.Marshal(resp)
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
