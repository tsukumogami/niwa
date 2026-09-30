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
		"skip the unpushed-work check when destroying one instance; never wipes the workspace")
	destroyCmd.Flags().BoolVar(&destroyWorkspace, "workspace", false,
		"at the workspace root, destroy every instance and the workspace itself; always asks for the workspace name")
	destroyCmd.Flags().StringVar(&destroyConfirm, "confirm", "",
		"with --workspace, the workspace name, given up front instead of typed at the prompt (for non-interactive use)")
	destroyCmd.ValidArgsFunction = completeInstanceNames
}

var (
	destroyForce     bool
	destroyWorkspace bool
	destroyConfirm   string
)

var destroyCmd = &cobra.Command{
	Use:   "destroy [instance]",
	Short: "Destroy a workspace instance or the entire workspace",
	Long: `Destroy a workspace instance or, with --workspace at the workspace root, the
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
    niwa destroy --workspace
                            destroys every instance and the workspace itself.
                            It scans every instance for non-pushed work and
                            lists any it finds, then always asks you to type
                            the workspace name.
    niwa destroy --workspace --confirm <workspace-name>
                            the same, without the prompt: the name is checked
                            against the workspace's own name instead.

  Anywhere else under the workspace root (a directory that is not an
  instance, a worktree, or the root itself), destroy refuses.

By default, destroy refuses to proceed if any cloned repository has
uncommitted changes. Use --force <name> to skip this check for one instance.
--force never wipes the workspace: without an instance name at the workspace
root it refuses and points at --workspace.

Non-TTY behavior: the picker and typed-confirmation prompt require a
terminal on stdin. In CI or scripted environments, pass an explicit
instance name, or --workspace --confirm <workspace-name>.`,
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
		// An argument that is present but empty is a name that went missing
		// (`niwa destroy "$NAME"` with NAME unset), not a request for the
		// no-name modes, which can delete an empty workspace or the only
		// instance. Refuse it the way a bare --force is refused.
		if strings.TrimSpace(nameArg) == "" {
			return fmt.Errorf("the instance name is empty; nothing was destroyed. " +
				"Pass the instance name, or run `niwa destroy --workspace` to destroy every instance and the workspace itself")
		}
	}

	class, err := workspace.ClassifyCwd(cwd)
	if err != nil {
		return fmt.Errorf("classifying working directory: %w", err)
	}
	// The scope rule apply shares: from a directory under the root that is
	// not an instance, a worktree or the root itself, refuse outright rather
	// than take the root's scope.
	if err := class.RefuseBelowRoot(cwd, "niwa destroy"); err != nil {
		return err
	}

	if destroyConfirm != "" && !destroyWorkspace {
		return fmt.Errorf("--confirm only applies to --workspace")
	}
	if destroyWorkspace {
		if class.Class != workspace.CwdAtWorkspaceRoot {
			return fmt.Errorf("--workspace is only valid at the workspace root")
		}
		if nameArg != "" {
			return fmt.Errorf("--workspace destroys every instance and takes no instance name; " +
				"to destroy one instance, run `niwa destroy <name>` without --workspace")
		}
		return runDestroyWorkspace(cmd, class.WorkspaceRoot, destroyConfirm)
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
		scan, err := workspace.ScanInstance(instanceDir, workspace.WithForge(newDestroyForge()))
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

	if err := workspace.DestroyInstance(instanceDir,
		workspace.WithDestroyReporter(workspace.NewReporter(cmd.ErrOrStderr()))); err != nil {
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
//   - nameArg empty + force → refuse; wiping the workspace is --workspace
//   - nameArg empty + 1 instance → destroy that instance directly
//   - nameArg empty + ≥2 instances → picker
//
// --workspace never reaches here; runDestroy sends it to runDestroyWorkspace.
func runDestroyAtRoot(cmd *cobra.Command, workspaceRoot, nameArg string, force bool) error {
	// --force used to mean "wipe the workspace" when the name was missing, so
	// a teardown script that lost its instance name to an empty variable
	// wiped every instance on the machine. Refuse before enumerating anything.
	if nameArg == "" && force {
		return fmt.Errorf("--force without an instance name does not destroy anything: " +
			"to destroy one instance, run `niwa destroy --force <name>`; " +
			"to destroy every instance and the workspace itself, run `niwa destroy --workspace`")
	}

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

	// No name and no --force. Branch by instance count.
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
		return fmt.Errorf("no instance specified and not running in a terminal; pass an instance name, or use --workspace --confirm <workspace-name> to destroy the whole workspace")
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

// runDestroyWorkspace handles `niwa destroy --workspace`: scan every
// instance for non-pushed work and list any it finds, then require the
// workspace name, typed at a prompt or passed as confirm, whether or not the
// scan found anything. Only then destroy every instance and the workspace
// root, landing the shell at the workspace parent.
//
// The confirmation is unconditional because this is the one command that
// removes every other session's instance too; a clean scan says nothing about
// whether anyone meant to do that. Without a terminal it refuses unless
// confirm names the workspace, so an empty variable can never satisfy it.
//
// The confirmation fires BEFORE anything is removed and before
// writeLandingPath. On a mismatch or EOF the workspace stays intact AND no
// landing path is written, so the shell stays where it was.
func runDestroyWorkspace(cmd *cobra.Command, workspaceRoot, confirm string) error {
	instances, err := workspace.EnumerateInstances(workspaceRoot)
	if err != nil {
		return fmt.Errorf("enumerating instances: %w", err)
	}

	workspaceName, err := loadWorkspaceName(workspaceRoot)
	if err != nil {
		return fmt.Errorf("resolving workspace name for confirmation: %w", err)
	}

	// Scan for non-pushed work, so whoever confirms sees what would be lost.
	scans, scanErr := workspace.ScanInstancesParallel(workspaceRoot, instances, 0, workspace.WithForge(newDestroyForge()))
	if scanErr != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: scan reported errors: %v\n", scanErr)
		// Continue — individual repo errors are surfaced in scans[*].Skipped
		// and the confirmation below still fires.
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "This destroys %d instance(s) and the workspace at %s.\n", len(instances), workspaceRoot)
	promptName := workspaceName
	if confirm != "" {
		// No prompt follows, so don't print its "Type ..." line.
		promptName = ""
	}
	workspace.FormatScans(scans, cmd.ErrOrStderr(), promptName)

	if confirm != "" {
		if confirm != workspaceName {
			return fmt.Errorf("--confirm %q does not match the workspace name %q; aborting", confirm, workspaceName)
		}
	} else {
		if !IsStdinTTY() {
			return fmt.Errorf("--workspace needs the workspace name and stdin is not a terminal; aborting (pass --confirm %s to confirm without a prompt)", workspaceName)
		}
		anyLoss := false
		for _, s := range scans {
			anyLoss = anyLoss || s.HasLoss()
		}
		if !anyLoss {
			// FormatScans prints the prompt line only when it lists losses.
			fmt.Fprintf(cmd.ErrOrStderr(), `Type "%s" to confirm deletion (or Ctrl-C to abort):`+"\n", workspaceName)
		}
		matched, err := ReadConfirmation("> ", workspaceName, os.Stdin, cmd.ErrOrStderr())
		if err != nil {
			return fmt.Errorf("confirmation aborted: %w", err)
		}
		if !matched {
			return fmt.Errorf("confirmation did not match workspace name; aborting")
		}
	}

	parent := filepath.Dir(workspaceRoot)

	if err := workspace.DestroyWorkspace(workspaceRoot, workspace.DestroyWorkspaceOpts{
		Reporter:    workspace.NewReporter(cmd.ErrOrStderr()),
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
