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

// deprecatedSessionAlias is the legacy parent command name. Invoking any
// subcommand via `niwa session ...` still resolves to the canonical
// `worktree` commands (via the Aliases below) and prints a one-line
// deprecation notice to stderr without altering behavior or exit code.
const deprecatedSessionAlias = "session"

// sessionCmd is the canonical `worktree` parent command. It keeps the
// historical "session" name as an alias so existing scripts keep working;
// the variable name is retained to minimize churn across the package.
var sessionCmd = &cobra.Command{
	Use:     "worktree",
	Aliases: []string{deprecatedSessionAlias},
	Short:   "Manage git worktrees in the workspace",
	Long: `Manage git worktrees in the workspace.

Subcommands:
  attach    Attach to a worktree interactively (resume claude with full transcript)
  create    Create a new git worktree for a repo
  destroy   Destroy a worktree and remove its working directory
  detach    Release a stale attach lock (operator escape hatch)
  list      List worktree lifecycle states with availability projection`,
	// PersistentPreRun fires for the parent and every subcommand. When the
	// command was reached via the legacy "session" token on the command
	// line, emit a deprecation notice to stderr. Behavior and exit code are
	// unchanged; this is informational only.
	PersistentPreRun: func(cmd *cobra.Command, _ []string) {
		if invokedViaSessionAlias() {
			fmt.Fprintln(cmd.ErrOrStderr(),
				`"niwa session" is deprecated; use "niwa worktree"`)
		}
	},
}

// invokedViaSessionAlias reports whether the legacy "session" token appears
// in os.Args before any "--" terminator. The canonical "worktree" token, if
// present, takes precedence: a literal `niwa worktree ...` invocation never
// triggers the notice even in the unlikely case a later argument is the word
// "session".
func invokedViaSessionAlias() bool {
	for _, a := range os.Args[1:] {
		if a == "--" {
			return false
		}
		if a == "worktree" {
			return false
		}
		if a == deprecatedSessionAlias {
			return true
		}
	}
	return false
}

// sessionListCmd lists per-session lifecycle states. Filter flags --repo,
// --status, --attached, --available all AND-combine. The flagless default
// shows every session in the current instance.
var sessionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List worktree lifecycle states with availability projection",
	Long: `List per-worktree lifecycle states.

Renders SESSION_ID, REPO, STATUS, AVAILABILITY, CREATED, PURPOSE for every
worktree in the current workspace instance. AVAILABILITY values are:

  available  no attach lock held; the worktree is free for niwa worktree attach
  attached   currently held by a niwa worktree attach process
  stale      a sentinel exists but the holder is dead; the lock is no longer
             effective and the next read will reap it

Filter flags AND-combine: --repo, --status, --attached, --available.
--attached and --available are mutually exclusive. Worktrees with
AVAILABILITY=stale appear under neither filter; run without filters to
see them.`,
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
)

func init() {
	sessionListCmd.Flags().StringVar(&sessionListRepo, "repo", "", "Filter by repo name")
	sessionListCmd.Flags().StringVar(&sessionListStatus, "status", "", "Filter by status: active, ended, abandoned")
	sessionListCmd.Flags().BoolVar(&sessionListAttached, "attached", false, "Show only sessions currently held by an attach lock")
	sessionListCmd.Flags().BoolVar(&sessionListAvailable, "available", false, "Show only sessions with no attach lock held")
	sessionListCmd.Flags().BoolVar(&sessionListJSON, "json", false, "Output JSON (one object per session, including the attach sub-object) instead of a table")
}

func runSessionList(cmd *cobra.Command, _ []string) error {
	return runSessionLifecycleList(cmd, sessionListRepo, sessionListStatus, sessionListAttached, sessionListAvailable)
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

// discoverInstanceRoot walks up from startDir to find the nearest
// directory containing .niwa/instance.json. Mirrors
// workspace.DiscoverInstance but avoids the circular import and lets
// tests override via NIWA_INSTANCE_ROOT without running an apply first.
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
