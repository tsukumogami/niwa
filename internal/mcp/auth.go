// Task-lifecycle authorization — the kindDelegator / kindExecutor / kindParty
// checks invoked by every MCP tool handler that mutates or reads a task.
//
// See DESIGN Decision 3 for the authorization model. In summary:
//
//   - kindDelegator (niwa_await_task, niwa_update_task, niwa_cancel_task):
//     caller's role matches envelope.from.role.
//   - kindExecutor (niwa_finish_task, niwa_report_progress): caller's taskID
//     matches state.json.task_id AND caller's role matches
//     state.json.worker.role. Mandatory-on-Linux hardening step: the
//     caller's PPIDChain(1) PID's start_time matches
//     state.json.worker.{pid, start_time}.
//   - kindParty (niwa_query_task): either delegator or executor passes.
//
// All error paths return PRD R50's codes — NOT_TASK_OWNER, NOT_TASK_PARTY,
// TASK_ALREADY_TERMINAL — with no new codes introduced.
//
// macOS degradation: PIDStartTime returns (0, nil) when the platform cannot
// sample a precise start time, in which case the executor check skips the
// PPID hardening step and falls back to PID/role equality. This is explicitly
// within the PRD's Known-Limitations ceiling; see DESIGN Decision 3.

package mcp

import (
	"path/filepath"
)

// accessKind enumerates the three authorization categories. Unexported so
// only the mcp package decides the mapping from tool to kind.
type accessKind int

const (
	// kindDelegator — caller must be the task's delegator.
	kindDelegator accessKind = iota
	// kindExecutor — caller must be the running worker of the task.
	kindExecutor
	// kindParty — caller must be either delegator or executor.
	kindParty
)

// String returns a human-readable access kind for logging and tests.
func (k accessKind) String() string {
	switch k {
	case kindDelegator:
		return "delegator"
	case kindExecutor:
		return "executor"
	case kindParty:
		return "party"
	}
	return "unknown"
}

// authIdentity captures the caller's identity as observed by the MCP
// server. Issue #3 will populate these fields from NIWA_INSTANCE_ROOT,
// NIWA_SESSION_ROLE, and NIWA_TASK_ID; until then, callers construct one
// per invocation.
//
// This intermediate struct keeps authorizeTaskCall decoupled from the
// Server struct's field names, so Issue #3 can freely rename or reshape
// Server without touching auth logic.
type authIdentity struct {
	InstanceRoot string // <instance-root> (owns .niwa/tasks/)
	Role         string // caller's registered role
	TaskID       string // caller's NIWA_TASK_ID (empty for coordinators)
}

// authorizeTaskCall runs the Decision 3 authorization check for a tool
// operating on taskID against the caller's identity. On success it
// returns the parsed envelope and state; on failure it returns
// (nil, nil, errResult) with the appropriate R50 code.
//
// The storage layer is read under shared flock; the returned snapshot is a
// consistent view of envelope.json + state.json. Callers that need to
// mutate must re-acquire an exclusive flock via UpdateState.
//
// Error-code mapping:
//
//   - ReadState → ErrCorruptedState → NOT_TASK_PARTY (fail closed).
//   - ReadState → ErrLockTimeout → NOT_TASK_PARTY (fail closed, retryable).
//   - ReadState → not-exist → NOT_TASK_PARTY (no leak of task-ID existence).
//   - Terminal state → TASK_ALREADY_TERMINAL for delegator + executor kinds
//     only when the tool requires a non-terminal task. kindParty accepts
//     terminal tasks (niwa_query_task is valid post-terminal).
func authorizeTaskCall(id authIdentity, taskID string, kind accessKind) (*TaskEnvelope, *TaskState, *toolResult) {
	// Defensive task-ID format check before we touch the filesystem. A
	// malformed ID cannot match any existing directory under our UUIDv4
	// naming discipline; reject with NOT_TASK_PARTY so we don't leak
	// whether the ID was malformed vs. missing.
	if !uuidV4Regex.MatchString(taskID) {
		r := errResultCode("NOT_TASK_PARTY", "not authorized for this task")
		return nil, nil, &r
	}

	taskDir := taskDirPath(id.InstanceRoot, taskID)
	env, st, err := ReadState(taskDir)
	if err != nil {
		// Fail closed on any read error (ENOENT, corruption, lock timeout).
		// We specifically do NOT return a distinguishable "not found" code
		// so a same-UID attacker cannot enumerate task IDs via error-code
		// observation.
		r := errResultCode("NOT_TASK_PARTY", "not authorized for this task")
		return nil, nil, &r
	}

	switch kind {
	case kindDelegator:
		if !checkDelegator(id, env) {
			r := errResultCode("NOT_TASK_OWNER", "only the delegator may perform this action")
			return nil, nil, &r
		}
		if isTaskStateTerminal(st.State) {
			r := errResultCode("TASK_ALREADY_TERMINAL",
				"task is in terminal state: "+st.State)
			return nil, nil, &r
		}
	case kindExecutor:
		if r := checkExecutor(id, st); r != nil {
			return nil, nil, r
		}
	case kindParty:
		if !checkDelegator(id, env) && checkExecutor(id, st) != nil {
			r := errResultCode("NOT_TASK_PARTY", "not authorized for this task")
			return nil, nil, &r
		}
		// kindParty intentionally does NOT reject terminal state —
		// niwa_query_task is valid after completion.
	}

	return env, st, nil
}

// checkDelegator returns true when id.Role matches envelope.from.role.
// Coordinators typically delegate; peer roles may also delegate to each
// other, so the delegator check is purely role-equality.
func checkDelegator(id authIdentity, env *TaskEnvelope) bool {
	return id.Role != "" && id.Role == env.From.Role
}

// checkExecutor verifies the caller is the running worker for the task.
// Three conjunctive gates:
//
//  1. id.TaskID == state.json.task_id (env-carried worker identity).
//  2. id.Role == state.json.worker.role (target role of the worker spawn).
//  3. Linux only: PPIDChain(1) PID's start_time matches
//     state.json.worker.start_time.
//
// A worker with pid==0 (daemon has not yet backfilled after cmd.Start)
// passes gates 1+2 but fails gate 3's PID comparison — the Issue 10 harness
// retries authorization-path tool calls for up to 2 s to ride out this
// window, so the MCP server intentionally does NOT skip the check here.
//
// The check returns a tool-layer errResult pointer on failure (nil on
// success) so the caller can forward it unchanged.
func checkExecutor(id authIdentity, st *TaskState) *toolResult {
	if st == nil {
		r := errResultCode("NOT_TASK_PARTY", "not authorized for this task")
		return &r
	}
	if isTaskStateTerminal(st.State) {
		r := errResultCode("TASK_ALREADY_TERMINAL",
			"task is in terminal state: "+st.State)
		return &r
	}
	if id.TaskID == "" || id.TaskID != st.TaskID {
		r := errResultCode("NOT_TASK_PARTY", "not authorized for this task")
		return &r
	}
	if id.Role == "" || id.Role != st.Worker.Role {
		r := errResultCode("NOT_TASK_PARTY", "not authorized for this task")
		return &r
	}

	// Linux hardening: PPIDChain(1) + PIDStartTime cross-check. Skipped on
	// platforms where PIDStartTime cannot produce a precise value (macOS
	// /proc is absent; start_time returns 0 and we degrade to PID-match
	// only per PRD Known Limitation).
	if st.Worker.PID > 0 && st.Worker.StartTime > 0 {
		chain, err := PPIDChain(1)
		if err != nil || len(chain) == 0 {
			// Unable to read the parent PID: on Linux this is a hard
			// failure (we expect a live claude -p parent); on macOS the
			// call still returns the Getppid result, so a failure here
			// signals something truly unexpected. Fail closed.
			r := errResultCode("NOT_TASK_PARTY", "not authorized for this task")
			return &r
		}
		parentPID := chain[0]
		if parentPID != st.Worker.PID {
			r := errResultCode("NOT_TASK_PARTY", "not authorized for this task")
			return &r
		}
		start, err := PIDStartTime(parentPID)
		if err == nil && start != st.Worker.StartTime {
			// Only treat a successful-but-divergent start_time as a
			// failure. An error from PIDStartTime (macOS, read failure)
			// degrades to PID-match-only — same trust ceiling documented
			// in the PRD.
			r := errResultCode("NOT_TASK_PARTY", "not authorized for this task")
			return &r
		}
	}
	return nil
}

// taskDirPath returns the on-disk directory for a task under instanceRoot.
// Centralized so every caller (auth, storage, daemon, CLI) resolves the
// same path without independent string joins.
func taskDirPath(instanceRoot, taskID string) string {
	return filepath.Join(instanceRoot, ".niwa", "tasks", taskID)
}
