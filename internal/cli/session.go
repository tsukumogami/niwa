package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tsukumogami/niwa/internal/workspace"
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
//
// The shell wrapper mirrors these Aliases by hand -- shellWrapperTemplate
// (shell_init.go) matches `worktree|session`. A spelling added here without a
// matching token there silently loses the auto-cd for that spelling.
var sessionCmd = &cobra.Command{
	Use:     "worktree",
	Aliases: []string{deprecatedSessionAlias},
	Short:   "Manage git worktrees in the workspace",
	Long: `Manage git worktrees in the workspace.

Subcommands:
  apply     Re-sync an existing worktree's CLAUDE content (idempotent)
  attach    Attach to a worktree interactively (resume claude with full transcript)
  create    Create a new git worktree for a repo
  destroy   Destroy a worktree and remove its working directory
  detach    Release a stale attach lock (operator escape hatch)
  list      List worktree lifecycle states with availability projection`,
	// PersistentPreRun fires for the parent and every subcommand. When the
	// command was reached via the legacy "session" token on the command
	// line, emit a deprecation notice to stderr. Behavior and exit code are
	// unchanged; this is informational only.
	//
	// rootPersistentPreRun must be called explicitly. Cobra runs only the
	// nearest persistent pre-run in the parent chain -- it stops at the first
	// hook it finds unless EnableTraverseRunHooks is set, which niwa does not
	// set -- so declaring a hook here shadows the root's rather than adding to
	// it. Dropping it silently disabled NIWA_RESPONSE_FILE capture for every
	// worktree subcommand, which meant `niwa worktree create` wrote its landing
	// path nowhere and the shell never moved (#281). It also left the variable
	// exported to every child process, the inheritance the root hook exists to
	// prevent.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if err := rootPersistentPreRun(cmd, args); err != nil {
			return err
		}
		if invokedViaSessionAlias() {
			fmt.Fprintln(cmd.ErrOrStderr(),
				`"niwa session" is deprecated; use "niwa worktree"`)
		}
		return nil
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

// errAtWorkspaceRoot is returned by discoverInstanceRoot when the working
// directory is the root of a multi-instance workspace. The root is not an
// instance, and its .niwa holds the workspace's configuration snapshot and
// session mapping store rather than worktree records — so a worktree command
// that treated it as one would read the wrong store entirely.
//
// Callers match it with errors.Is. Its text is what Execute prints before
// exiting 1, so it reads as a redirect rather than a failure.
//
// atWorkspaceRootMessage is the message without a severity prefix, because
// `worktree list` prints the same redirect and exits 0: there is nothing wrong
// with listing worktrees at the root, there just aren't any there.
const atWorkspaceRootMessage = "this is the workspace root, not an instance; run inside an instance, or pass a session id to niwa worktree destroy"

var errAtWorkspaceRoot = errors.New("niwa: error: " + atWorkspaceRootMessage)

// discoverInstanceRoot resolves startDir to the instance it belongs to.
//
// It classifies rather than walking up for .niwa/instance.json. The walk was
// the bug behind #292: a workspace root carries its own instance.json (init
// persists init-time state there for `niwa create` to read), so the walk
// stopped at the root and every worktree command silently treated the root as
// instance zero. workspace.ClassifyCwd already distinguishes the two by looking
// for .niwa/workspace.toml, and it is the same classifier `niwa destroy` and
// `niwa apply` use.
func discoverInstanceRoot(startDir string) (string, error) {
	class, err := workspace.ClassifyCwd(startDir)
	if err != nil {
		return "", err
	}
	switch class.Class {
	case workspace.CwdInsideWorktree, workspace.CwdInsideInstance:
		return class.InstanceDir, nil
	case workspace.CwdAtWorkspaceRoot:
		// The single-instance layout is the one case where the root really is
		// the instance, and worktree commands there keep working as before.
		if workspace.IsSingleInstanceLayout(class.WorkspaceRoot) {
			return class.WorkspaceRoot, nil
		}
		return "", errAtWorkspaceRoot
	default:
		return "", fmt.Errorf("not inside a workspace instance (no .niwa/instance.json found walking up from %s)", startDir)
	}
}
