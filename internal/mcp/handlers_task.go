// Task-lifecycle MCP tool handlers (Issue #3).
//
// Each handler:
//
//   - Invokes authorizeTaskCall() with the appropriate accessKind for tools
//     that mutate or read a specific task. Non-task-specific tools
//     (niwa_delegate, niwa_list_outbound_tasks) skip the per-task check.
//
//   - Funnels every state.json mutation through taskstore.UpdateState so the
//     flock + atomic-rename + fsync discipline from Decision 1 is applied
//     uniformly.
//
//   - Returns errors as textResult with a PRD R50 error_code prefix. The six
//     legal codes are NOT_TASK_OWNER, NOT_TASK_PARTY, TASK_ALREADY_TERMINAL,
//     BAD_PAYLOAD, BAD_TYPE, UNKNOWN_ROLE. No new codes are introduced.

package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// progressSummaryLimit truncates incoming progress summaries to bound the
// size of state.json and transitions.log. 200 chars is the PRD limit; the
// trailing ellipsis marks truncation explicitly.
const progressSummaryLimit = 200

// bodySummaryLimit bounds the single-line body summary returned by
// niwa_list_outbound_tasks; matches CLI niwa task list for parity.
const bodySummaryLimit = 200

// delegateArgs — niwa_delegate input.
type delegateArgs struct {
	To        string          `json:"to"`
	Body      json.RawMessage `json:"body"`
	Mode      string          `json:"mode,omitempty"`
	ExpiresAt string          `json:"expires_at,omitempty"`
}

type queryTaskArgs struct {
	TaskID string `json:"task_id"`
}

type awaitTaskArgs struct {
	TaskID         string `json:"task_id"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

type reportProgressArgs struct {
	TaskID  string          `json:"task_id"`
	Summary string          `json:"summary"`
	Body    json.RawMessage `json:"body,omitempty"`
}

type finishTaskArgs struct {
	TaskID  string          `json:"task_id"`
	Outcome string          `json:"outcome"`
	Result  json.RawMessage `json:"result,omitempty"`
	Reason  json.RawMessage `json:"reason,omitempty"`
}

type listOutboundArgs struct {
	To     string `json:"to,omitempty"`
	Status string `json:"status,omitempty"`
}

type updateTaskArgs struct {
	TaskID string          `json:"task_id"`
	Body   json.RawMessage `json:"body"`
}

type cancelTaskArgs struct {
	TaskID string `json:"task_id"`
}

// identity returns the caller's authIdentity derived from Server fields.
func (s *Server) identity() authIdentity {
	return authIdentity{
		InstanceRoot: s.instanceRoot,
		Role:         s.role,
		TaskID:       s.taskID,
	}
}

// --- niwa_delegate -----------------------------------------------------------

// handleDelegate creates a new task and inserts it into the target role's
// inbox. Async mode (default) returns {task_id}; sync mode registers an
// awaitWaiter and blocks until the worker transitions the task to a
// terminal state.
func (s *Server) handleDelegate(args delegateArgs) toolResult {
	if args.To == "" {
		return errResult("to is required")
	}
	if len(args.Body) == 0 || string(args.Body) == "null" {
		return errResult("body is required")
	}
	if args.Mode == "" {
		args.Mode = "async"
	}
	if args.Mode != "async" && args.Mode != "sync" {
		return errResultCode("BAD_PAYLOAD",
			fmt.Sprintf("mode must be \"async\" or \"sync\"; got %q", args.Mode))
	}
	if !s.isKnownRole(args.To) {
		return errResultCode("UNKNOWN_ROLE",
			fmt.Sprintf("role %q is not registered under .niwa/roles/", args.To))
	}
	if s.instanceRoot == "" {
		return errResult("NIWA_INSTANCE_ROOT not set")
	}
	if s.role == "" {
		return errResult("NIWA_SESSION_ROLE not set")
	}

	taskID, errTR := s.createTaskEnvelope(args.To, args.Body, args.ExpiresAt, "")
	if errTR.IsError {
		return errTR
	}

	if args.Mode == "async" {
		return textResult(fmt.Sprintf(`{"task_id":%q}`, taskID))
	}

	// Sync mode: register awaitWaiter, then return when terminal.
	ch, cancel := s.registerAwaitWaiter(taskID)
	defer cancel()
	// Race-guard: re-read state.json in case the task already completed
	// before the waiter was registered.
	taskDir := taskDirPath(s.instanceRoot, taskID)
	if _, st, err := ReadState(taskDir); err == nil && isTaskStateTerminal(st.State) {
		return formatTerminalResult(st)
	}

	// No default sync timeout — the PRD bounds this via worker-side retry
	// cap + stall watchdog. Callers that want a hard ceiling can use async
	// mode + niwa_await_task with timeout_seconds.
	evt := <-ch
	return formatEventResult(evt, taskDir)
}

// createTaskEnvelope writes .niwa/tasks/<id>/envelope.json + state.json and
// then atomic-renames the envelope into the target role's inbox. Returns
// the allocated task ID or an errResult-shaped toolResult.
//
// parentTaskID, when non-empty, overrides s.taskID (used by niwa_ask to
// force a top-level task even when the caller is itself a worker).
func (s *Server) createTaskEnvelope(to string, body json.RawMessage, expiresAt, parentTaskID string) (string, toolResult) {
	taskID := NewTaskID()
	taskDir := taskDirPath(s.instanceRoot, taskID)
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		return "", errResult("cannot create task dir: " + err.Error())
	}

	now := time.Now().UTC().Format(time.RFC3339)
	parent := parentTaskID
	if parent == "" {
		parent = s.taskID
	}
	env := TaskEnvelope{
		V:            1,
		ID:           taskID,
		From:         TaskParty{Role: s.role, PID: os.Getpid()},
		To:           TaskParty{Role: to},
		Body:         body,
		SentAt:       now,
		ParentTaskID: parent,
		ExpiresAt:    expiresAt,
	}
	envBytes, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return "", errResult("cannot marshal envelope: " + err.Error())
	}
	if err := os.WriteFile(filepath.Join(taskDir, envelopeFileName), envBytes, 0o600); err != nil {
		return "", errResult("cannot write envelope: " + err.Error())
	}

	// Seed state.json. We cannot use UpdateState yet because it requires an
	// existing state.json; write the initial state directly and then rely
	// on UpdateState for every subsequent mutation.
	st := &TaskState{
		V:      1,
		TaskID: taskID,
		State:  TaskStateQueued,
		StateTransitions: []StateTransition{
			{From: "", To: TaskStateQueued, At: now},
		},
		MaxRestarts:   3,
		DelegatorRole: s.role,
		TargetRole:    to,
		UpdatedAt:     now,
	}
	stBytes, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return "", errResult("cannot marshal state.json: " + err.Error())
	}
	if err := os.WriteFile(filepath.Join(taskDir, stateFileName), stBytes, 0o600); err != nil {
		return "", errResult("cannot write state.json: " + err.Error())
	}

	// Insert into target role's inbox via atomic rename. We write a
	// task.delegate Message wrapper so the inbox format stays homogeneous
	// across peer messages and delegations.
	inboxDir := filepath.Join(s.instanceRoot, ".niwa", "roles", to, "inbox")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		return "", errResultCode("INBOX_UNWRITABLE", "cannot create inbox: "+err.Error())
	}
	msg := Message{
		V:      1,
		ID:     taskID,
		Type:   "task.delegate",
		From:   MessageFrom{Role: s.role, PID: os.Getpid()},
		To:     MessageTo{Role: to},
		TaskID: taskID,
		SentAt: now,
		Body:   body,
	}
	if errTR := writeMessageAtomic(inboxDir, taskID, msg); errTR.IsError {
		return "", errTR
	}
	return taskID, toolResult{}
}

// registerAwaitWaiter allocates a size-1 buffered chan taskEvent under
// waitersMu and returns (channel, cancel). cancel removes the entry.
func (s *Server) registerAwaitWaiter(taskID string) (chan taskEvent, func()) {
	ch := make(chan taskEvent, 1)
	s.waitersMu.Lock()
	s.awaitWaiters[taskID] = ch
	s.waitersMu.Unlock()
	return ch, func() {
		s.waitersMu.Lock()
		delete(s.awaitWaiters, taskID)
		s.waitersMu.Unlock()
	}
}

// --- niwa_query_task ---------------------------------------------------------

// handleQueryTask returns state + transitions + restart_count + last_progress
// plus terminal-only fields. kindParty accepts both delegator and executor;
// non-parties receive NOT_TASK_PARTY. Non-blocking.
func (s *Server) handleQueryTask(args queryTaskArgs) toolResult {
	_, st, errR := authorizeTaskCall(s.identity(), args.TaskID, kindParty)
	if errR != nil {
		return *errR
	}
	return textResult(formatQueryResult(st))
}

// --- niwa_await_task ---------------------------------------------------------

func (s *Server) handleAwaitTask(args awaitTaskArgs) toolResult {
	s.maybeRegisterCoordinator()

	timeout := args.TimeoutSeconds
	if timeout <= 0 {
		timeout = 600
	}
	_, st, errR := authorizeTaskCall(s.identity(), args.TaskID, kindDelegator)
	// kindDelegator already returns TASK_ALREADY_TERMINAL on terminal tasks;
	// niwa_await_task should instead return the terminal result.
	if errR != nil {
		if errorCode(errR) == "TASK_ALREADY_TERMINAL" {
			// Re-read state.json to obtain the terminal payload.
			taskDir := taskDirPath(s.instanceRoot, args.TaskID)
			if _, stTerm, err := ReadState(taskDir); err == nil {
				return formatTerminalResult(stTerm)
			}
		}
		return *errR
	}

	// Register waiter BEFORE the race-guard re-read so a terminal message
	// arriving between the read and registration still wakes us.
	ch, cancel := s.registerAwaitWaiter(args.TaskID)
	defer cancel()

	// Race guard: re-read state.json; if terminal, return immediately.
	taskDir := taskDirPath(s.instanceRoot, args.TaskID)
	if _, fresh, err := ReadState(taskDir); err == nil && isTaskStateTerminal(fresh.State) {
		return formatTerminalResult(fresh)
	}

	select {
	case evt := <-ch:
		return formatEventResult(evt, taskDir)
	case <-time.After(time.Duration(timeout) * time.Second):
		// On timeout return a shape that signals the timeout explicitly while
		// preserving the current non-terminal state so callers can inspect
		// progress without a second call. This mirrors handleAsk's timeout
		// result shape for consistency.
		currentState := st.State
		lastProgress := st.LastProgress
		if _, fresh, err := ReadState(taskDir); err == nil {
			currentState = fresh.State
			lastProgress = fresh.LastProgress
		}
		timeoutPayload := map[string]any{
			"status":          "timeout",
			"task_id":         args.TaskID,
			"current_state":   currentState,
			"timeout_seconds": timeout,
		}
		if lastProgress != nil {
			timeoutPayload["last_progress"] = lastProgress
		}
		b, _ := json.Marshal(timeoutPayload)
		return textResult(string(b))
	}
}

// --- niwa_report_progress ----------------------------------------------------

func (s *Server) handleReportProgress(args reportProgressArgs) toolResult {
	if args.Summary == "" {
		return errResultCode("BAD_PAYLOAD", "summary is required")
	}
	_, _, errR := authorizeTaskCall(s.identity(), args.TaskID, kindExecutor)
	if errR != nil {
		return *errR
	}

	summary := truncateSummary(args.Summary, progressSummaryLimit)
	now := time.Now().UTC().Format(time.RFC3339)

	taskDir := taskDirPath(s.instanceRoot, args.TaskID)
	var delegatorRole string
	if err := UpdateState(taskDir, func(cur *TaskState) (*TaskState, *TransitionLogEntry, error) {
		next := *cur
		next.UpdatedAt = now
		next.LastProgress = &TaskProgress{Summary: summary, At: now}
		delegatorRole = cur.DelegatorRole
		entry := &TransitionLogEntry{
			Kind:    "progress",
			Summary: summary,
			At:      now,
			Actor:   &TransitionActor{Kind: "worker", PID: os.Getpid(), Role: s.role},
		}
		return &next, entry, nil
	}); err != nil {
		return mapStoreError(err)
	}

	// Best-effort task.progress delivery to the delegator's inbox. Failure
	// is logged only; the caller-visible success is the state.json write.
	if delegatorRole != "" {
		s.sendTaskMessage(delegatorRole, "task.progress", args.TaskID, map[string]any{
			"task_id": args.TaskID,
			"summary": summary,
			"body":    args.Body,
			"at":      now,
		})
	}

	return textResult(fmt.Sprintf(`{"status":"recorded","task_id":%q,"summary":%q}`,
		args.TaskID, summary))
}

// truncateSummary trims s to limit runes and appends an ellipsis when truncated.
func truncateSummary(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "…"
}

// --- niwa_finish_task --------------------------------------------------------

func (s *Server) handleFinishTask(args finishTaskArgs) toolResult {
	if args.Outcome != TaskStateCompleted && args.Outcome != TaskStateAbandoned {
		return errResultCode("BAD_PAYLOAD",
			fmt.Sprintf("outcome must be \"completed\" or \"abandoned\"; got %q", args.Outcome))
	}
	// Payload invariants: completed requires result, must not have reason;
	// abandoned requires reason, must not have result.
	switch args.Outcome {
	case TaskStateCompleted:
		if !hasPayload(args.Result) {
			return errResultCode("BAD_PAYLOAD", "outcome=completed requires result")
		}
		if hasPayload(args.Reason) {
			return errResultCode("BAD_PAYLOAD", "outcome=completed must not include reason")
		}
	case TaskStateAbandoned:
		if !hasPayload(args.Reason) {
			return errResultCode("BAD_PAYLOAD", "outcome=abandoned requires reason")
		}
		if hasPayload(args.Result) {
			return errResultCode("BAD_PAYLOAD", "outcome=abandoned must not include result")
		}
	}

	_, _, errR := authorizeTaskCall(s.identity(), args.TaskID, kindExecutor)
	if errR != nil {
		// Second call on terminal state returns {status:"already_terminal"}.
		if errorCode(errR) == "TASK_ALREADY_TERMINAL" {
			taskDir := taskDirPath(s.instanceRoot, args.TaskID)
			if _, st, err := ReadState(taskDir); err == nil {
				return textResult(fmt.Sprintf(
					`{"status":"already_terminal","error_code":"TASK_ALREADY_TERMINAL","current_state":%q}`,
					st.State))
			}
		}
		return *errR
	}

	now := time.Now().UTC().Format(time.RFC3339)
	taskDir := taskDirPath(s.instanceRoot, args.TaskID)
	var delegatorRole string
	if err := UpdateState(taskDir, func(cur *TaskState) (*TaskState, *TransitionLogEntry, error) {
		next := *cur
		next.State = args.Outcome
		next.UpdatedAt = now
		next.StateTransitions = append(next.StateTransitions,
			StateTransition{From: cur.State, To: args.Outcome, At: now})
		if args.Outcome == TaskStateCompleted {
			next.Result = args.Result
		} else {
			next.Reason = args.Reason
		}
		delegatorRole = cur.DelegatorRole
		entry := &TransitionLogEntry{
			Kind:   "state_transition",
			From:   cur.State,
			To:     args.Outcome,
			At:     now,
			Result: args.Result,
			Reason: args.Reason,
			Actor:  &TransitionActor{Kind: "worker", PID: os.Getpid(), Role: s.role},
		}
		return &next, entry, nil
	}); err != nil {
		return mapStoreError(err)
	}

	// Deliver a task.completed / task.abandoned message to the delegator.
	msgType := "task.completed"
	body := map[string]any{"task_id": args.TaskID, "result": args.Result}
	if args.Outcome == TaskStateAbandoned {
		msgType = "task.abandoned"
		body = map[string]any{"task_id": args.TaskID, "reason": args.Reason}
	}
	if delegatorRole != "" {
		s.sendTaskMessage(delegatorRole, msgType, args.TaskID, body)
	}

	return textResult(fmt.Sprintf(`{"status":%q,"task_id":%q}`, args.Outcome, args.TaskID))
}

// hasPayload reports whether a json.RawMessage carries a non-null, non-empty
// JSON value. An absent optional field decodes as nil (len==0); "null" is a
// distinct caller-supplied value.
func hasPayload(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	return string(raw) != "null"
}

// --- niwa_list_outbound_tasks ------------------------------------------------

func (s *Server) handleListOutboundTasks(args listOutboundArgs) toolResult {
	if s.instanceRoot == "" {
		return errResult("NIWA_INSTANCE_ROOT not set")
	}
	tasksDir := filepath.Join(s.instanceRoot, ".niwa", "tasks")
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return textResult(`{"tasks":[]}`)
		}
		return errResult("cannot read tasks dir: " + err.Error())
	}

	type row struct {
		TaskID      string `json:"task_id"`
		ToRole      string `json:"to_role"`
		State       string `json:"state"`
		AgeSeconds  int64  `json:"age_seconds"`
		BodySummary string `json:"body_summary"`
	}
	var rows []row
	now := time.Now().UTC()
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		taskDir := filepath.Join(tasksDir, e.Name())
		env, st, err := ReadState(taskDir)
		if err != nil {
			continue
		}
		if env.From.Role != s.role {
			continue
		}
		if args.To != "" && env.To.Role != args.To {
			continue
		}
		if args.Status != "" && st.State != args.Status {
			continue
		}
		sent, _ := time.Parse(time.RFC3339, env.SentAt)
		age := int64(0)
		if !sent.IsZero() {
			age = int64(now.Sub(sent).Seconds())
		}
		rows = append(rows, row{
			TaskID:      env.ID,
			ToRole:      env.To.Role,
			State:       st.State,
			AgeSeconds:  age,
			BodySummary: bodySummary(env.Body),
		})
	}

	// Stable ordering: most recent first by task_id string (UUIDv4 is random,
	// so this is just a deterministic tiebreaker — CLI "niwa task list"
	// provides the user-facing chronological view).
	sort.Slice(rows, func(i, j int) bool { return rows[i].TaskID < rows[j].TaskID })

	out, _ := json.Marshal(map[string]any{"tasks": rows})
	return textResult(string(out))
}

// bodySummary marshals body to a single-line JSON string and truncates to
// bodySummaryLimit runes.
func bodySummary(body json.RawMessage) string {
	if len(body) == 0 {
		return ""
	}
	// Re-marshal via a generic decode so we collapse any pretty-printing.
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return truncateSummary(string(body), bodySummaryLimit)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return truncateSummary(string(body), bodySummaryLimit)
	}
	return truncateSummary(string(out), bodySummaryLimit)
}

// --- niwa_update_task --------------------------------------------------------

func (s *Server) handleUpdateTask(args updateTaskArgs) toolResult {
	if len(args.Body) == 0 || string(args.Body) == "null" {
		return errResultCode("BAD_PAYLOAD", "body is required")
	}
	env, _, errR := authorizeTaskCall(s.identity(), args.TaskID, kindDelegator)
	if errR != nil {
		return *errR
	}

	taskDir := taskDirPath(s.instanceRoot, args.TaskID)

	// Under exclusive lock via UpdateState: reject if state != queued, else
	// rewrite envelope.body and the queued inbox file.
	var tooLate string
	err := UpdateState(taskDir, func(cur *TaskState) (*TaskState, *TransitionLogEntry, error) {
		if cur.State != TaskStateQueued {
			tooLate = cur.State
			return nil, nil, nil
		}
		// State is unchanged; we just bump updated_at to capture the edit.
		next := *cur
		next.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		return &next, nil, nil
	})
	if err != nil {
		if err == ErrAlreadyTerminal {
			return textResult(fmt.Sprintf(`{"status":"too_late","current_state":%q}`, "terminal"))
		}
		return mapStoreError(err)
	}
	if tooLate != "" {
		return textResult(fmt.Sprintf(`{"status":"too_late","current_state":%q}`, tooLate))
	}

	// Rewrite envelope.json with the new body.
	newEnv := *env
	newEnv.Body = args.Body
	envBytes, err := json.MarshalIndent(newEnv, "", "  ")
	if err != nil {
		return errResult("cannot marshal updated envelope: " + err.Error())
	}
	envTmp := filepath.Join(taskDir, envelopeFileName+".tmp")
	if err := os.WriteFile(envTmp, envBytes, 0o600); err != nil {
		return errResult("cannot write envelope tmp: " + err.Error())
	}
	if err := os.Rename(envTmp, filepath.Join(taskDir, envelopeFileName)); err != nil {
		_ = os.Remove(envTmp)
		return errResult("cannot commit envelope: " + err.Error())
	}

	// Rewrite the queued inbox file. If ENOENT (already consumed), surface
	// a too_late response — the race between the lock read above and the
	// rename here is the one the spec explicitly calls out.
	inboxDir := filepath.Join(s.instanceRoot, ".niwa", "roles", env.To.Role, "inbox")
	inboxPath := filepath.Join(inboxDir, args.TaskID+".json")
	if _, err := os.Stat(inboxPath); err != nil {
		if os.IsNotExist(err) {
			return textResult(`{"status":"too_late","current_state":"consumed"}`)
		}
	}
	msg := Message{
		V:      1,
		ID:     args.TaskID,
		Type:   "task.delegate",
		From:   MessageFrom{Role: env.From.Role, PID: env.From.PID},
		To:     MessageTo{Role: env.To.Role},
		TaskID: args.TaskID,
		SentAt: time.Now().UTC().Format(time.RFC3339),
		Body:   args.Body,
	}
	if errTR := writeMessageAtomic(inboxDir, args.TaskID, msg); errTR.IsError {
		return errTR
	}
	return textResult(`{"status":"updated"}`)
}

// --- niwa_cancel_task --------------------------------------------------------

func (s *Server) handleCancelTask(args cancelTaskArgs) toolResult {
	env, _, errR := authorizeTaskCall(s.identity(), args.TaskID, kindDelegator)
	if errR != nil {
		return *errR
	}

	taskDir := taskDirPath(s.instanceRoot, args.TaskID)
	inboxDir := filepath.Join(s.instanceRoot, ".niwa", "roles", env.To.Role, "inbox")
	src := filepath.Join(inboxDir, args.TaskID+".json")
	cancelledDir := filepath.Join(inboxDir, "cancelled")
	if err := os.MkdirAll(cancelledDir, 0o700); err != nil {
		return errResult("cannot create cancelled dir: " + err.Error())
	}
	dst := filepath.Join(cancelledDir, args.TaskID+".json")
	if err := os.Rename(src, dst); err != nil {
		if os.IsNotExist(err) {
			// Daemon already claimed the envelope. Return current state.
			if _, st, err := ReadState(taskDir); err == nil {
				return textResult(fmt.Sprintf(
					`{"status":"too_late","current_state":%q}`, st.State))
			}
			return textResult(`{"status":"too_late","current_state":"consumed"}`)
		}
		return errResult("cannot rename inbox file: " + err.Error())
	}

	// Transition state.json to cancelled.
	now := time.Now().UTC().Format(time.RFC3339)
	if err := UpdateState(taskDir, func(cur *TaskState) (*TaskState, *TransitionLogEntry, error) {
		next := *cur
		next.State = TaskStateCancelled
		next.UpdatedAt = now
		next.StateTransitions = append(next.StateTransitions,
			StateTransition{From: cur.State, To: TaskStateCancelled, At: now})
		entry := &TransitionLogEntry{
			Kind:  "state_transition",
			From:  cur.State,
			To:    TaskStateCancelled,
			At:    now,
			Actor: &TransitionActor{Kind: "delegator", PID: os.Getpid(), Role: s.role},
		}
		return &next, entry, nil
	}); err != nil {
		return mapStoreError(err)
	}
	return textResult(`{"status":"cancelled"}`)
}

// --- shared helpers ----------------------------------------------------------

// formatQueryResult renders a TaskState snapshot as the JSON payload returned
// by niwa_query_task / niwa_await_task.
func formatQueryResult(st *TaskState) string {
	out := map[string]any{
		"task_id":           st.TaskID,
		"state":             st.State,
		"state_transitions": st.StateTransitions,
		"restart_count":     st.RestartCount,
	}
	if st.LastProgress != nil {
		out["last_progress"] = st.LastProgress
	}
	if isTaskStateTerminal(st.State) {
		if len(st.Result) > 0 {
			out["result"] = st.Result
		}
		if len(st.Reason) > 0 {
			out["reason"] = st.Reason
		}
		if len(st.CancellationReason) > 0 {
			out["cancellation_reason"] = st.CancellationReason
		}
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// formatTerminalResult returns a {status, result|reason, restart_count} shape
// for niwa_await_task / sync niwa_delegate when the task is already terminal.
// max_restarts is included when restart_count > 0 so the coordinator can judge
// severity. last_progress is included when non-nil.
func formatTerminalResult(st *TaskState) toolResult {
	payload := map[string]any{
		"status":        st.State,
		"task_id":       st.TaskID,
		"restart_count": st.RestartCount,
	}
	if st.RestartCount > 0 {
		payload["max_restarts"] = st.MaxRestarts
	}
	if st.LastProgress != nil {
		payload["last_progress"] = st.LastProgress
	}
	switch st.State {
	case TaskStateCompleted:
		payload["result"] = st.Result
	case TaskStateAbandoned:
		payload["reason"] = st.Reason
	case TaskStateCancelled:
		if len(st.CancellationReason) > 0 {
			payload["cancellation_reason"] = st.CancellationReason
		}
	}
	b, _ := json.Marshal(payload)
	return textResult(string(b))
}

// formatEventResult renders a taskEvent delivered on an awaitWaiter channel.
// Fields unused by the event kind are omitted so callers see a minimal shape.
func formatEventResult(evt taskEvent, taskDir string) toolResult {
	// Prefer re-reading state.json so the response carries restart_count and
	// any daemon-written fields the event struct does not carry. Fall back
	// to the event's own fields if the read fails.
	if _, st, err := ReadState(taskDir); err == nil {
		return formatTerminalResult(st)
	}
	payload := map[string]any{
		"status":  evt.Kind.String(),
		"task_id": evt.TaskID,
	}
	if len(evt.Result) > 0 {
		payload["result"] = evt.Result
	}
	if len(evt.Reason) > 0 {
		payload["reason"] = evt.Reason
	}
	b, _ := json.Marshal(payload)
	return textResult(string(b))
}

// mapStoreError converts taskstore errors to tool-result shapes. Only
// ErrAlreadyTerminal has a matching PRD R50 code; ErrCorruptedState and
// ErrLockTimeout are internal/transient conditions that never appear in the
// R50 enumeration, so they map to plain errResult (no structured error_code)
// rather than being mis-labeled as an authorization failure.
func mapStoreError(err error) toolResult {
	switch err {
	case ErrAlreadyTerminal:
		return errResultCode("TASK_ALREADY_TERMINAL", "task already terminal")
	case ErrCorruptedState:
		return errResult("taskstore: corrupted state.json (schema validation failed)")
	case ErrLockTimeout:
		return errResult("taskstore: lock acquisition timed out")
	}
	return errResult("taskstore: " + err.Error())
}

// sendTaskMessage delivers a task-lifecycle message (task.progress,
// task.completed, task.abandoned) to a role's inbox. Best-effort: errors are
// swallowed so state.json remains authoritative and a transient inbox write
// failure does not poison the handler's success path.
func (s *Server) sendTaskMessage(toRole, msgType, taskID string, body any) {
	inboxDir := filepath.Join(s.instanceRoot, ".niwa", "roles", toRole, "inbox")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		return
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return
	}
	msgID := newUUID()
	msg := Message{
		V:      1,
		ID:     msgID,
		Type:   msgType,
		From:   MessageFrom{Role: s.role, PID: os.Getpid()},
		To:     MessageTo{Role: toRole},
		TaskID: taskID,
		SentAt: time.Now().UTC().Format(time.RFC3339),
		Body:   bodyBytes,
	}
	_ = writeMessageAtomic(inboxDir, msgID, msg)
}
