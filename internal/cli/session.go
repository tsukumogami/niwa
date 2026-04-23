package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/mcp"
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
  register    Register this session with the workspace mesh
  unregister  Remove this session from the workspace mesh
  list        List all registered sessions`,
}

// sessionListCmd renders the coordinator session registry. Workers
// intentionally do not register themselves (PRD R39, R40); the registry
// is scoped to coordinator roles so this listing is necessarily
// coordinator-only. The view shows liveness per entry so a stale PID
// (crashed coordinator that could not unregister cleanly) is visible
// to the operator.
var sessionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List coordinator sessions registered in this workspace",
	Long: `List coordinator sessions registered in this workspace.

Only coordinator-role sessions appear in the registry — workers are
not registered (mesh messages route through per-role inboxes under
.niwa/roles/<role>/inbox/, not per-session). Columns:

  ROLE    PID    STATUS (alive/dead)    LAST-SEEN    PENDING

PENDING counts JSON envelopes directly in the coordinator's role
inbox directory (not in the in-progress/cancelled/expired subdirs).`,
	RunE: runSessionList,
}

func runSessionList(cmd *cobra.Command, args []string) error {
	instanceRoot, err := resolveInstanceRoot()
	if err != nil {
		return err
	}

	registryPath := filepath.Join(instanceRoot, ".niwa", "sessions", "sessions.json")
	data, err := os.ReadFile(registryPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading sessions.json: %w", err)
	}

	var registry mcp.SessionRegistry
	if len(data) > 0 {
		if err := json.Unmarshal(data, &registry); err != nil {
			return fmt.Errorf("parsing sessions.json: %w", err)
		}
	}

	// Sort by role for a stable, human-scannable layout.
	sort.SliceStable(registry.Sessions, func(i, j int) bool {
		return registry.Sessions[i].Role < registry.Sessions[j].Role
	})

	writeSessionListTable(cmd.OutOrStdout(), instanceRoot, registry.Sessions)
	return nil
}

// writeSessionListTable renders the registered-session table. Headers
// always print — even on empty registries — so scripted consumers get
// a predictable first line and interactive users see the expected
// columns.
func writeSessionListTable(out io.Writer, instanceRoot string, sessions []mcp.SessionEntry) {
	fmt.Fprintf(out, "  %-16s %-8s %-10s %-14s %s\n",
		"ROLE", "PID", "STATUS", "LAST-SEEN", "PENDING")
	for _, s := range sessions {
		status := "dead"
		if mcp.IsPIDAlive(s.PID, s.StartTime) {
			status = "alive"
		}
		lastSeen := "-"
		if s.RegisteredAt != "" {
			if t, err := time.Parse(time.RFC3339, s.RegisteredAt); err == nil {
				lastSeen = formatRelativeTime(t)
			}
		}
		pending := countPendingInbox(instanceRoot, s.Role)
		fmt.Fprintf(out, "  %-16s %-8d %-10s %-14s %d\n",
			s.Role, s.PID, status, lastSeen, pending)
	}
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
