package cli

import (
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func init() {
	rootCmd.AddCommand(destroyCmd)
	destroyCmd.Flags().BoolVar(&destroyForce, "force", false, "skip uncommitted changes check")
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

	if err := workspace.DestroyInstance(instanceDir); err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Destroyed instance: %s\n", instanceDir)
	return nil
}
