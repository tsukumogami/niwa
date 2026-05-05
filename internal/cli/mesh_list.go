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
	meshCmd.AddCommand(meshListCmd)
}

var meshListCmd = &cobra.Command{
	Use:   "list",
	Short: "List coordinator sessions registered in this workspace",
	Long: `List coordinator sessions registered in this workspace.

Only coordinator-role sessions appear in the registry — workers are
not registered (mesh messages route through per-role inboxes under
.niwa/roles/<role>/inbox/, not per-session). Columns:

  ROLE    PID    STATUS (alive/dead)    LAST-SEEN    PENDING

PENDING counts JSON envelopes directly in the coordinator's role
inbox directory (not in the in-progress/cancelled/expired subdirs).`,
	RunE: runMeshList,
}

func runMeshList(cmd *cobra.Command, args []string) error {
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

	sort.SliceStable(registry.Sessions, func(i, j int) bool {
		return registry.Sessions[i].Role < registry.Sessions[j].Role
	})

	writeMeshListTable(cmd.OutOrStdout(), instanceRoot, registry.Sessions)
	return nil
}

func writeMeshListTable(out io.Writer, instanceRoot string, sessions []mcp.SessionEntry) {
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
