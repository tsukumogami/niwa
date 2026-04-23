package mcp

import (
	"encoding/json"
	"time"
)

// JSON-RPC wire types

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// MCP protocol types

type initializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	Capabilities    capabilities `json:"capabilities"`
	ServerInfo      serverInfo   `json:"serverInfo"`
}

type capabilities struct {
	Experimental map[string]any `json:"experimental,omitempty"`
	Tools        map[string]any `json:"tools,omitempty"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type toolsListResult struct {
	Tools []toolDef `json:"tools"`
}

type toolDef struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema inputSchema `json:"inputSchema"`
}

type inputSchema struct {
	Type       string                `json:"type"`
	Properties map[string]schemaProp `json:"properties,omitempty"`
	Required   []string              `json:"required,omitempty"`
}

type schemaProp struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type toolResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type channelNotificationParams struct {
	Source  string `json:"source"`
	Content string `json:"content"`
}

// Domain types

// Sessions directory layout under <instance-root>/.niwa/sessions/:
//
//	sessions.json              — SessionRegistry: all active sessions
//	<session-uuid>/
//	  inbox/                   — incoming message files (<msg-uuid>.json)
//	    read/                  — consumed messages (moved here by notifyNewFile for reply-awaited messages, TTL sweep for others)
type SessionEntry struct {
	ID              string `json:"id"`
	Role            string `json:"role"`
	Repo            string `json:"repo,omitempty"`
	PID             int    `json:"pid"`
	StartTime       int64  `json:"start_time"`
	InboxDir        string `json:"inbox_dir"`
	RegisteredAt    string `json:"registered_at"`
	ClaudeSessionID string `json:"claude_session_id,omitempty"`
}

type SessionRegistry struct {
	Sessions []SessionEntry `json:"sessions"`
}

type MessageFrom struct {
	Instance string `json:"instance,omitempty"`
	Role     string `json:"role"`
	Repo     string `json:"repo,omitempty"`
	PID      int    `json:"pid,omitempty"`
}

type MessageTo struct {
	Instance string `json:"instance,omitempty"`
	Role     string `json:"role"`
}

type Message struct {
	V         int             `json:"v"`
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	From      MessageFrom     `json:"from"`
	To        MessageTo       `json:"to"`
	ReplyTo   string          `json:"reply_to,omitempty"`
	TaskID    string          `json:"task_id,omitempty"`
	SentAt    string          `json:"sent_at"`
	ExpiresAt string          `json:"expires_at,omitempty"`
	Body      json.RawMessage `json:"body"`
}

type sendMessageArgs struct {
	To        string          `json:"to"`
	Type      string          `json:"type"`
	Body      json.RawMessage `json:"body"`
	ReplyTo   string          `json:"reply_to,omitempty"`
	TaskID    string          `json:"task_id,omitempty"`
	ExpiresAt string          `json:"expires_at,omitempty"`
}

type askArgs struct {
	To      string          `json:"to"`
	Body    json.RawMessage `json:"body"`
	Timeout int             `json:"timeout,omitempty"`
}

type waitArgs struct {
	Types   []string `json:"types,omitempty"`
	From    []string `json:"from,omitempty"`
	Count   int      `json:"count,omitempty"`
	Timeout int      `json:"timeout,omitempty"`
}

// Task domain types — Issue #1 task storage, types, and authorization.
//
// Directory layout for a task (see DESIGN Decision 1):
//
//	.niwa/tasks/<task-id>/
//	├── .lock            — zero-byte coordination file; per-task flock target
//	├── envelope.json    — TaskEnvelope (PRD R15 v=1); body mutable via niwa_update_task
//	├── state.json       — TaskState (authoritative state + audit-friendly fields)
//	└── transitions.log  — append-only NDJSON audit trail (one taskEvent per line)
//
// All writers coordinate under an exclusive flock on `.lock`; readers acquire a
// shared flock for consistent snapshots. See taskstore.go for the atomic update
// sequence.

// Task states. Terminal states are completed, abandoned, cancelled.
// Enumeration matches DESIGN Decision 1 / PRD R13.
const (
	TaskStateQueued    = "queued"
	TaskStateRunning   = "running"
	TaskStateCompleted = "completed"
	TaskStateAbandoned = "abandoned"
	TaskStateCancelled = "cancelled"
)

// validTaskStates enumerates every legal state.json.state value. The taskstore
// fails closed (ErrCorruptedState) when a read observes anything outside this
// set — a defensive check against manual edits or partial writes.
var validTaskStates = map[string]bool{
	TaskStateQueued:    true,
	TaskStateRunning:   true,
	TaskStateCompleted: true,
	TaskStateAbandoned: true,
	TaskStateCancelled: true,
}

// isTaskStateTerminal reports whether state corresponds to a terminal state.
// Terminal states cannot be mutated: niwa_finish_task on a terminal task
// returns TASK_ALREADY_TERMINAL per PRD R49.
func isTaskStateTerminal(state string) bool {
	switch state {
	case TaskStateCompleted, TaskStateAbandoned, TaskStateCancelled:
		return true
	}
	return false
}

// TaskParty is a role+pid tuple, as carried by envelope.from and envelope.to.
// PRD R15 requires role on both ends and a pid on `from`. `to.pid` is not
// required and is omitted here.
type TaskParty struct {
	Role string `json:"role"`
	PID  int    `json:"pid,omitempty"`
}

// TaskEnvelope is the delegation payload schema (PRD R15 v=1). Once written
// to `.niwa/tasks/<id>/envelope.json`, only `body` is mutable — and only via
// niwa_update_task while the task is still queued.
type TaskEnvelope struct {
	V            int             `json:"v"`
	ID           string          `json:"id"`
	From         TaskParty       `json:"from"`
	To           TaskParty       `json:"to"`
	Body         json.RawMessage `json:"body"`
	SentAt       string          `json:"sent_at"`
	ParentTaskID string          `json:"parent_task_id,omitempty"`
	DeadlineAt   string          `json:"deadline_at,omitempty"`
	ExpiresAt    string          `json:"expires_at,omitempty"`
}

// StateTransition is a single entry in TaskState.StateTransitions. Duplicated
// in transitions.log (as part of the NDJSON audit trail) so readers do not
// need to replay the log for simple state queries.
type StateTransition struct {
	From string `json:"from"` // empty string denotes the initial "(new)" pseudo-state
	To   string `json:"to"`
	At   string `json:"at"` // RFC3339
}

// TaskWorker captures the daemon-owned worker metadata for an in-flight task.
// Authorization (kindExecutor on Linux) cross-checks caller PPID + start_time
// against these fields.
type TaskWorker struct {
	PID            int    `json:"pid"`
	StartTime      int64  `json:"start_time"`
	Role           string `json:"role"`
	SpawnStartedAt string `json:"spawn_started_at,omitempty"`
	AdoptedAt      string `json:"adopted_at,omitempty"`
}

// TaskProgress is the most recent progress event summary. The `body` field is
// intentionally NOT stored in state.json for the security-critical
// progress-summary redaction: only the 200-char summary persists across
// restarts. Full bodies live in transitions.log are not re-read.
type TaskProgress struct {
	Summary string `json:"summary"`
	At      string `json:"at"` // RFC3339
}

// TaskState is the authoritative task state (DESIGN Decision 1 schema v=1).
// `result`, `reason`, and `cancellation_reason` are set only on the matching
// terminal transition; JSON omitempty keeps non-terminal state.json concise.
type TaskState struct {
	V                  int               `json:"v"`
	TaskID             string            `json:"task_id"`
	State              string            `json:"state"`
	StateTransitions   []StateTransition `json:"state_transitions"`
	RestartCount       int               `json:"restart_count"`
	MaxRestarts        int               `json:"max_restarts"`
	LastProgress       *TaskProgress     `json:"last_progress,omitempty"`
	Worker             TaskWorker        `json:"worker"`
	DelegatorRole      string            `json:"delegator_role"`
	TargetRole         string            `json:"target_role"`
	Result             json.RawMessage   `json:"result,omitempty"`
	Reason             json.RawMessage   `json:"reason,omitempty"`
	CancellationReason json.RawMessage   `json:"cancellation_reason,omitempty"`
	UpdatedAt          string            `json:"updated_at"`
}

// TaskEventKind discriminates in-process task events carried on daemon and
// MCP waiter channels. The values also tag NDJSON lines in transitions.log
// (via the `kind` field).
type TaskEventKind int

const (
	// EvtCompleted — worker called niwa_finish_task with outcome=completed.
	EvtCompleted TaskEventKind = iota
	// EvtAbandoned — worker called niwa_finish_task with outcome=abandoned,
	// or the daemon abandoned the task after retry-cap exhaustion.
	EvtAbandoned
	// EvtCancelled — delegator called niwa_cancel_task.
	EvtCancelled
	// EvtProgress — non-terminal progress update. Consumed by the daemon
	// supervisor; ignored by awaitWaiters.
	EvtProgress
	// EvtUnexpectedExit — worker process exited without a terminal
	// transition. Daemon-supervisor only.
	EvtUnexpectedExit
	// EvtAdopted — crash-recovery reconciliation found a live orphan.
	EvtAdopted
)

// String returns the wire/log name for a TaskEventKind. These names appear
// in transitions.log's `kind` field; they are part of the file format.
func (k TaskEventKind) String() string {
	switch k {
	case EvtCompleted:
		return "completed"
	case EvtAbandoned:
		return "abandoned"
	case EvtCancelled:
		return "cancelled"
	case EvtProgress:
		return "progress"
	case EvtUnexpectedExit:
		return "unexpected_exit"
	case EvtAdopted:
		return "adoption"
	}
	return "unknown"
}

// taskEvent is carried over in-process channels (daemon taskEvent channel,
// MCP awaitWaiters). It is the Go-side projection of a transitions.log entry
// — serialized shape on disk may include additional fields via transitionEntry.
type taskEvent struct {
	TaskID   string
	Kind     TaskEventKind
	ExitCode int             // valid when Kind == EvtUnexpectedExit
	Result   json.RawMessage // valid when Kind == EvtCompleted
	Reason   json.RawMessage // valid when Kind == EvtAbandoned
	At       time.Time
}

// TransitionLogEntry is an append-only NDJSON record written to
// transitions.log. The v=1 schema matches DESIGN Decision 1.
//
// Security note: progress entries intentionally store only `summary`, never
// `body`. This is the body-redaction guarantee that keeps transitions.log
// safe to include in bug reports (PRD Known Limitation — the log can still
// contain result/reason terminal payloads, which is documented separately).
type TransitionLogEntry struct {
	V         int              `json:"v"`
	Kind      string           `json:"kind"`
	At        string           `json:"at"` // RFC3339
	From      string           `json:"from,omitempty"`
	To        string           `json:"to,omitempty"`
	Summary   string           `json:"summary,omitempty"`
	WorkerPID int              `json:"worker_pid,omitempty"`
	ExitCode  *int             `json:"exit_code,omitempty"`
	Signal    string           `json:"signal,omitempty"`
	Attempt   int              `json:"attempt,omitempty"`
	Result    json.RawMessage  `json:"result,omitempty"`
	Reason    json.RawMessage  `json:"reason,omitempty"`
	Actor     *TransitionActor `json:"actor,omitempty"`
}

// TransitionActor identifies who appended a transitions.log entry.
type TransitionActor struct {
	Kind string `json:"kind"` // "daemon", "worker", "delegator"
	PID  int    `json:"pid,omitempty"`
	Role string `json:"role,omitempty"`
}
