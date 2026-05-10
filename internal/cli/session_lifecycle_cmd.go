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
		return fmt.Errorf("%s", result.Content[0].Text)
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
		return fmt.Errorf("%s", result.Content[0].Text)
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
// repo and/or status. Called by sessionListCmd when flags are present.
func runSessionLifecycleList(cmd *cobra.Command, repo, status string) error {
	instanceRoot, err := resolveInstanceRoot()
	if err != nil {
		return err
	}

	sessionsDir := filepath.Join(instanceRoot, ".niwa", "sessions")
	all, err := mcp.ListSessionLifecycleStates(sessionsDir)
	if err != nil {
		return fmt.Errorf("listing sessions: %w", err)
	}

	var filtered []mcp.SessionLifecycleState
	for _, st := range all {
		if repo != "" && st.Repo != repo {
			continue
		}
		if status != "" && st.Status != status {
			continue
		}
		filtered = append(filtered, st)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].SessionID < filtered[j].SessionID
	})

	writeSessionLifecycleTable(cmd.OutOrStdout(), filtered)
	return nil
}

func writeSessionLifecycleTable(out interface{ Write([]byte) (int, error) }, sessions []mcp.SessionLifecycleState) {
	fmt.Fprintf(out, "  %-8s %-12s %-10s %-20s %s\n",
		"ID", "REPO", "STATUS", "CREATED", "PURPOSE")
	for _, s := range sessions {
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
		fmt.Fprintf(out, "  %-8s %-12s %-10s %-20s %s\n",
			s.SessionID, s.Repo, s.Status, created, purpose)
	}
}

