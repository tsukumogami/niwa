package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(sessionCmd)
	sessionCmd.AddCommand(sessionListCmd)
}

var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Manage sessions in the workspace mesh",
	Long: `Manage sessions in the workspace mesh.

Subcommands:
  attach    Attach to a session interactively (resume claude with full transcript)
  create    Create a new git-worktree session for a repo
  destroy   Destroy a session and remove its worktree
  detach    Release a stale attach lock (operator escape hatch)
  list      List session lifecycle states with availability projection`,
}

// sessionListCmd lists per-session lifecycle states. Filter flags --repo,
// --status, --attached, --available all AND-combine. The flagless default
// shows every session in the current instance.
//
// Earlier versions of niwa fell through to `niwa mesh list` (the
// coordinator process registry) when invoked without flags, with a
// deprecation warning. That alias was removed in PLAN issue 10 once the
// AVAILABILITY column landed: the issue's UX sketch is incompatible with
// the alias being default. Operators wanting the coordinator process
// registry call `niwa mesh list` directly.
var sessionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List session lifecycle states with availability projection",
	Long: `List per-session lifecycle states.

Renders SESSION_ID, REPO, STATUS, AVAILABILITY, CREATED, PURPOSE for every
session in the current workspace instance. AVAILABILITY values are:

  available  no attach lock held; the session is free for niwa session attach
  attached   currently held by a niwa session attach process
  stale      a sentinel exists but the holder is dead; the lock is no longer
             effective and the next read will reap it

Filter flags AND-combine: --repo, --status, --attached, --available.
--attached and --available are mutually exclusive. Sessions with
AVAILABILITY=stale appear under neither filter; run without filters to
see them.

For the coordinator process registry view, use 'niwa mesh list' directly.`,
	RunE:          runSessionList,
	SilenceErrors: true,
	SilenceUsage:  true,
}

var (
	sessionListRepo      string
	sessionListStatus    string
	sessionListAttached  bool
	sessionListAvailable bool
	sessionListJSON      bool
	sessionListVerbose   bool
)

func init() {
	sessionListCmd.Flags().StringVar(&sessionListRepo, "repo", "", "Filter by repo name")
	sessionListCmd.Flags().StringVar(&sessionListStatus, "status", "", "Filter by status: active, ended, abandoned")
	sessionListCmd.Flags().BoolVar(&sessionListAttached, "attached", false, "Show only sessions currently held by an attach lock")
	sessionListCmd.Flags().BoolVar(&sessionListAvailable, "available", false, "Show only sessions with no attach lock held")
	sessionListCmd.Flags().BoolVar(&sessionListJSON, "json", false, "Output JSON (one object per session, including daemon and attach sub-objects) instead of a table")
	sessionListCmd.Flags().BoolVar(&sessionListVerbose, "verbose", false, "Include PID and STARTED_AT columns in the table view")
}

func runSessionList(cmd *cobra.Command, _ []string) error {
	return runSessionLifecycleList(cmd, sessionListRepo, sessionListStatus, sessionListAttached, sessionListAvailable)
}

// countPendingInbox counts JSON envelopes directly under
// .niwa/roles/<role>/inbox/. Subdirectories (in-progress, cancelled,
// expired, read) represent already-processed states and are excluded.
// Non-existent inboxes (role has no provisioned inbox yet) contribute
// 0 rather than erroring so a registry row with a missing inbox still
// prints.
func countPendingInbox(instanceRoot, role string) int {
	inboxDir := filepath.Join(instanceRoot, ".niwa", "roles", role, "inbox")
	entries, err := os.ReadDir(inboxDir)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		count++
	}
	return count
}

// resolveInstanceRoot returns the absolute path of the current instance
// root. Priority: NIWA_INSTANCE_ROOT env var, then walk up from cwd to
// find .niwa/instance.json.
func resolveInstanceRoot() (string, error) {
	if root := os.Getenv("NIWA_INSTANCE_ROOT"); root != "" {
		return root, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getting working directory: %w", err)
	}
	return discoverInstanceRoot(cwd)
}
