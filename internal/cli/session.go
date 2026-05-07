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
  create    Create a new git-worktree session for a repo
  destroy   Destroy a session and remove its worktree
  list      List sessions (use --repo or --status to filter lifecycle sessions;
            without flags delegates to 'niwa mesh list')`,
}

// sessionListCmd is the gateway for listing sessions. Without flags it
// delegates to 'niwa mesh list' (coordinator registry view) with a
// deprecation notice. With --repo or --status it shows lifecycle session
// states via niwa_list_sessions logic.
var sessionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List sessions (lifecycle view with --repo/--status; else deprecated coordinator alias)",
	Long: `List sessions in the workspace.

Without flags, this command is a deprecated alias for 'niwa mesh list' and
shows the coordinator process registry. Use 'niwa mesh list' instead.

With --repo or --status, shows lifecycle session states (worktree sessions
created via niwa_create_session / niwa session create).`,
	RunE: runSessionList,
}

var (
	sessionListRepo   string
	sessionListStatus string
)

func init() {
	sessionListCmd.Flags().StringVar(&sessionListRepo, "repo", "", "Filter by repo name")
	sessionListCmd.Flags().StringVar(&sessionListStatus, "status", "", "Filter by status: active, ended, abandoned")
}

func runSessionList(cmd *cobra.Command, args []string) error {
	if sessionListRepo == "" && sessionListStatus == "" {
		fmt.Fprintln(cmd.ErrOrStderr(), "warning: 'niwa session list' without flags is deprecated; use 'niwa mesh list' to list coordinator sessions")
		return runMeshList(cmd, args)
	}
	return runSessionLifecycleList(cmd, sessionListRepo, sessionListStatus)
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
