package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func init() {
	rootCmd.AddCommand(listCmd)
	listCmd.Flags().BoolVar(&listJSON, "json", false,
		"emit a JSON array of {name, path, ephemeral[, keep_alive]} records, one per instance")
}

var listJSON bool

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List workspace instances",
	Long: `List the instances under the current workspace root.

Run from inside a workspace (at the root or inside any instance); list
resolves the workspace root from the current directory and enumerates every
instance beneath it.

With --json, emits a JSON array of {name, path, ephemeral} records, where
ephemeral marks instances backed by an ephemeral session mapping. An
instance whose session was dispatched with keep-alive armed and is still
live additionally carries keep_alive:true (and a "(keep-alive)" marker in
the human output).`,
	Args: cobra.NoArgs,
	RunE: runList,
}

func runList(cmd *cobra.Command, args []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	class, err := workspace.ClassifyCwd(cwd)
	if err != nil {
		return fmt.Errorf("classifying working directory: %w", err)
	}
	if class.Class == workspace.CwdOutside {
		return fmt.Errorf("not inside a niwa workspace or instance")
	}

	records, err := workspace.EnumerateInstanceRecords(class.WorkspaceRoot)
	if err != nil {
		return fmt.Errorf("enumerating instances: %w", err)
	}
	annotateKeepAlive(records, class.WorkspaceRoot, defaultJobsDir(), time.Now())

	if listJSON {
		// Always emit a JSON array (never null) so consumers can iterate
		// unconditionally, even when no instances exist.
		if records == nil {
			records = []workspace.InstanceRecord{}
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		if err := enc.Encode(records); err != nil {
			return fmt.Errorf("encoding list JSON: %w", err)
		}
		return nil
	}

	if len(records) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No instances found.")
		return nil
	}
	for _, r := range records {
		if r.KeepAlive {
			fmt.Fprintf(cmd.OutOrStdout(), "%s (keep-alive)\n", r.Name)
			continue
		}
		fmt.Fprintln(cmd.OutOrStdout(), r.Name)
	}
	return nil
}

// annotateKeepAlive fills each record's KeepAlive flag by joining the
// workspace's session mapping store with the Claude Code job-entry liveness
// signal: an instance is reported kept-alive when some mapping points at it
// with KeepAlive recorded AND that mapping's session is still live (its job
// entry exists, the same rule the reaper uses). A kept-alive session that has
// since been deleted reports nothing -- its self-wake died with the session,
// so the report reflects sessions being kept alive NOW, not past opt-ins. A
// store read failure degrades to no annotation; list must stay usable with a
// partially written store.
func annotateKeepAlive(records []workspace.InstanceRecord, workspaceRoot, jobsDir string, now time.Time) {
	mappings, err := workspace.ListSessionMappings(workspaceRoot)
	if err != nil || len(mappings) == 0 {
		return
	}
	keptAlive := make(map[string]bool)
	for _, m := range mappings {
		if m.KeepAlive && sessionLive(jobsDir, m.SessionID, now) {
			keptAlive[m.InstancePath] = true
		}
	}
	for i := range records {
		if keptAlive[records[i].Path] {
			records[i].KeepAlive = true
		}
	}
}
