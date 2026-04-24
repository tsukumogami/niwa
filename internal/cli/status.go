package cli

import (
	"encoding/json"
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
	statusCmd.Flags().BoolVar(&statusAuditSecrets, "audit-secrets", false,
		"classify every *.secrets table value (vault-ref/plaintext/empty/resolved); "+
			"exits non-zero when plaintext values are found and a vault is configured")
	statusCmd.Flags().BoolVar(&statusCheckVault, "check-vault", false,
		"re-resolve every vault:// reference (invokes providers) and report rotations "+
			"without materializing files")
	statusCmd.Flags().BoolVar(&statusVerbose, "verbose", false,
		"show full source attribution for every managed file")
	statusCmd.ValidArgsFunction = completeInstanceNames
}

var (
	statusAuditSecrets bool
	statusCheckVault   bool
	statusVerbose      bool
)

var statusCmd = &cobra.Command{
	Use:   "status [instance]",
	Short: "Show workspace instance status",
	Long: `Show the status of workspace instances.

When run from inside an instance, shows detailed status including repo clone
status and managed file drift. When run from the workspace root, shows a
summary table of all instances.

An optional instance name argument can be provided from the workspace root
to show detail for a specific instance.

Flags:
  --verbose         Show full source attribution for every managed file,
                    including the kind (plaintext, vault, .env.example)
                    and source ID for each input.
  --audit-secrets   Classify every *.secrets table value across the
                    workspace config (team + resolved overlay). Prints a
                    KEY / CLASSIFICATION / TABLE / SHADOWED table and
                    exits non-zero when any plaintext values are found
                    and a vault is configured.
  --check-vault     Re-resolve every vault:// reference by invoking the
                    configured providers and compare the resulting source
                    fingerprints against the stored state. Does NOT
                    materialize any files. Requires network access to
                    the backends.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runStatus,
}

func runStatus(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	if statusAuditSecrets {
		return runAuditSecrets(cmd, cwd)
	}
	if statusCheckVault {
		return runCheckVault(cmd, cwd)
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
	// PRD R20: display the resolved source slug + ref annotation when
	// state.ConfigSource is populated (v3 and later state files).
	if state.ConfigSource != nil && state.ConfigSource.URL != "" {
		ref := state.ConfigSource.Ref
		if ref == "" {
			ref = "(default branch)"
		}
		oid := state.ConfigSource.ResolvedCommit
		if len(oid) > 8 {
			oid = oid[:8]
		}
		fmt.Fprintf(out, "Source:   %s @ %s [%s]\n",
			state.ConfigSource.URL, ref, oid)
	}
	// PRD R36: display the discovered overlay slug on its own line when
	// an overlay was successfully cloned. NoOverlay or silent-skip cases
	// leave state.OverlayURL empty, which suppresses the line.
	if state.OverlayURL != "" {
		fmt.Fprintf(out, "Overlay:  %s\n", state.OverlayURL)
	}
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

	// Build a path → sources map for --verbose; populated from state so all
	// sources are available, not just the changed subset ComputeStatus returns.
	var fileSources map[string][]workspace.SourceEntry
	if statusVerbose {
		fileSources = make(map[string][]workspace.SourceEntry, len(state.ManagedFiles))
		for _, mf := range state.ManagedFiles {
			if len(mf.Sources) > 0 {
				fileSources[mf.Path] = mf.Sources
			}
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
			// For stale files, print the changed sources with their
			// old->new version tokens and provenance. Indent under
			// the file line so the attribution is visually grouped.
			for _, cs := range f.ChangedSources {
				fmt.Fprintf(out, "    changed source: %s://%s\n", sourceLabel(cs.Kind), cs.SourceID)
				if cs.Description != "" {
					fmt.Fprintf(out, "      note: %s\n", cs.Description)
				}
				if cs.OldToken != "" || cs.NewToken != "" {
					fmt.Fprintf(out, "      version: %s -> %s\n",
						shortToken(cs.OldToken), shortToken(cs.NewToken))
				}
				if cs.Provenance != "" {
					fmt.Fprintf(out, "      provenance: %s\n", cs.Provenance)
				}
			}
			// Under --verbose, show every source entry (not just changed ones).
			if statusVerbose {
				for _, se := range fileSources[f.Path] {
					fmt.Fprintf(out, "    source: %s://%s\n", sourceLabel(se.Kind), se.SourceID)
				}
			}
		}
	}

	// Mesh summary (cross-session-communication Issue #9). Printed
	// only when the workspace has channels opted in, which we detect
	// by combining two signals: (1) the instance has a `.niwa/` dir
	// (always true for a valid instance, but still asserted
	// defensively); (2) the workspace config parses with
	// [channels.mesh] present. When either is missing we skip the
	// summary entirely — AC says "Non-channeled workspace: no mesh
	// line".
	if meshLine := buildMeshSummary(instanceRoot); meshLine != "" {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Mesh: "+meshLine)
	}

	// Personal-overlay shadow summary. Omitted when the last apply
	// recorded zero shadows so the default happy-path output stays
	// clean. The `--audit-secrets` SHADOWED column is owned by
	// Issue 10.
	if len(state.Shadows) > 0 {
		fmt.Fprintln(out)
		suffix := "keys"
		if len(state.Shadows) == 1 {
			suffix = "key"
		}
		fmt.Fprintf(out, "%d %s shadowed by personal overlay (see niwa status --audit-secrets)\n",
			len(state.Shadows), suffix)
	}

	return nil
}

// sourceLabel maps a SourceKind constant to its display prefix.
// Unknown kinds fall back to the kind string itself.
func sourceLabel(kind string) string {
	switch kind {
	case workspace.SourceKindPlaintext:
		return "plaintext"
	case workspace.SourceKindVault:
		return "vault"
	case workspace.SourceKindEnvExample:
		return ".env.example"
	default:
		return kind
	}
}

// shortToken returns a compact form of a version token for display.
// Long opaque tokens (SHA-256 hex = 64 chars) are truncated to the
// first 12 characters + an ellipsis; short or empty tokens are
// returned verbatim. The truncation is display-only — the full token
// remains in state.json.
func shortToken(t string) string {
	if t == "" {
		return "(none)"
	}
	if len(t) > 14 {
		return t[:12] + "..."
	}
	return t
}

// buildMeshSummary returns the "Mesh: <queued> queued, <running> ..." line
// for the detail view, or "" when the workspace is not channeled or the
// `.niwa/tasks` directory does not exist. Counts are derived entirely
// from state.json files to stay consistent with `niwa task list`; 24-hour
// windows filter state_transitions for terminal (completed/abandoned)
// transitions.
//
// Any read error on an individual task is treated as a skip (same as the
// task-list path) so a single partial write doesn't break the summary
// line. The empty return value is the signal used by the caller to
// suppress the entire line.
func buildMeshSummary(instanceRoot string) string {
	if !isChanneledInstance(instanceRoot) {
		return ""
	}
	tasksDir := filepath.Join(instanceRoot, ".niwa", "tasks")
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		// Channels config present but no tasks dir yet: still print the
		// summary with zero counts so operators see the mesh is live.
		if os.IsNotExist(err) {
			return meshSummaryLine(0, 0, 0, 0)
		}
		return ""
	}

	var queued, running, completed24h, abandoned24h int
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		taskDir := filepath.Join(tasksDir, e.Name())
		data, err := os.ReadFile(filepath.Join(taskDir, "state.json"))
		if err != nil {
			continue
		}
		var st struct {
			State            string `json:"state"`
			StateTransitions []struct {
				From string `json:"from"`
				To   string `json:"to"`
				At   string `json:"at"`
			} `json:"state_transitions"`
		}
		if err := json.Unmarshal(data, &st); err != nil {
			continue
		}
		switch st.State {
		case "queued":
			queued++
		case "running":
			running++
		}
		// Walk transitions for terminal entries in the last 24h. A task
		// can only reach each terminal state once, so at most one
		// transition per task contributes to each count.
		for _, tr := range st.StateTransitions {
			if tr.To != "completed" && tr.To != "abandoned" {
				continue
			}
			t, err := time.Parse(time.RFC3339, tr.At)
			if err != nil {
				// Nanosecond-precision timestamps from transitions.log
				// use RFC3339Nano which still parses via RFC3339 on Go's
				// parser, so this branch only hits on malformed input.
				continue
			}
			if t.Before(cutoff) {
				continue
			}
			if tr.To == "completed" {
				completed24h++
			} else {
				abandoned24h++
			}
		}
	}
	return meshSummaryLine(queued, running, completed24h, abandoned24h)
}

func meshSummaryLine(queued, running, completed24h, abandoned24h int) string {
	return fmt.Sprintf("%d queued, %d running, %d completed (last 24h), %d abandoned (last 24h)",
		queued, running, completed24h, abandoned24h)
}

// isChanneledInstance returns true when the instance has channels opted
// in. The check combines two signals: (1) the instance has a `.niwa/`
// directory (required for any valid instance), and (2) the workspace
// config reachable from the instance root parses with [channels.mesh]
// present.
func isChanneledInstance(instanceRoot string) bool {
	if _, err := os.Stat(filepath.Join(instanceRoot, ".niwa")); err != nil {
		return false
	}
	configPath, _, err := config.Discover(instanceRoot)
	if err != nil {
		return false
	}
	result, err := config.Load(configPath)
	if err != nil {
		return false
	}
	return result.Config.Channels.IsEnabled()
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
