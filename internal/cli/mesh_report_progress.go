package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/mcp"
)

func init() {
	meshCmd.AddCommand(meshReportProgressCmd)
}

var meshReportProgressCmd = &cobra.Command{
	Use:          "report-progress",
	Short:        "Advance the stall watchdog deadline for the current worker task",
	SilenceUsage: true,
	RunE:         runMeshReportProgress,
}

func runMeshReportProgress(_ *cobra.Command, _ []string) error {
	taskID := os.Getenv("NIWA_TASK_ID")
	if taskID == "" {
		return nil
	}

	role := os.Getenv("NIWA_SESSION_ROLE")
	if role == "" {
		return fmt.Errorf("NIWA_SESSION_ROLE is not set")
	}
	instanceRoot := os.Getenv("NIWA_INSTANCE_ROOT")
	if instanceRoot == "" {
		return fmt.Errorf("NIWA_INSTANCE_ROOT is not set")
	}

	taskDir := filepath.Join(instanceRoot, ".niwa", "tasks", taskID)

	err := mcp.UpdateState(taskDir, func(cur *mcp.TaskState) (*mcp.TaskState, *mcp.TransitionLogEntry, error) {
		if cur.TaskID != taskID {
			return nil, nil, fmt.Errorf("task_id mismatch: state has %q, env has %q", cur.TaskID, taskID)
		}
		// Worker.Role is the role the daemon assigned to this worker process, set
		// when it transitions to running. It is the authoritative ownership field
		// (TargetRole is the intended role before spawn, which may differ if the
		// daemon reassigns; Worker.Role is what actually ran).
		if cur.Worker.Role != role {
			return nil, nil, fmt.Errorf("worker.role mismatch: state has %q, env has %q", cur.Worker.Role, role)
		}

		next := *cur
		summary := ""
		if cur.LastProgress != nil {
			summary = cur.LastProgress.Summary
		}
		next.LastProgress = &mcp.TaskProgress{
			Summary: summary,
			At:      time.Now().UTC().Format(time.RFC3339),
		}
		return &next, nil, nil
	})

	if errors.Is(err, mcp.ErrAlreadyTerminal) {
		return nil
	}
	return err
}
