package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/mcp"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func init() {
	rootCmd.AddCommand(destroyCmd)
	destroyCmd.Flags().BoolVar(&destroyForce, "force", false, "skip uncommitted changes check")
	destroyCmd.ValidArgsFunction = completeInstanceNames
}

var destroyForce bool

var destroyCmd = &cobra.Command{
	Use:   "destroy [instance]",
	Short: "Destroy a workspace instance",
	Long: `Destroy a workspace instance and remove its directory.

If no instance name is given, the current directory is used to discover the
enclosing instance.

By default, destroy refuses to proceed if any cloned repository has uncommitted
changes. Use --force to skip this check.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runDestroy,
}

func runDestroy(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	var nameArg string
	if len(args) > 0 {
		nameArg = args[0]
	}

	instanceDir, err := workspace.ResolveInstanceTarget(cwd, nameArg)
	if err != nil {
		return err
	}

	if err := workspace.ValidateInstanceDir(instanceDir); err != nil {
		return err
	}

	if !destroyForce {
		dirty, err := workspace.CheckUncommittedChanges(instanceDir)
		if err != nil {
			return fmt.Errorf("checking for uncommitted changes: %w", err)
		}
		if len(dirty) > 0 {
			sort.Strings(dirty)
			fmt.Fprintf(cmd.ErrOrStderr(), "Repos with uncommitted changes:\n")
			for _, name := range dirty {
				fmt.Fprintf(cmd.ErrOrStderr(), "  %s\n", name)
			}
			return fmt.Errorf("instance has uncommitted changes in %d repo(s); use --force to override", len(dirty))
		}
	}

	// Terminate the mesh watch daemon if it is running.
	if err := terminateDaemon(instanceDir); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not stop mesh daemon: %v\n", err)
	}

	if err := workspace.DestroyInstance(instanceDir); err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Destroyed instance: %s\n", instanceDir)
	return nil
}

// terminateDaemon sends SIGTERM to the mesh watch daemon (if running), polls
// IsPIDAlive for up to 5 seconds, then sends SIGKILL if still alive. It removes
// daemon.pid when the daemon is confirmed dead or was never running.
func terminateDaemon(instanceRoot string) error {
	niwaDir := filepath.Join(instanceRoot, ".niwa")
	pid, startTime, err := ReadPIDFile(niwaDir)
	if err != nil {
		return fmt.Errorf("reading daemon pid: %w", err)
	}
	if pid == 0 {
		return nil // no daemon running
	}

	if !mcp.IsPIDAlive(pid, startTime) {
		_ = os.Remove(filepath.Join(niwaDir, "daemon.pid"))
		return nil
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(filepath.Join(niwaDir, "daemon.pid"))
		return nil
	}

	// Send SIGTERM and poll for up to 5 seconds.
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		_ = os.Remove(filepath.Join(niwaDir, "daemon.pid"))
		return nil
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		if !mcp.IsPIDAlive(pid, startTime) {
			_ = os.Remove(filepath.Join(niwaDir, "daemon.pid"))
			return nil
		}
	}

	// Still alive: send SIGKILL.
	_ = proc.Signal(syscall.SIGKILL)
	_ = os.Remove(filepath.Join(niwaDir, "daemon.pid"))
	return nil
}
