package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/hostdir"
	"github.com/tsukumogami/niwa/internal/mirror"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func init() {
	rootCmd.AddCommand(dissociateCmd)
	dissociateCmd.ValidArgsFunction = completeInstanceNames
}

var dissociateCmd = &cobra.Command{
	Use:   "dissociate [instance]",
	Short: "Make an instance's clones independent of the workspace's mirrors",
	Long: `Dissociate copies every git object an instance's clones borrow from the
workspace's mirrors under ~/.niwa into each clone's own pack, then removes the
clone's alternates file. Afterwards the instance reads its whole history with
the mirrors deleted. Worktrees of those clones share their objects and are
covered too.

If no instance name is given, the current directory is used to discover the
enclosing instance.

Dissociate refuses, with nothing changed, while another niwa command holds the
instance's lock, and when free space on the instance's volume is below the
mirror pack sizes of the instance's repos. Every clone is copied before any
alternates file is removed, and a clone's file is removed only once git fsck
passes without it, so a failure part way leaves every clone still borrowing
and readable. A clone that doesn't borrow is left alone, and one whose
alternates file names anything outside the workspace's mirrors is reported and
left as it is. Running it again is safe: an instance with nothing left to
change prints "nothing to change".

Run it on an idle instance before copying or archiving it. The instance lock
covers niwa commands only, so a git command run in the instance meanwhile is
not blocked.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runDissociate,
}

// dissociateFunc is the engine runDissociate calls; a test replaces it to
// see what the command passes and to drive its output.
var dissociateFunc = mirror.Dissociate

func runDissociate(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	var nameArg string
	if len(args) > 0 {
		nameArg = args[0]
		// A present but empty name is a name that went missing
		// (`niwa dissociate "$NAME"` with NAME unset), not a request for the
		// enclosing instance.
		if strings.TrimSpace(nameArg) == "" {
			return fmt.Errorf("the instance name is empty; nothing was changed. " +
				"Pass the instance name, or run niwa dissociate with no argument from inside the instance")
		}
	}

	instanceDir, err := workspace.ResolveInstanceTarget(cwd, nameArg)
	if err != nil {
		return err
	}
	if err := workspace.ValidateInstanceDir(instanceDir); err != nil {
		return err
	}

	release, err := takeInstanceLock(instanceDir, "dissociate")
	if err != nil {
		return fmt.Errorf("nothing was changed: instance %s was kept: %s; run niwa dissociate again once it is done",
			filepath.Base(instanceDir), instanceLockReason(err))
	}
	defer release()

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("nothing was changed: finding the home directory that holds the mirrors: %w", err)
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	rep, runErr := dissociateFunc(ctx, mirror.DissociateOptions{
		Home:          home,
		WorkspaceRoot: filepath.Dir(instanceDir),
		Instance:      instanceDir,
		Clones:        hostdir.InstanceClones(instanceDir),
	})

	out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
	// A clone borrowing from outside this workspace's mirrors is a warning,
	// not a failure. What it names isn't a store niwa keeps or retires, so
	// once every other clone is freed the instance depends on none of niwa's
	// stores, which is all this command promises. Failing would also make
	// every rerun fail on a clone it will never change.
	for _, o := range rep.Outside {
		fmt.Fprintf(errOut, "warning: %s borrows from %s, outside this workspace's mirrors; left as it is\n",
			mirror.CleanText(o.Clone), mirror.CleanText(o.Line))
	}
	for _, c := range rep.Restored {
		fmt.Fprintf(errOut, "put back the alternates file of %s: git fsck failed without it\n", mirror.CleanText(c))
	}
	// Changed is printed even when the run stopped part way, so the clones
	// it already freed are listed.
	for _, c := range rep.Changed {
		fmt.Fprintf(out, "dissociated %s\n", mirror.CleanText(c))
	}
	if runErr != nil {
		return runErr
	}
	if len(rep.Changed) == 0 {
		fmt.Fprintln(out, "nothing to change")
	}
	return nil
}
