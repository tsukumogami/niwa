package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func init() {
	rootCmd.AddCommand(statusCmd)
}

var statusCmd = &cobra.Command{
	Use:   "status [instance]",
	Short: "Show workspace instance status",
	Long: `Show the status of workspace instances.

When run from inside an instance, shows detailed status including repo clone
status and managed file drift. When run from the workspace root, shows a
summary table of all instances.

An optional instance name argument can be provided from the workspace root
to show detail for a specific instance.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runStatus,
}

func runStatus(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	// If an instance name was provided, find it and show detail.
	if len(args) == 1 {
		return runStatusByName(cmd, cwd, args[0])
	}

	// Try instance discovery first (detail view).
	instanceRoot, err := workspace.DiscoverInstance(cwd)
	if err == nil {
		return showDetailView(cmd, instanceRoot)
	}

	// Fall back to config discovery (summary view).
	_, configDir, err := config.Discover(cwd)
	if err != nil {
		return fmt.Errorf("not inside a workspace instance or workspace root: %w", err)
	}

	workspaceRoot := filepath.Dir(configDir)
	return showSummaryView(cmd, workspaceRoot)
}

func runStatusByName(cmd *cobra.Command, cwd, name string) error {
	_, configDir, err := config.Discover(cwd)
	if err != nil {
		return fmt.Errorf("finding workspace root: %w", err)
	}

	workspaceRoot := filepath.Dir(configDir)
	instances, err := workspace.EnumerateInstances(workspaceRoot)
	if err != nil {
		return fmt.Errorf("enumerating instances: %w", err)
	}

	for _, dir := range instances {
		state, loadErr := workspace.LoadState(dir)
		if loadErr != nil {
			continue
		}
		if state.InstanceName == name {
			return showDetailView(cmd, dir)
		}
	}

	// Build available names for the error message.
	var available []string
	for _, dir := range instances {
		state, loadErr := workspace.LoadState(dir)
		if loadErr != nil {
			continue
		}
		available = append(available, state.InstanceName)
	}

	if len(available) == 0 {
		return fmt.Errorf("instance %q not found: no instances exist in workspace", name)
	}
	return fmt.Errorf("instance %q not found, available instances: %s", name, strings.Join(available, ", "))
}

func showSummaryView(cmd *cobra.Command, workspaceRoot string) error {
	instances, err := workspace.EnumerateInstances(workspaceRoot)
	if err != nil {
		return fmt.Errorf("enumerating instances: %w", err)
	}

	if len(instances) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No instances found.")
		return nil
	}

	fmt.Fprintln(cmd.OutOrStdout(), "Instances:")
	fmt.Fprintln(cmd.OutOrStdout())

	for _, dir := range instances {
		state, loadErr := workspace.LoadState(dir)
		if loadErr != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not load state for %s: %v\n", dir, loadErr)
			continue
		}

		status, statusErr := workspace.ComputeStatus(state, dir)
		if statusErr != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not compute status for %s: %v\n", dir, statusErr)
			continue
		}

		repoCount := len(status.Repos)
		driftLabel := "drifted"
		if status.DriftCount == 0 {
			driftLabel = "drifted"
		}
		appliedAgo := formatRelativeTime(status.LastApplied)

		fmt.Fprintf(cmd.OutOrStdout(), "  %-12s %d repos   %d %s   applied %s\n",
			status.Name, repoCount, status.DriftCount, driftLabel, appliedAgo)
	}

	return nil
}

func showDetailView(cmd *cobra.Command, instanceRoot string) error {
	state, err := workspace.LoadState(instanceRoot)
	if err != nil {
		return fmt.Errorf("loading instance state: %w", err)
	}

	status, err := workspace.ComputeStatus(state, instanceRoot)
	if err != nil {
		return fmt.Errorf("computing status: %w", err)
	}

	out := cmd.OutOrStdout()

	fmt.Fprintf(out, "Instance: %s\n", status.Name)
	fmt.Fprintf(out, "Config:   %s\n", status.ConfigName)
	fmt.Fprintf(out, "Root:     %s\n", status.Root)
	fmt.Fprintf(out, "Created:  %s\n", status.Created.Format("2006-01-02 15:04"))
	fmt.Fprintf(out, "Applied:  %s\n", status.LastApplied.Format("2006-01-02 15:04"))

	if len(status.Repos) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Repos:")

		// Sort repos by name for stable output.
		sorted := make([]workspace.RepoStatus, len(status.Repos))
		copy(sorted, status.Repos)
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].Name < sorted[j].Name
		})

		for _, r := range sorted {
			fmt.Fprintf(out, "  %-12s %s\n", r.Name, r.Status)
		}
	}

	if len(status.Files) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Managed files:")

		for _, f := range status.Files {
			// Show path relative to instance root when possible.
			displayPath := f.Path
			if rel, err := filepath.Rel(status.Root, f.Path); err == nil {
				displayPath = rel
			}
			fmt.Fprintf(out, "  %-40s %s\n", displayPath, f.Status)
		}
	}

	return nil
}

// formatRelativeTime returns a human-readable relative time string.
func formatRelativeTime(t time.Time) string {
	d := time.Since(t)

	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		mins := int(d.Minutes())
		if mins == 1 {
			return "1m ago"
		}
		return fmt.Sprintf("%dm ago", mins)
	case d < 24*time.Hour:
		hours := int(d.Hours())
		if hours == 1 {
			return "1h ago"
		}
		return fmt.Sprintf("%dh ago", hours)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1d ago"
		}
		return fmt.Sprintf("%dd ago", days)
	}
}
