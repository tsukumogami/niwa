package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/tsukumogami/niwa/internal/worktree"
)

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
type sessionListEntry struct {
	worktree.SessionLifecycleState
	Daemon worktree.DaemonHealth `json:"daemon"`
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
	all, err := worktree.ListSessionLifecycleStates(sessionsDir)
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
		attachState, attachAvail, _ := worktree.ReadAttachState(st.WorktreePath, true)
		if args.Attached && attachAvail != worktree.AttachAttached {
			continue
		}
		if args.Available && attachAvail != worktree.AttachAvailable {
			continue
		}
		if attachAvail == worktree.AttachAttached {
			st.Attach = attachState
		}
		filtered = append(filtered, sessionListEntry{
			SessionLifecycleState: st,
			Daemon:                worktree.DaemonHealthFor(st.WorktreePath),
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

// handleCreateSession implements niwa_create_session as a thin wrapper around
// worktree.CreateSession. BranchPrefix is left empty so the historic
// `session/<sid>` branch name is preserved for every existing caller.
func (s *Server) handleCreateSession(args createSessionArgs) toolResult {
	sid, wtPath, _, err := worktree.CreateSession(context.Background(), worktree.CreateSessionParams{
		InstanceRoot:    s.instanceRoot,
		Repo:            args.Repo,
		Purpose:         args.Purpose,
		ParentSessionID: args.ParentSessionID,
		BranchPrefix:    "",
		GitInvoker:      worktree.StdGitInvoker{},
	})
	resp := map[string]string{}
	if sid != "" {
		resp["session_id"] = sid
	}
	if wtPath != "" {
		resp["worktree_path"] = wtPath
	}
	if err != nil {
		if errors.Is(err, worktree.ErrSessionUnknownRole) {
			return errResultCode("UNKNOWN_ROLE", err.Error())
		}
		// Bad-payload arms (empty repo/purpose) propagate as-is.
		if args.Repo == "" {
			return errResultCode("BAD_PAYLOAD", "repo is required")
		}
		if args.Purpose == "" {
			return errResultCode("BAD_PAYLOAD", "purpose is required")
		}
		return errResult(err.Error())
	}
	result, _ := json.Marshal(resp)
	return textResult(string(result))
}

// handleDestroySession implements niwa_destroy_session.
//
// It is idempotent: if the session is already ended or abandoned, it returns
// the current state without further action. Otherwise it force-kills running
// workers, cascades change cleanup, removes the worktree, deletes the session
// branch, and stops the per-worktree daemon.
func (s *Server) handleDestroySession(args destroySessionArgs) toolResult {
	if s.daemonStopper == nil {
		return errResult("niwa_destroy_session: daemon stopper not configured (internal error)")
	}

	sessionsDir := filepath.Join(s.instanceRoot, ".niwa", "sessions")
	state, err := worktree.ReadSessionLifecycleState(sessionsDir, args.SessionID)
	if err != nil {
		return errResultCode("SESSION_NOT_FOUND", err.Error())
	}

	// Idempotent: already terminal.
	if state.Status == worktree.SessionStatusEnded || state.Status == worktree.SessionStatusAbandoned {
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
	if attachState, attachAvail, _ := worktree.ReadAttachState(worktreePath, false); attachAvail == worktree.AttachAttached && !args.Force {
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
	// destroying the session removes the worktree those changes reference, so
	// without this cascade the changes would linger pointing at a worktree_path
	// that no longer exists. Cleanup happens before the worktree teardown so a
	// crash between the two leaves the session in a consistent state for retry.
	CancelChangesForSession(s.taskStoreRoot(), args.SessionID,
		"originating_session_destroyed", s.audit)

	// Tear down the worktree and branch via the shared lifecycle helper. This
	// writes the terminal SessionLifecycleState, removes the git worktree, and
	// deletes the session branch.
	resultState, err := worktree.DestroySession(context.Background(), s.instanceRoot, args.SessionID, args.Force, worktree.StdGitInvoker{})
	if err != nil {
		return errResult("destroying session: " + err.Error())
	}

	// Stop the per-worktree daemon (mesh teardown; removed with the mesh).
	_ = s.daemonStopper(worktreePath)

	// Use a dedicated response struct so BranchWarning (which has json:"-" on
	// the disk type) can appear in the wire response without risk of accidental
	// persistence via WriteSessionLifecycleState.
	resp := struct {
		SessionID     string `json:"session_id"`
		Status        string `json:"status"`
		BranchWarning string `json:"branch_warning,omitempty"`
	}{
		SessionID:     resultState.SessionID,
		Status:        resultState.Status,
		BranchWarning: resultState.BranchWarning,
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
