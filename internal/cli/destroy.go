package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/tui"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func init() {
	rootCmd.AddCommand(destroyCmd)
	destroyCmd.Flags().BoolVar(&destroyForce, "force", false,
		"skip uncommitted changes check; at the workspace root with no instance name, "+
			"wipe the entire workspace after a non-pushed-work scan")
	destroyCmd.ValidArgsFunction = completeInstanceNames
}

var destroyForce bool

var destroyCmd = &cobra.Command{
	Use:   "destroy [instance]",
	Short: "Destroy a workspace instance or the entire workspace",
	Long: `Destroy a workspace instance or, with --force at the workspace root, the
entire workspace.

Behavior depends on where you run it from and which arguments are provided:

  Inside an instance:
    niwa destroy            destroys the enclosing instance and lands the
                            shell at the workspace root.
    niwa destroy <name>     rejected — name is only valid from the workspace
                            root.

  At the workspace root:
    niwa destroy <name>     destroys the named instance.
    niwa destroy            (no instances) deletes the workspace itself.
                            (one instance)  destroys that instance.
                            (≥2 instances)  shows an interactive picker.
    niwa destroy --force    wipes the entire workspace after scanning every
                            instance for non-pushed work; if any work would
                            be lost, prints a list and prompts for typed
                            confirmation.

By default, destroy refuses to proceed if any cloned repository has
uncommitted changes. Use --force to skip this check.

Non-TTY behavior: the picker and typed-confirmation prompt require a
terminal on stdin. In CI or scripted environments, pass an explicit
instance name or use --force.`,
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

	class, err := workspace.ClassifyCwd(cwd)
	if err != nil {
		return fmt.Errorf("classifying working directory: %w", err)
	}

	switch class.Class {
	case workspace.CwdOutside:
		return fmt.Errorf("not inside a niwa workspace or instance")

	case workspace.CwdInsideInstance:
		if nameArg != "" {
			return fmt.Errorf("instance name is only valid from the workspace root; " +
				"run `niwa destroy` (no arguments) to destroy the enclosing instance")
		}
		return runDestroyInstance(cmd, class.InstanceDir, class.WorkspaceRoot, destroyForce)

	case workspace.CwdAtWorkspaceRoot:
		return runDestroyAtRoot(cmd, class.WorkspaceRoot, nameArg, destroyForce)
	}

	return fmt.Errorf("internal error: unhandled cwd class %s", class.Class)
}

// runDestroyInstance destroys a single instance directory. Used for:
//   - destroy from inside an instance (writes landing path = workspace root)
//   - destroy by name from workspace root (no landing path written)
//   - destroy with no name from workspace root when only one instance exists
//     (no landing path written)
//
// landingPath, when non-empty, is written via writeLandingPath after a
// successful RemoveAll so the shell wrapper drops the user out of any
// directory destroy just removed.
func runDestroyInstance(cmd *cobra.Command, instanceDir, landingPath string, force bool) error {
	if err := workspace.ValidateInstanceDir(instanceDir); err != nil {
		return err
	}

	if !force {
		scan, err := workspace.ScanInstance(instanceDir)
		if err != nil {
			return fmt.Errorf("scanning instance for unpushed work: %w", err)
		}
		if scan.HasLoss() {
			workspace.FormatScans([]workspace.InstanceScan{scan}, cmd.ErrOrStderr(), scan.InstanceName)

			if !IsStdinTTY() {
				return fmt.Errorf("instance has unpushed work and stdin is not a terminal; aborting (resolve unpushed work, or use --force to destroy without confirmation)")
			}

			matched, err := ReadConfirmation("> ", scan.InstanceName, os.Stdin, cmd.ErrOrStderr())
			if err != nil {
				return fmt.Errorf("confirmation aborted: %w", err)
			}
			if !matched {
				return fmt.Errorf("confirmation did not match instance name; aborting")
			}
		}
	}

	if err := workspace.TerminateDaemon(instanceDir); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not stop mesh daemon: %v\n", err)
	}

	if err := workspace.DestroyInstance(instanceDir); err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Destroyed instance: %s\n", instanceDir)

	if landingPath != "" {
		if err := writeLandingPath(landingPath); err != nil {
			return fmt.Errorf("writing landing path: %w", err)
		}
		hintShellInit(cmd)
	}
	return nil
}

// runDestroyAtRoot dispatches the workspace-root cases:
//   - nameArg non-empty → destroy the named instance
//   - nameArg empty + 0 instances → destroy the entire workspace (empty case)
//   - nameArg empty + 1 instance → destroy that instance directly
//   - nameArg empty + ≥2 instances + force → workspace wipe (with scan + prompt)
//   - nameArg empty + ≥2 instances + no force → picker
func runDestroyAtRoot(cmd *cobra.Command, workspaceRoot, nameArg string, force bool) error {
	instances, err := workspace.EnumerateInstances(workspaceRoot)
	if err != nil {
		return fmt.Errorf("enumerating instances: %w", err)
	}

	// Named: today's flow. Even at the workspace root, force passes through
	// to the per-instance dirty-check bypass.
	if nameArg != "" {
		instanceDir, err := resolveInstanceByNameAtRoot(instances, nameArg)
		if err != nil {
			return err
		}
		return runDestroyInstance(cmd, instanceDir, "", force)
	}

	// No name. Branch by --force and instance count.
	if force {
		// Workspace wipe path (handles empty workspace too — scan over zero
		// instances yields no losses and the prompt is skipped).
		return runDestroyWorkspace(cmd, workspaceRoot, instances)
	}

	switch len(instances) {
	case 0:
		// Empty workspace: delete the whole thing without --force, lands
		// at the workspace parent.
		return runDestroyEmptyWorkspace(cmd, workspaceRoot)
	case 1:
		// Single-instance shortcut: skip the picker.
		return runDestroyInstance(cmd, instances[0], "", force)
	default:
		// Picker case.
		return runDestroyPick(cmd, instances, force)
	}
}

// resolveInstanceByNameAtRoot finds an instance whose InstanceName matches
// nameArg. Preserves the existing error wording from
// internal/workspace/destroy.go's resolveInstanceByName helper so users
// and scripts that match on the error string continue to work.
func resolveInstanceByNameAtRoot(instances []string, nameArg string) (string, error) {
	for _, dir := range instances {
		state, loadErr := workspace.LoadState(dir)
		if loadErr != nil {
			continue
		}
		if state.InstanceName == nameArg {
			return dir, nil
		}
	}

	var available []string
	for _, dir := range instances {
		state, loadErr := workspace.LoadState(dir)
		if loadErr != nil {
			continue
		}
		available = append(available, state.InstanceName)
	}

	if len(available) == 0 {
		return "", fmt.Errorf("instance %q not found: no instances exist in workspace", nameArg)
	}
	sort.Strings(available)
	return "", fmt.Errorf("instance %q not found, available instances: %s", nameArg, strings.Join(available, ", "))
}

// runDestroyPick presents an interactive picker over the given instances
// and destroys the chosen one. Refuses with a helpful error when stdin
// is not a TTY.
func runDestroyPick(cmd *cobra.Command, instances []string, force bool) error {
	type entry struct {
		dir  string
		name string
	}

	var entries []entry
	for _, dir := range instances {
		state, err := workspace.LoadState(dir)
		if err != nil {
			continue
		}
		entries = append(entries, entry{dir: dir, name: state.InstanceName})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	if !IsStdinTTY() || !tui.IsAvailable() {
		fmt.Fprintf(cmd.ErrOrStderr(), "Workspace has multiple instances:\n")
		for _, e := range entries {
			fmt.Fprintf(cmd.ErrOrStderr(), "  %s\n", e.name)
		}
		return fmt.Errorf("no instance specified and not running in a terminal; pass an instance name or use --force to wipe the workspace")
	}

	choices := make([]tui.Choice, 0, len(entries))
	for _, e := range entries {
		choices = append(choices, tui.Choice{Name: e.name})
	}

	idx, err := tui.Pick("Pick an instance to destroy:", choices)
	if err != nil {
		// Includes ErrCanceled — treat as user-driven abort.
		fmt.Fprintln(cmd.ErrOrStderr(), "Canceled.")
		return err
	}

	return runDestroyInstance(cmd, entries[idx].dir, "", force)
}

// runDestroyEmptyWorkspace deletes the workspace root when no instances
// exist. No --force required (empty case has no work to lose). Writes
// a landing path equal to the workspace's parent so the shell wrapper
// drops the user out of the deleted directory.
func runDestroyEmptyWorkspace(cmd *cobra.Command, workspaceRoot string) error {
	parent := filepath.Dir(workspaceRoot)

	if err := workspace.DestroyWorkspace(workspaceRoot, workspace.DestroyWorkspaceOpts{}); err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Destroyed workspace: %s\n", workspaceRoot)

	if parent != "" {
		if err := writeLandingPath(parent); err != nil {
			return fmt.Errorf("writing landing path: %w", err)
		}
		hintShellInit(cmd)
	}
	return nil
}

// runDestroyWorkspace handles the `niwa destroy --force` workspace-wipe
// path: scan every instance for non-pushed work; if any is found,
// print the loss listing and require typed confirmation against the
// workspace name; on success (clean scan or matched confirmation),
// destroy every instance and the workspace root, landing the shell
// at the workspace parent.
//
// The typed-confirmation prompt fires BEFORE writeLandingPath. If the
// user mismatches or hits EOF, the workspace stays intact AND no
// landing path is written, so the shell stays where it was.
func runDestroyWorkspace(cmd *cobra.Command, workspaceRoot string, instances []string) error {
	// Empty workspace under --force still works — pass through.
	if len(instances) == 0 {
		return runDestroyEmptyWorkspace(cmd, workspaceRoot)
	}

	// Scan for non-pushed work.
	scans, scanErr := workspace.ScanInstancesParallel(workspaceRoot, instances, 0)
	if scanErr != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: scan reported errors: %v\n", scanErr)
		// Continue — individual repo errors are surfaced in scans[*].Skipped
		// and a typed-confirmation prompt will still fire.
	}

	anyLoss := false
	for _, s := range scans {
		if s.HasLoss() {
			anyLoss = true
			break
		}
	}

	if anyLoss {
		// Resolve workspace name for the confirmation token.
		workspaceName, err := loadWorkspaceName(workspaceRoot)
		if err != nil {
			return fmt.Errorf("resolving workspace name for confirmation prompt: %w", err)
		}

		workspace.FormatScans(scans, cmd.ErrOrStderr(), workspaceName)

		if !IsStdinTTY() {
			return fmt.Errorf("workspace has unpushed work and stdin is not a terminal; aborting (resolve unpushed work or run from a terminal to confirm)")
		}

		matched, err := ReadConfirmation("> ", workspaceName, os.Stdin, cmd.ErrOrStderr())
		if err != nil {
			return fmt.Errorf("confirmation aborted: %w", err)
		}
		if !matched {
			return fmt.Errorf("confirmation did not match workspace name; aborting")
		}
	}

	// Confirmed (or clean): proceed with the wipe.
	parent := filepath.Dir(workspaceRoot)

	if err := workspace.DestroyWorkspace(workspaceRoot, workspace.DestroyWorkspaceOpts{
		ProgressOut: cmd.ErrOrStderr(),
	}); err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Destroyed workspace: %s\n", workspaceRoot)

	if parent != "" {
		if err := writeLandingPath(parent); err != nil {
			return fmt.Errorf("writing landing path: %w", err)
		}
		hintShellInit(cmd)
	}
	return nil
}

// loadWorkspaceName reads .niwa/workspace.toml at workspaceRoot and
// returns the EffectiveConfigName-resolved workspace name (honoring any
// `niwa init <name>` override).
func loadWorkspaceName(workspaceRoot string) (string, error) {
	configPath := filepath.Join(workspaceRoot, config.ConfigDir, config.ConfigFile)
	result, err := config.Load(configPath)
	if err != nil {
		return "", fmt.Errorf("loading workspace config: %w", err)
	}
	return resolveEffectiveWorkspaceName(workspaceRoot, result.Config)
}
