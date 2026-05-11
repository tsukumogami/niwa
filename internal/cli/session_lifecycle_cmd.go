package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/mcp"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func init() {
	sessionCmd.AddCommand(sessionCreateCmd)
	sessionCmd.AddCommand(sessionDestroyCmd)
}

var sessionCreateCmd = &cobra.Command{
	Use:   "create <repo> <purpose>",
	Short: "Create a new git-worktree session for a repo",
	Long: `Create a new git-worktree session for a repo.

Scaffolds a git worktree under .niwa/worktrees/<repo>-<session-id>/,
writes the session lifecycle state, and starts the per-worktree daemon.

On success the shell wrapper navigates to the new worktree directory.`,
	Args: cobra.ExactArgs(2),
	RunE: runSessionCreate,
}

var sessionDestroyCmd = &cobra.Command{
	Use:   "destroy <session-id>",
	Short: "Destroy a session and remove its worktree",
	Long: `Destroy a session: kill running workers, mark the session ended,
stop the per-worktree daemon, remove the worktree, and delete the session
branch (only if already merged; use --force to delete regardless).`,
	Args: cobra.ExactArgs(1),
	RunE: runSessionDestroy,
}

var sessionDestroyForce bool

func init() {
	sessionDestroyCmd.Flags().BoolVar(&sessionDestroyForce, "force", false, "Delete session branch even if it has unmerged commits")
}

func runSessionCreate(cmd *cobra.Command, args []string) error {
	instanceRoot, err := resolveInstanceRoot()
	if err != nil {
		return err
	}
	repo := args[0]
	purpose := args[1]

	srv := mcp.New("coordinator", instanceRoot)
	srv.SetDaemonFuncs(makeDaemonStarter(), workspace.TerminateDaemon)

	result := srv.CreateSessionDirect(repo, purpose, "")
	if result.IsError {
		return renderMCPError(result.Content[0].Text)
	}

	var resp struct {
		SessionID    string `json:"session_id"`
		WorktreePath string `json:"worktree_path"`
		DaemonWarn   string `json:"daemon_warning,omitempty"`
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), &resp); err != nil {
		return fmt.Errorf("parsing session response: %w", err)
	}
	if resp.DaemonWarn != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), "warning:", resp.DaemonWarn)
	}
	// Issue 10: success summary on stdout so callers can pipe it.
	// Landing-path delivery uses NIWA_RESPONSE_FILE separately; the
	// shell wrapper's stdout-cd target is unaffected.
	fmt.Fprintf(cmd.OutOrStdout(), "session: created %s at %s\n", resp.SessionID, resp.WorktreePath)

	if err := validateLandingPath(resp.WorktreePath); err != nil {
		return err
	}
	if err := writeLandingPath(resp.WorktreePath); err != nil {
		return err
	}
	hintShellInit(cmd)
	return nil
}

func runSessionDestroy(cmd *cobra.Command, args []string) error {
	instanceRoot, err := resolveInstanceRoot()
	if err != nil {
		return err
	}
	sessionID := args[0]

	srv := mcp.New("coordinator", instanceRoot)
	srv.SetDaemonFuncs(makeDaemonStarter(), workspace.TerminateDaemon)

	result := srv.DestroySessionDirect(sessionID, sessionDestroyForce)
	if result.IsError {
		return renderMCPError(result.Content[0].Text)
	}

	var resp struct {
		BranchWarn string `json:"branch_warning,omitempty"`
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), &resp); err != nil {
		return fmt.Errorf("parsing destroy response: %w", err)
	}
	if resp.BranchWarn != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), "warning:", resp.BranchWarn)
	}

	// Issue 10: destroy success on stdout, matching create.
	fmt.Fprintf(cmd.OutOrStdout(), "session: destroyed %s\n", sessionID)
	return nil
}

// runSessionLifecycleList lists per-session lifecycle states, filtering by
// repo, status, daemon health (via --json/--verbose surfaces), and attach
// availability. Called by sessionListCmd when at least one filter flag is
// present.
//
// Each row's AVAILABILITY value is projected from the per-worktree
// attach.state sentinel via mcp.ReadAttachState (with reapStale=true so
// the listing pass naturally cleans up dead-holder sentinels). Each row's
// DAEMON value comes from mcp.DaemonHealthFor (reads daemon.pid + checks
// liveness).
//
// Sort order matches PRD R17: attached first (the operator's hot question
// "is anyone in there?"), then daemon-alive first, then active before
// terminal status, then creation_time descending.
func runSessionLifecycleList(cmd *cobra.Command, repo, status string, onlyAttached, onlyAvailable bool) error {
	if onlyAttached && onlyAvailable {
		return fmt.Errorf("--attached and --available are mutually exclusive")
	}
	instanceRoot, err := resolveInstanceRoot()
	if err != nil {
		return err
	}

	sessionsDir := filepath.Join(instanceRoot, ".niwa", "sessions")
	all, err := mcp.ListSessionLifecycleStates(sessionsDir)
	if err != nil {
		return fmt.Errorf("listing sessions: %w", err)
	}

	rows := make([]lifecycleRow, 0, len(all))
	for _, st := range all {
		if repo != "" && st.Repo != repo {
			continue
		}
		if status != "" && st.Status != status {
			continue
		}
		attachState, avail, _ := mcp.ReadAttachState(st.WorktreePath, true /* reap dead-holder sentinels */)
		if onlyAttached && avail != mcp.AttachAttached {
			continue
		}
		if onlyAvailable && avail != mcp.AttachAvailable {
			continue
		}
		// Project the live attach state onto the embedded
		// SessionLifecycleState so the CLI JSON wire shape matches what
		// niwa_list_sessions emits: same struct, same `attach` key
		// shape, absent when no live lock is held.
		if avail == mcp.AttachAttached && attachState != nil {
			st.Attach = attachState
		}
		rows = append(rows, lifecycleRow{
			state:  st,
			avail:  avail,
			daemon: mcp.DaemonHealthFor(st.WorktreePath),
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return rowSortLess(rows[i], rows[j])
	})

	if sessionListJSON {
		// JSON mode: emit a fresh array (not null) when empty. The wire
		// shape matches niwa_list_sessions for the `attach` key (full
		// AttachState struct from the embedded SessionLifecycleState,
		// absent when no live lock). The `daemon` and `availability`
		// keys are CLI-specific projections.
		out := cmd.OutOrStdout()
		jsonRows := make([]sessionListJSONRow, 0, len(rows))
		for _, r := range rows {
			jsonRows = append(jsonRows, sessionListJSONRow{
				SessionLifecycleState: r.state,
				Daemon:                r.daemon,
				Availability:          availabilityForTable(r.avail),
			})
		}
		if len(jsonRows) == 0 {
			fmt.Fprintln(out, "[]")
			return nil
		}
		data, err := json.MarshalIndent(jsonRows, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal sessions: %w", err)
		}
		_, _ = out.Write(data)
		fmt.Fprintln(out)
		return nil
	}

	writeSessionLifecycleTable(cmd.OutOrStdout(), rows, sessionListVerbose)
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "(no sessions match the current filter)")
	}
	return nil
}

// rowSortLess implements PRD R17's composite sort:
//  1. attached first (the operator's hot question: "is anyone in there?")
//  2. daemon-alive before dead (so live sessions sort up within each
//     attach bucket)
//  3. status: active before terminal
//  4. creation_time descending (newest first)
func rowSortLess(a, b lifecycleRow) bool {
	// Key 1: attached < others.
	aAttached := a.avail == mcp.AttachAttached
	bAttached := b.avail == mcp.AttachAttached
	if aAttached != bAttached {
		return aAttached // true sorts first
	}
	// Key 2: daemon alive < dead.
	if a.daemon.Alive != b.daemon.Alive {
		return a.daemon.Alive
	}
	// Key 3: active < ended < abandoned.
	if a.state.Status != b.state.Status {
		return statusRank(a.state.Status) < statusRank(b.state.Status)
	}
	// Key 4: creation_time descending (newer first).
	return a.state.CreationTime > b.state.CreationTime
}

func statusRank(s string) int {
	switch s {
	case mcp.SessionStatusActive:
		return 0
	case mcp.SessionStatusEnded:
		return 1
	case mcp.SessionStatusAbandoned:
		return 2
	default:
		return 3
	}
}

// availabilityForTable reduces an AttachAvailability to one of the three
// values rendered in the AVAILABILITY column.
func availabilityForTable(a mcp.AttachAvailability) string {
	switch a {
	case mcp.AttachAttached:
		return string(mcp.AttachAttached)
	case mcp.AttachStale:
		return string(mcp.AttachStale)
	default:
		return string(mcp.AttachAvailable)
	}
}

// lifecycleRow bundles a persisted SessionLifecycleState with the two
// computed projections needed at table-render time: attach availability
// (from .niwa/attach.state) and daemon health (from .niwa/daemon.pid).
// Neither projection is persisted; both are read on every list.
type lifecycleRow struct {
	state  mcp.SessionLifecycleState
	avail  mcp.AttachAvailability
	daemon mcp.DaemonHealth
}

// sessionListJSONRow is the wire shape returned by
// `niwa session list --json`. The `attach` key (via the embedded
// SessionLifecycleState.Attach pointer field) matches what
// niwa_list_sessions emits exactly: the full AttachState struct when a
// live lock is held, absent otherwise. The `daemon` sub-object comes
// from PR #115 (also computed, not persisted). The `availability` key
// is a CLI-side projection that lets callers distinguish `stale`
// (sentinel present but reaped) from `available` (no sentinel) without
// having to walk PIDs themselves.
type sessionListJSONRow struct {
	mcp.SessionLifecycleState
	Daemon       mcp.DaemonHealth `json:"daemon"`
	Availability string           `json:"availability"`
}

func writeSessionLifecycleTable(out interface{ Write([]byte) (int, error) }, rows []lifecycleRow, verbose bool) {
	if verbose {
		fmt.Fprintf(out, "  %-12s %-12s %-10s %-7s %-12s %-8s %-20s %-20s %s\n",
			"SESSION_ID", "REPO", "STATUS", "DAEMON", "AVAILABILITY", "PID", "STARTED-AT", "CREATED", "PURPOSE")
	} else {
		fmt.Fprintf(out, "  %-12s %-12s %-10s %-7s %-12s %-20s %s\n",
			"SESSION_ID", "REPO", "STATUS", "DAEMON", "AVAILABILITY", "CREATED", "PURPOSE")
	}
	for _, r := range rows {
		s := r.state
		created := "-"
		if s.CreationTime != "" {
			if t, err := time.Parse(time.RFC3339, s.CreationTime); err == nil {
				created = formatRelativeTime(t)
			}
		}
		purpose := s.Purpose
		if len(purpose) > 40 {
			purpose = purpose[:37] + "..."
		}
		daemonState := "dead"
		if r.daemon.Alive {
			daemonState = "alive"
		}
		availability := availabilityForTable(r.avail)
		if verbose {
			pidStr := "-"
			if r.daemon.PID > 0 {
				pidStr = fmt.Sprintf("%d", r.daemon.PID)
			}
			startedAt := r.daemon.StartedAt
			if startedAt == "" {
				startedAt = "-"
			}
			fmt.Fprintf(out, "  %-12s %-12s %-10s %-7s %-12s %-8s %-20s %-20s %s\n",
				s.SessionID, s.Repo, s.Status, daemonState, availability, pidStr, startedAt, created, purpose)
		} else {
			fmt.Fprintf(out, "  %-12s %-12s %-10s %-7s %-12s %-20s %s\n",
				s.SessionID, s.Repo, s.Status, daemonState, availability, created, purpose)
		}
	}
}
