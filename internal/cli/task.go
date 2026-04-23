package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/mcp"
)

// taskCmd is the root of the `niwa task` subcommand group. Subcommands
// expose read-only views over the per-task state machine files under
// `.niwa/tasks/<id>/`. Every read funnels through mcp.ReadState so the
// shared flock discipline matches the daemon and MCP tool handlers.
var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "Inspect tasks in the workspace mesh",
	Long: `Inspect tasks in the workspace mesh.

Subcommands:
  list    List tasks with optional role/state/delegator/since filters
  show    Show envelope, current state, and transitions for one task`,
}

var (
	taskListStateFlag     string
	taskListRoleFlag      string
	taskListDelegatorFlag string
	taskListSinceFlag     string
)

var taskListCmd = &cobra.Command{
	Use:   "list",
	Short: "List tasks under .niwa/tasks/",
	Long: `List tasks under .niwa/tasks/.

Filters combine with AND semantics — a task must satisfy every provided
filter to appear in the output. --since takes a Go duration string
(e.g. "30m", "1h", "24h") and matches tasks whose envelope.sent_at is
newer than the cutoff.`,
	RunE: runTaskList,
}

var taskShowCmd = &cobra.Command{
	Use:   "show <task-id>",
	Short: "Show envelope, state, and transitions for a task",
	Args:  cobra.ExactArgs(1),
	RunE:  runTaskShow,
}

func init() {
	rootCmd.AddCommand(taskCmd)
	taskCmd.AddCommand(taskListCmd)
	taskCmd.AddCommand(taskShowCmd)

	taskListCmd.Flags().StringVar(&taskListStateFlag, "state", "",
		"filter by task state (queued, running, completed, abandoned, cancelled)")
	taskListCmd.Flags().StringVar(&taskListRoleFlag, "role", "",
		"filter by target role (envelope.to.role)")
	taskListCmd.Flags().StringVar(&taskListDelegatorFlag, "delegator", "",
		"filter by delegator role (envelope.from.role)")
	taskListCmd.Flags().StringVar(&taskListSinceFlag, "since", "",
		"filter to tasks newer than this Go duration (e.g. 30m, 1h, 24h)")
}

// runTaskList enumerates task directories and prints a table. The
// directory under which tasks live is derived by walking up from cwd to
// find an instance root; when none is found the command exits with an
// explanatory error so users running from outside an instance get a
// clear message instead of an empty table.
func runTaskList(cmd *cobra.Command, args []string) error {
	tasksDir, err := resolveTasksDir()
	if err != nil {
		return err
	}

	var sinceCutoff time.Time
	if taskListSinceFlag != "" {
		d, err := time.ParseDuration(taskListSinceFlag)
		if err != nil {
			return fmt.Errorf("invalid --since duration %q: %w", taskListSinceFlag, err)
		}
		sinceCutoff = time.Now().Add(-d)
	}

	rows, err := collectTaskRows(tasksDir)
	if err != nil {
		return err
	}

	filtered := rows[:0]
	for _, r := range rows {
		if taskListStateFlag != "" && r.state != taskListStateFlag {
			continue
		}
		if taskListRoleFlag != "" && r.targetRole != taskListRoleFlag {
			continue
		}
		if taskListDelegatorFlag != "" && r.delegatorRole != taskListDelegatorFlag {
			continue
		}
		if !sinceCutoff.IsZero() && r.sentAt.Before(sinceCutoff) {
			continue
		}
		filtered = append(filtered, r)
	}

	// Sort newest-first by envelope.sent_at so operators see recent
	// activity without scrolling. Stable by task ID on tie.
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].sentAt.Equal(filtered[j].sentAt) {
			return filtered[i].taskID < filtered[j].taskID
		}
		return filtered[i].sentAt.After(filtered[j].sentAt)
	})

	writeTaskTable(cmd.OutOrStdout(), filtered)
	return nil
}

// taskRow is the projected view for a single row in `niwa task list`.
// Carrying the envelope sent_at as a time.Time (not a formatted string)
// lets the row-level filter and sort share parsing with the display
// helper and keeps the --since window arithmetic obvious.
type taskRow struct {
	taskID        string
	targetRole    string
	state         string
	restartCount  int
	sentAt        time.Time
	delegatorRole string
	bodySummary   string
}

// collectTaskRows reads every <tasksDir>/<id>/ entry via mcp.ReadState
// so the shared flock discipline applies to the CLI too. Corrupted or
// partially-written task directories are skipped with a stderr warning
// rather than aborting the whole listing — one bad task should not
// hide the other N.
func collectTaskRows(tasksDir string) ([]taskRow, error) {
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read tasks dir: %w", err)
	}

	var rows []taskRow
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		taskDir := filepath.Join(tasksDir, e.Name())
		env, st, err := mcp.ReadState(taskDir)
		if err != nil {
			// Corrupt / partial / symlink / flock-timeout. Skip silently;
			// a human can run `niwa task show` on the ID to see the raw
			// error.
			continue
		}
		sent, _ := time.Parse(time.RFC3339, env.SentAt)
		rows = append(rows, taskRow{
			taskID:        st.TaskID,
			targetRole:    env.To.Role,
			state:         st.State,
			restartCount:  st.RestartCount,
			sentAt:        sent,
			delegatorRole: env.From.Role,
			bodySummary:   summarizeBody(env.Body),
		})
	}
	return rows, nil
}

// summarizeBody renders envelope.body as a single-line, 200-character
// truncated summary. JSON is re-marshaled without indentation so multi-
// line bodies collapse to one line; non-JSON or malformed content is
// returned verbatim after newline collapse so the operator can still
// see something useful.
func summarizeBody(body json.RawMessage) string {
	if len(body) == 0 {
		return ""
	}
	var generic any
	s := string(body)
	if err := json.Unmarshal(body, &generic); err == nil {
		if compact, err := json.Marshal(generic); err == nil {
			s = string(compact)
		}
	}
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.TrimSpace(s)
	const maxLen = 200
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	return s
}

// writeTaskTable prints the task list with columns aligned to the
// existing `niwa status` convention (two leading spaces, fixed-width
// columns, body summary in the final variable-width column).
func writeTaskTable(out io.Writer, rows []taskRow) {
	header := fmt.Sprintf("  %-10s %-14s %-10s %-8s %-8s %-14s %s",
		"TASK", "TARGET", "STATE", "RESTART", "AGE", "DELEGATOR", "BODY")
	fmt.Fprintln(out, header)
	if len(rows) == 0 {
		return
	}
	for _, r := range rows {
		age := "-"
		if !r.sentAt.IsZero() {
			age = formatRelativeTime(r.sentAt)
		}
		fmt.Fprintf(out, "  %-10s %-14s %-10s %-8d %-8s %-14s %s\n",
			shortTaskID(r.taskID),
			r.targetRole,
			r.state,
			r.restartCount,
			age,
			r.delegatorRole,
			r.bodySummary,
		)
	}
}

// shortTaskID truncates a UUIDv4 to its first 8 characters for the
// list view. `niwa task show` accepts the full ID; this helper is
// presentation-only.
func shortTaskID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// runTaskShow prints a human-readable report for a single task: the
// envelope summary, current state, and the full transitions log
// chronologically. Unknown IDs exit non-zero with a stderr message
// so scripts can detect missing tasks.
func runTaskShow(cmd *cobra.Command, args []string) error {
	taskID := args[0]
	tasksDir, err := resolveTasksDir()
	if err != nil {
		return err
	}
	taskDir := filepath.Join(tasksDir, taskID)
	if _, err := os.Stat(taskDir); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "task not found: %s\n", taskID)
		return fmt.Errorf("task not found: %s", taskID)
	}

	env, st, err := mcp.ReadState(taskDir)
	if err != nil {
		return fmt.Errorf("reading task %s: %w", taskID, err)
	}

	out := cmd.OutOrStdout()
	writeTaskShowEnvelope(out, env)
	fmt.Fprintln(out)
	writeTaskShowState(out, st)
	fmt.Fprintln(out)
	if err := writeTaskShowTransitions(out, taskDir); err != nil {
		return err
	}
	return nil
}

func writeTaskShowEnvelope(out io.Writer, env *mcp.TaskEnvelope) {
	fmt.Fprintln(out, "Envelope:")
	fmt.Fprintf(out, "  task_id:    %s\n", env.ID)
	fmt.Fprintf(out, "  from:       role=%s pid=%d\n", env.From.Role, env.From.PID)
	fmt.Fprintf(out, "  to:         role=%s\n", env.To.Role)
	fmt.Fprintf(out, "  sent_at:    %s\n", env.SentAt)
	if env.ParentTaskID != "" {
		fmt.Fprintf(out, "  parent:     %s\n", env.ParentTaskID)
	}
	if env.DeadlineAt != "" {
		fmt.Fprintf(out, "  deadline:   %s\n", env.DeadlineAt)
	}
	if env.ExpiresAt != "" {
		fmt.Fprintf(out, "  expires_at: %s\n", env.ExpiresAt)
	}
	fmt.Fprintln(out, "  body:")
	fmt.Fprintln(out, indent(prettyJSON(env.Body), "    "))
}

func writeTaskShowState(out io.Writer, st *mcp.TaskState) {
	fmt.Fprintln(out, "State:")
	fmt.Fprintf(out, "  state:         %s\n", st.State)
	fmt.Fprintf(out, "  restart_count: %d (max %d)\n", st.RestartCount, st.MaxRestarts)
	fmt.Fprintf(out, "  updated_at:    %s\n", st.UpdatedAt)
	if st.Worker.PID != 0 {
		fmt.Fprintf(out, "  worker:        role=%s pid=%d\n", st.Worker.Role, st.Worker.PID)
	}
	if st.LastProgress != nil && st.LastProgress.Summary != "" {
		fmt.Fprintf(out, "  last_progress: %s (at %s)\n",
			st.LastProgress.Summary, st.LastProgress.At)
	}
	if len(st.Result) > 0 {
		fmt.Fprintln(out, "  result:")
		fmt.Fprintln(out, indent(prettyJSON(st.Result), "    "))
	}
	if len(st.Reason) > 0 {
		fmt.Fprintln(out, "  reason:")
		fmt.Fprintln(out, indent(prettyJSON(st.Reason), "    "))
	}
	if len(st.CancellationReason) > 0 {
		fmt.Fprintln(out, "  cancellation_reason:")
		fmt.Fprintln(out, indent(prettyJSON(st.CancellationReason), "    "))
	}
}

// writeTaskShowTransitions streams transitions.log as one human-readable
// line per entry, chronologically (append order). Unknown or malformed
// lines are rendered verbatim so the audit trail stays visible even
// after format-schema drift.
func writeTaskShowTransitions(out io.Writer, taskDir string) error {
	path := filepath.Join(taskDir, "transitions.log")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(out, "Transitions: (none)")
			return nil
		}
		return fmt.Errorf("read transitions.log: %w", err)
	}
	fmt.Fprintln(out, "Transitions:")
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		var entry mcp.TransitionLogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			fmt.Fprintf(out, "  (unparseable) %s\n", line)
			continue
		}
		fmt.Fprintln(out, "  "+formatTransitionEntry(entry))
	}
	return nil
}

// formatTransitionEntry is the single-line renderer for a transitions.log
// record. It intentionally omits the full result/reason bodies: those
// appear in `niwa task show`'s state block instead, where they have
// space to render as pretty-printed JSON.
func formatTransitionEntry(e mcp.TransitionLogEntry) string {
	var b strings.Builder
	b.WriteString(e.At)
	b.WriteString(" kind=")
	b.WriteString(e.Kind)
	if e.From != "" || e.To != "" {
		b.WriteString(fmt.Sprintf(" transition=%s->%s", e.From, e.To))
	}
	if e.WorkerPID != 0 {
		b.WriteString(fmt.Sprintf(" pid=%d", e.WorkerPID))
	}
	if e.ExitCode != nil {
		b.WriteString(fmt.Sprintf(" exit=%d", *e.ExitCode))
	}
	if e.Signal != "" {
		b.WriteString(" signal=" + e.Signal)
	}
	if e.Attempt != 0 {
		b.WriteString(fmt.Sprintf(" attempt=%d", e.Attempt))
	}
	if e.Summary != "" {
		b.WriteString(" summary=" + e.Summary)
	}
	if e.Actor != nil {
		b.WriteString(fmt.Sprintf(" actor=%s", e.Actor.Kind))
		if e.Actor.Role != "" {
			b.WriteString("/" + e.Actor.Role)
		}
	}
	return b.String()
}

// prettyJSON re-marshals a json.RawMessage with indent, falling back to
// the raw string when the payload is not valid JSON. Callers rely on
// the result already ending with a newline-free string; no trailing
// newline is appended.
func prettyJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "(none)"
	}
	var out bytes.Buffer
	if err := json.Indent(&out, raw, "", "  "); err != nil {
		return string(raw)
	}
	return out.String()
}

// indent prefixes every line of s with prefix. Used to nest
// pretty-printed JSON under a labeled section header.
func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// resolveTasksDir finds the .niwa/tasks directory for the current
// instance. Priority: NIWA_INSTANCE_ROOT env var (so tests and
// hooks can inject), otherwise walk up from cwd to the nearest
// instance root. Returns an error with a clear message when no
// instance is found so scripted callers don't mistake the empty-
// listing case for "no tasks".
func resolveTasksDir() (string, error) {
	instanceRoot := os.Getenv("NIWA_INSTANCE_ROOT")
	if instanceRoot == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("getting working directory: %w", err)
		}
		dir, err := discoverInstanceRoot(cwd)
		if err != nil {
			return "", err
		}
		instanceRoot = dir
	}
	return filepath.Join(instanceRoot, ".niwa", "tasks"), nil
}

// discoverInstanceRoot walks up from startDir to find the nearest
// directory containing .niwa/instance.json. Mirrors
// workspace.DiscoverInstance but avoids the circular import (cli
// already imports workspace via status.go; this local copy keeps the
// task command self-contained and lets tests override via
// NIWA_INSTANCE_ROOT without running an apply first).
func discoverInstanceRoot(startDir string) (string, error) {
	abs, err := filepath.Abs(startDir)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	dir := abs
	for {
		if _, err := os.Stat(filepath.Join(dir, ".niwa", "instance.json")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("not inside a workspace instance (no .niwa/instance.json found walking up from %s)", startDir)
		}
		dir = parent
	}
}
